package importer

import (
	"regexp"
	"sort"
	"strings"
)

// ErrorHint provides actionable guidance for common terraform errors.
// When terraform reports an error, it often uses generic language that
// sounds like a provider bug ("this is always a provider issue") when the
// actual cause is a user configuration mistake (missing required variable,
// incorrect UUID, etc.). ErrorHint detects these patterns and provides
// clear, actionable guidance.
type ErrorHint struct {
	// Summary is a one-line description of the likely cause.
	Summary string
	// Details provides multi-line guidance on how to fix the issue.
	Details string
	// IsUserConfig indicates whether this is likely a user configuration
	// issue rather than a provider bug.
	IsUserConfig bool
	// IsRetryable indicates whether this error might resolve on retry
	// (e.g., transient git errors, race conditions).
	IsRetryable bool
}

// ClassifyError analyzes a terraform error message and returns an ErrorHint
// if the error matches a known pattern. Returns nil if no pattern matches.
func ClassifyError(errMsg string) *ErrorHint {
	if errMsg == "" {
		return nil
	}

	// Check for transient git/module errors first (retryable)
	if h := checkTransientGitError(errMsg); h != nil {
		return h
	}

	// Check for stale plan errors (retryable)
	if h := checkStalePlanError(errMsg); h != nil {
		return h
	}

	// Check for provider internal errors that are actually user config issues
	if h := checkProviderInternalError(errMsg); h != nil {
		return h
	}

	// Check for model not found errors (user config issue)
	if h := checkModelError(errMsg); h != nil {
		return h
	}

	// Check for missing required argument errors
	if h := checkMissingRequiredArgument(errMsg); h != nil {
		return h
	}

	// Check for provider configuration errors (often misattributed to provider)
	if h := checkProviderConfigError(errMsg); h != nil {
		return h
	}

	// Check for variable validation errors
	if h := checkVariableValidationError(errMsg); h != nil {
		return h
	}

	// Check for connection/authentication errors
	if h := checkConnectionError(errMsg); h != nil {
		return h
	}

	// Check for invalid input errors
	if h := checkInvalidInputError(errMsg); h != nil {
		return h
	}

	return nil
}

// checkTransientGitError detects transient git errors that are retryable.
// These typically happen due to race conditions, network issues, or stale state.
func checkTransientGitError(errMsg string) *ErrorHint {
	if !strings.Contains(errMsg, "Failed to download module") {
		return nil
	}

	errLower := strings.ToLower(errMsg)

	// "could not lock config file" - stale git state from parallel runs
	if strings.Contains(errLower, "could not lock config file") {
		return &ErrorHint{
			Summary: "Module download failed (transient git error)",
			Details: "A parallel terraform process may have left stale git state.\n" +
				"This is a transient issue — retry the operation.\n\n" +
				"To fix this:\n" +
				"  1. Wait a moment and retry the operation\n" +
				"  2. If persistent, run: rm -rf .terraform/modules/<module_name>\n" +
				"  3. Then run: terraform init -upgrade",
			IsUserConfig: false,
			IsRetryable:  true,
		}
	}

	// "already exists and is not an empty directory" - race condition
	if strings.Contains(errLower, "already exists and is not an empty directory") {
		return &ErrorHint{
			Summary: "Module download failed (race condition)",
			Details: "Another terraform process is currently downloading this module.\n" +
				"This is a transient issue — retry the operation.\n\n" +
				"To fix this:\n" +
				"  1. Wait for the other terraform process to complete\n" +
				"  2. If no other process is running, run: rm -rf .terraform/modules/<module_name>\n" +
				"  3. Then run: terraform init -upgrade",
			IsUserConfig: false,
			IsRetryable:  true,
		}
	}

	// "could not open ... for reading: No such file or directory" - partial clone
	if strings.Contains(errLower, "could not open") &&
		strings.Contains(errLower, "for reading: no such file or directory") {
		return &ErrorHint{
			Summary: "Module download failed (corrupted clone)",
			Details: "The module clone appears incomplete or corrupted.\n" +
				"This is a transient issue — retry the operation.\n\n" +
				"To fix this:\n" +
				"  1. Remove the corrupted module: rm -rf .terraform/modules/<module_name>\n" +
				"  2. Run: terraform init -upgrade\n\n" +
				"If this persists, check your network connection and try again.",
			IsUserConfig: false,
			IsRetryable:  true,
		}
	}

	// "invalid index-pack output" - corrupted download
	if strings.Contains(errLower, "invalid index-pack output") {
		return &ErrorHint{
			Summary: "Module download failed (corrupted download)",
			Details: "The git pack file appears corrupted.\n" +
				"This is a transient issue — retry the operation.\n\n" +
				"To fix this:\n" +
				"  1. Remove the module: rm -rf .terraform/modules/<module_name>\n" +
				"  2. Run: terraform init -upgrade\n\n" +
				"If this persists, it may be a network or proxy issue.",
			IsUserConfig: false,
			IsRetryable:  true,
		}
	}

	// Generic "error downloading" - other transient git errors
	if strings.Contains(errLower, "error downloading") ||
		strings.Contains(errLower, "/usr/bin/git exited with") {
		return &ErrorHint{
			Summary: "Module download failed (git error)",
			Details: "Git encountered an error while downloading the module.\n" +
				"This is often a transient issue — retry the operation.\n\n" +
				"To fix this:\n" +
				"  1. Wait a moment and retry\n" +
				"  2. If persistent, run: rm -rf .terraform/modules/<module_name>\n" +
				"  3. Run: terraform init -upgrade\n\n" +
				"Check the error details above for the specific git error.",
			IsUserConfig: false,
			IsRetryable:  true,
		}
	}

	return nil
}

// checkStalePlanError detects "Saved plan is stale" errors that happen when
// state changes during plan/apply.
func checkStalePlanError(errMsg string) *ErrorHint {
	if !strings.Contains(errMsg, "Saved plan is stale") {
		return nil
	}

	return &ErrorHint{
		Summary: "Plan is stale (state changed)",
		Details: "The plan file is no longer valid because the state was changed\n" +
			"by another operation (e.g., another terraform run or external modification).\n\n" +
			"This is a transient issue — re-run the operation:\n" +
			"  1. Run: terraform plan\n" +
			"  2. Then: terraform apply\n\n" +
			"If this happens frequently, ensure only one terraform process runs at a time.",
		IsUserConfig: false,
		IsRetryable:  true,
	}
}

// checkProviderInternalError detects provider internal errors that are actually
// user configuration issues (e.g., null values passed to the provider).
func checkProviderInternalError(errMsg string) *ErrorHint {
	// "This is always an error in the provider" is terraform's generic message
	// for internal errors, but it's often caused by user config (null values)
	if !strings.Contains(errMsg, "This is always an error in the provider") &&
		!strings.Contains(errMsg, "Please report the following to the provider") {
		return nil
	}

	// Check if it's a null value error (user config issue)
	if strings.Contains(errMsg, "Received null value") {
		return &ErrorHint{
			Summary: "Provider received null value (configuration issue)",
			Details: "The provider received a null value where it expected a concrete value.\n" +
				"This is NOT a provider bug — it's a configuration issue.\n\n" +
				"To fix this:\n" +
				"  1. Check that all required variables are set (especially sensitive ones)\n" +
				"  2. Look for variables that might be null or empty\n" +
				"  3. The error message above shows which path received the null value\n\n" +
				"Common causes:\n" +
				"  - Missing sensitive variables (API keys, passwords, certificates)\n" +
				"  - Variables referencing data sources that returned null\n" +
				"  - Variables with conditional logic that evaluates to null",
			IsUserConfig: true,
		}
	}

	return nil
}

// checkModelError detects model not found errors that are user config issues.
func checkModelError(errMsg string) *ErrorHint {
	// "unknown model:" indicates the model UUID is wrong or doesn't exist
	if strings.Contains(errMsg, "unknown model:") ||
		strings.Contains(errMsg, "model not found") {
		return &ErrorHint{
			Summary: "Model not found (incorrect UUID)",
			Details: "The specified Juju model was not found.\n" +
				"This is NOT a provider issue — the model UUID is incorrect or the model doesn't exist.\n\n" +
				"To fix this:\n" +
				"  1. Verify the model exists: juju models\n" +
				"  2. Get the correct UUID: juju models --format yaml | grep uuid\n" +
				"  3. Update your wrapper with the correct model_uuid\n\n" +
				"Common causes:\n" +
				"  - Typo in the model UUID\n" +
				"  - Model was deleted or renamed\n" +
				"  - Wrong Juju controller or account",
			IsUserConfig: true,
		}
	}

	// "Unable to create application, got error: unknown model:" pattern
	if strings.Contains(errMsg, "Unable to") &&
		strings.Contains(errMsg, "got error:") &&
		strings.Contains(errMsg, "unknown model") {
		return &ErrorHint{
			Summary: "Model not found (incorrect UUID)",
			Details: "The operation failed because the Juju model was not found.\n" +
				"This is NOT a provider issue — the model UUID is incorrect.\n\n" +
				"To fix this:\n" +
				"  1. Verify the model exists: juju models\n" +
				"  2. Get the correct UUID: juju models --format yaml | grep uuid\n" +
				"  3. Update your wrapper with the correct model_uuid",
			IsUserConfig: true,
		}
	}

	return nil
}

// checkMissingRequiredArgument detects "Missing required argument" errors
// and provides guidance on which variables need to be set.
func checkMissingRequiredArgument(errMsg string) *ErrorHint {
	if !strings.Contains(errMsg, "Missing required argument") {
		return nil
	}

	// Collect every missing variable, not just the first: Terraform reports all
	// of them in one go, and a module with several required inputs is exactly
	// when this hint is most useful.
	varPattern := regexp.MustCompile(`Missing required argument.*?["'](\w+)["']`)
	all := varPattern.FindAllStringSubmatch(errMsg, -1)
	if len(all) == 0 {
		return &ErrorHint{
			Summary:      "A required variable is not set",
			Details:      "The module requires a variable that hasn't been provided.\nCheck the module's variables.tf to see which variables are required.",
			IsUserConfig: true,
		}
	}

	seen := map[string]bool{}
	var names []string
	for _, m := range all {
		if !seen[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	sort.Strings(names)

	// The remedy depends on *which block* is missing the argument, and the two
	// are not interchangeable:
	//
	//   - the wrapper's module {} block is missing a module input, fixed with
	//     --var, a --preset, or by editing the wrapper;
	//   - a list {} block in the generated query file is missing a required
	//     config attribute, fixed with --query-var.
	//
	// Both produce the identical "Missing required argument" text, so the
	// diagnostic's file reference is what tells them apart. Getting this wrong
	// sends the user to the flag they already passed.
	if strings.Contains(errMsg, DefaultQueryFile) {
		return &ErrorHint{
			Summary: "Query engine input not set: " + strings.Join(names, ", "),
			Details: "The provider's list resources require " + plural(len(names), "this argument", "these arguments") + "\n" +
				"to know what to enumerate. This is NOT a provider issue.\n\n" +
				"To fix this, supply " + plural(len(names), "it", "them") + " to the query engine and re-run:\n" +
				"  --query-var " + strings.Join(names, "=<value> --query-var ") + "=<value>",
			IsUserConfig: true,
		}
	}

	// A module input. Note that --query-var is deliberately *not* offered here:
	// it configures the query engine, not the module, so suggesting it when the
	// module block is what lacks a value is actively misleading.
	hint := &ErrorHint{
		Summary:      "Required variable not set: " + strings.Join(names, ", "),
		IsUserConfig: true,
	}
	fix := "To fix this, supply " + plural(len(names), "it", "them") + " and re-run:\n" +
		"  --var " + strings.Join(names, "=<value> --var ") + "=<value>\n" +
		"or set " + plural(len(names), "it", "them") + " in the wrapper, or apply a --preset that covers " +
		plural(len(names), "it", "them") + "."

	switch {
	case len(names) > 1:
		hint.Details = "The module declares " + plural(len(names), "this variable", "these variables") +
			" without a default, so Terraform\ncannot plan until every one has a value. This is a configuration issue,\n" +
			"not a provider bug.\n\n" + fix
	case strings.Contains(names[0], "s3"):
		hint.Details = "The module needs S3 configuration. This is NOT a provider issue.\n\n" + fix +
			"\n\nRelated variables are usually set together: s3_endpoint, s3_access_key, s3_secret_key."
	case strings.Contains(names[0], "model") && strings.Contains(names[0], "uuid"):
		hint.Details = "This module takes the model UUID as an input variable, so it has to be\n" +
			"set on the module itself. A query variable configures only the discovery\n" +
			"step and is not passed through to the module. Atelier seeds the module\n" +
			"input automatically when a query variable of the same name exists, so\n" +
			"seeing this means none was supplied either.\n\n" + fix +
			"\n\nFind your model UUID with: juju models --format yaml | grep uuid"
	default:
		hint.Details = "The module requires '" + names[0] + "' to be set.\n" +
			"This is a configuration issue, not a provider bug.\n\n" + fix
	}

	return hint
}

// plural picks between a singular and a plural phrasing.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// checkProviderConfigError detects provider configuration errors that are
// often misattributed to provider bugs.
func checkProviderConfigError(errMsg string) *ErrorHint {
	// "Provider configuration not present" is a common terraform error
	// that sounds like a provider bug but is actually a config issue
	if !strings.Contains(errMsg, "Provider configuration not present") &&
		!strings.Contains(errMsg, "provider configuration") {
		return nil
	}

	return &ErrorHint{
		Summary: "Provider configuration mismatch",
		Details: "The module requires a provider configuration that isn't available.\n" +
			"This is NOT a provider bug — it's a configuration issue.\n\n" +
			"To fix this:\n" +
			"  1. Ensure your wrapper has a providers.tf with the required provider\n" +
			"  2. Run 'terraform init' to install the provider\n" +
			"  3. Check that the provider version matches the module's requirements",
		IsUserConfig: true,
	}
}

// checkVariableValidationError detects variable validation errors and
// provides actionable guidance.
func checkVariableValidationError(errMsg string) *ErrorHint {
	if !strings.Contains(errMsg, "Invalid value for variable") {
		return nil
	}

	// Extract the validation message
	msgPattern := regexp.MustCompile(`(?s)\(source code not available\)\s*\n\s*(.*?)\s*\n\s*This was checked`)
	msgMatch := msgPattern.FindStringSubmatch(errMsg)

	details := "A variable validation rule failed.\n" +
		"This is a module requirement, not a provider issue.\n\n"

	if len(msgMatch) > 1 {
		details += "Validation message:\n  " + strings.TrimSpace(msgMatch[1]) + "\n\n"
	}

	details += "To fix this:\n" +
		"  1. Check the module's variables.tf for validation rules\n" +
		"  2. Ensure your variable values satisfy all constraints\n" +
		"  3. Use --var to provide valid values"

	return &ErrorHint{
		Summary:      "Variable validation failed",
		Details:      details,
		IsUserConfig: true,
	}
}

// checkConnectionError detects connection/authentication errors that users
// might mistake for provider issues.
func checkConnectionError(errMsg string) *ErrorHint {
	errLower := strings.ToLower(errMsg)

	// Connection refused errors
	if strings.Contains(errLower, "connection refused") ||
		strings.Contains(errLower, "dial tcp") ||
		strings.Contains(errLower, "no route to host") {
		return &ErrorHint{
			Summary: "Connection failed",
			Details: "Cannot connect to the target service.\n" +
				"This is NOT a provider issue — it's a network/connectivity problem.\n\n" +
				"To fix this:\n" +
				"  1. Check that the target service is running\n" +
				"  2. Verify network connectivity and firewall rules\n" +
				"  3. Check endpoint URLs and ports",
			IsUserConfig: true,
		}
	}

	// Authentication errors
	if strings.Contains(errLower, "unauthorized") ||
		strings.Contains(errLower, "authentication") ||
		strings.Contains(errLower, "permission denied") ||
		strings.Contains(errLower, "access denied") {
		return &ErrorHint{
			Summary: "Authentication failed",
			Details: "Authentication or authorization failed.\n" +
				"This is NOT a provider issue — check your credentials.\n\n" +
				"To fix this:\n" +
				"  1. Verify your credentials are correct\n" +
				"  2. Check that your account has the required permissions\n" +
				"  3. Ensure credentials haven't expired",
			IsUserConfig: true,
		}
	}

	return nil
}

// checkInvalidInputError detects invalid input errors and provides guidance.
func checkInvalidInputError(errMsg string) *ErrorHint {
	errLower := strings.ToLower(errMsg)

	// Invalid model UUID patterns
	if strings.Contains(errLower, "invalid") && strings.Contains(errLower, "uuid") {
		return &ErrorHint{
			Summary: "Invalid UUID format",
			Details: "The provided UUID is not in a valid format.\n" +
				"This is NOT a provider issue — check your input.\n\n" +
				"To fix this:\n" +
				"  1. Verify the UUID is in the correct format (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)\n" +
				"  2. Check for typos or copy-paste errors\n" +
				"  3. Get the correct UUID with: juju models --format yaml | grep uuid",
			IsUserConfig: true,
		}
	}

	// Invalid endpoint URLs
	if strings.Contains(errLower, "invalid") && strings.Contains(errLower, "url") ||
		strings.Contains(errLower, "invalid") && strings.Contains(errLower, "endpoint") {
		return &ErrorHint{
			Summary: "Invalid URL or endpoint",
			Details: "The provided URL or endpoint is not valid.\n" +
				"This is NOT a provider issue — check your configuration.\n\n" +
				"To fix this:\n" +
				"  1. Verify the URL format (include https:// if needed)\n" +
				"  2. Check for typos\n" +
				"  3. Ensure the endpoint is accessible",
			IsUserConfig: true,
		}
	}

	return nil
}

// FormatUserError wraps an error message with helpful context when the error
// matches a known pattern. If no pattern matches, returns the original message.
func FormatUserError(errMsg string) string {
	if hint := ClassifyError(errMsg); hint != nil {
		var b strings.Builder
		b.WriteString(hint.Summary)
		b.WriteString("\n\n")
		b.WriteString(hint.Details)
		b.WriteString("\n\n")
		b.WriteString("Original error:\n")
		b.WriteString(errMsg)
		return b.String()
	}
	return errMsg
}
