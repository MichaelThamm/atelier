package importer

import (
	"testing"
)

func TestClassifyError_MissingRequiredArgument(t *testing.T) {
	cases := []struct {
		name     string
		errMsg   string
		wantHint bool
		wantSummary string
	}{
		{
			name:     "missing s3_endpoint",
			errMsg:   `Error: Missing required argument; The argument "s3_endpoint" is required, but no definition was found.`,
			wantHint: true,
			wantSummary: "Required variable not set: s3_endpoint",
		},
		{
			name:     "missing model_uuid",
			errMsg:   `Error: Missing required argument; The argument "model_uuid" is required, but no definition was found.`,
			wantHint: true,
			wantSummary: "Required variable not set: model_uuid",
		},
		{
			name:     "missing generic variable",
			errMsg:   `Error: Missing required argument; The argument "my_var" is required, but no definition was found.`,
			wantHint: true,
			wantSummary: "Required variable not set: my_var",
		},
		{
			name:     "missing without variable name",
			errMsg:   `Error: Missing required argument; A required argument is missing.`,
			wantHint: true,
			wantSummary: "A required variable is not set",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := ClassifyError(tc.errMsg)
			if tc.wantHint && hint == nil {
				t.Errorf("ClassifyError() returned nil, want hint")
				return
			}
			if !tc.wantHint && hint != nil {
				t.Errorf("ClassifyError() returned hint, want nil")
				return
			}
			if hint != nil && hint.Summary != tc.wantSummary {
				t.Errorf("Summary = %q, want %q", hint.Summary, tc.wantSummary)
			}
		})
	}
}

func TestClassifyError_ProviderConfigError(t *testing.T) {
	cases := []struct {
		name     string
		errMsg   string
		wantHint bool
	}{
		{
			name:     "provider configuration not present",
			errMsg:   `Error: Provider configuration not present; To work with module.cos.juju_application.alertmanager its original provider configuration is required.`,
			wantHint: true,
		},
		{
			name:     "provider configuration",
			errMsg:   `Error: provider configuration missing`,
			wantHint: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := ClassifyError(tc.errMsg)
			if tc.wantHint && hint == nil {
				t.Errorf("ClassifyError() returned nil, want hint")
			}
			if !tc.wantHint && hint != nil {
				t.Errorf("ClassifyError() returned hint, want nil")
			}
		})
	}
}

func TestClassifyError_VariableValidationError(t *testing.T) {
	errMsg := `Error: Invalid value for variable

  on  line 0:
  (source code not available)

postgresql_offer_url must be supplied when Grafana is scaled > 1 due to its
database requirements.

This was checked by the validation rule at
.terraform/modules/cos/terraform/cos/variables.tf:103,3-13.`

	hint := ClassifyError(errMsg)
	if hint == nil {
		t.Error("ClassifyError() returned nil, want hint")
		return
	}
	if hint.Summary != "Variable validation failed" {
		t.Errorf("Summary = %q, want %q", hint.Summary, "Variable validation failed")
	}
	if !hint.IsUserConfig {
		t.Error("IsUserConfig = false, want true")
	}
}

func TestClassifyError_ConnectionError(t *testing.T) {
	cases := []struct {
		name     string
		errMsg   string
		wantHint bool
	}{
		{
			name:     "connection refused",
			errMsg:   `Error: connection refused`,
			wantHint: true,
		},
		{
			name:     "dial tcp",
			errMsg:   `Error: dial tcp 127.0.0.1:8080: connect: connection refused`,
			wantHint: true,
		},
		{
			name:     "unauthorized",
			errMsg:   `Error: unauthorized`,
			wantHint: true,
		},
		{
			name:     "permission denied",
			errMsg:   `Error: permission denied`,
			wantHint: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := ClassifyError(tc.errMsg)
			if tc.wantHint && hint == nil {
				t.Errorf("ClassifyError() returned nil, want hint")
			}
			if !tc.wantHint && hint != nil {
				t.Errorf("ClassifyError() returned hint, want nil")
			}
		})
	}
}

func TestClassifyError_InvalidInputError(t *testing.T) {
	cases := []struct {
		name     string
		errMsg   string
		wantHint bool
	}{
		{
			name:     "invalid uuid",
			errMsg:   `Error: invalid UUID format`,
			wantHint: true,
		},
		{
			name:     "invalid url",
			errMsg:   `Error: invalid URL`,
			wantHint: true,
		},
		{
			name:     "invalid endpoint",
			errMsg:   `Error: invalid endpoint`,
			wantHint: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := ClassifyError(tc.errMsg)
			if tc.wantHint && hint == nil {
				t.Errorf("ClassifyError() returned nil, want hint")
			}
			if !tc.wantHint && hint != nil {
				t.Errorf("ClassifyError() returned hint, want nil")
			}
		})
	}
}

func TestClassifyError_TransientGitError(t *testing.T) {
	cases := []struct {
		name        string
		errMsg      string
		wantRetryable bool
	}{
		{
			name:        "could not lock config file",
			errMsg:      `Error: Failed to download module; Could not download module "loki_operators" (main.tf:1) source code from "git::https://github.com/canonical/loki-operators.git": error downloading 'https://github.com/canonical/loki-operators.git': /usr/bin/git exited with 128: Cloning into '.terraform/modules/loki_operators'...error: could not lock config file .terraform/modules/loki_operators/.git/config: No such file or directory`,
			wantRetryable: true,
		},
		{
			name:        "already exists and is not an empty directory",
			errMsg:      `Error: Failed to download module; Could not download module "loki_operators" (main.tf:1) source code from "git::https://github.com/canonical/loki-operators.git": error downloading 'https://github.com/canonical/loki-operators.git': /usr/bin/git exited with 128: fatal: destination path '.terraform/modules/loki_operators' already exists and is not an empty directory.`,
			wantRetryable: true,
		},
		{
			name:        "could not open for reading",
			errMsg:      `Error: Failed to download module; Could not download module "loki_operators" (main.tf:1) source code from "git::https://github.com/canonical/loki-operators.git": error downloading 'https://github.com/canonical/loki-operators.git': /usr/bin/git exited with 128: Cloning into '.terraform/modules/loki_operators'...fatal: could not open '.terraform/modules/loki_operators/.git/objects/pack/tmp_pack_SBUT7X' for reading: No such file or directory`,
			wantRetryable: true,
		},
		{
			name:        "invalid index-pack output",
			errMsg:      `Error: Failed to download module; Could not download module "loki_operators" (main.tf:1) source code from "git::https://github.com/canonical/loki-operators.git": error downloading 'https://github.com/canonical/loki-operators.git': /usr/bin/git exited with 128: fatal: fetch-pack: invalid index-pack output`,
			wantRetryable: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := ClassifyError(tc.errMsg)
			if hint == nil {
				t.Errorf("ClassifyError() returned nil, want hint")
				return
			}
			if !hint.IsRetryable {
				t.Errorf("IsRetryable = false, want true")
			}
			if hint.IsUserConfig {
				t.Errorf("IsUserConfig = true, want false")
			}
		})
	}
}

func TestClassifyError_StalePlanError(t *testing.T) {
	errMsg := `Error: Saved plan is stale; The given plan file can no longer be applied because the state was changed by another operation after the plan was created.`

	hint := ClassifyError(errMsg)
	if hint == nil {
		t.Error("ClassifyError() returned nil, want hint")
		return
	}
	if !hint.IsRetryable {
		t.Error("IsRetryable = false, want true")
	}
	if hint.IsUserConfig {
		t.Error("IsUserConfig = true, want false")
	}
}

func TestClassifyError_ProviderInternalError(t *testing.T) {
	errMsg := `Error: Value Conversion Error; An unexpected error was encountered trying to build a value. This is always an error in the provider. Please report the following to the provider developer: Received null value, however the target type cannot handle null values.`

	hint := ClassifyError(errMsg)
	if hint == nil {
		t.Error("ClassifyError() returned nil, want hint")
		return
	}
	if !hint.IsUserConfig {
		t.Error("IsUserConfig = false, want true")
	}
	if hint.IsRetryable {
		t.Error("IsRetryable = true, want false")
	}
}

func TestClassifyError_ModelNotFoundError(t *testing.T) {
	cases := []struct {
		name     string
		errMsg   string
		wantHint bool
	}{
		{
			name:     "unknown model with UUID",
			errMsg:   `Error: Client Error; Unable to add secret, got error: unknown model: "7dee0805-c6b8-4d09-815d-beea09b6684c" (model not found)`,
			wantHint: true,
		},
		{
			name:     "model not found",
			errMsg:   `Error: Client Error; Unable to create application, got error: unknown model: "test-model" (model not found)`,
			wantHint: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := ClassifyError(tc.errMsg)
			if tc.wantHint && hint == nil {
				t.Errorf("ClassifyError() returned nil, want hint")
			}
			if !tc.wantHint && hint != nil {
				t.Errorf("ClassifyError() returned hint, want nil")
			}
			if hint != nil && !hint.IsUserConfig {
				t.Errorf("IsUserConfig = false, want true")
			}
		})
	}
}

func TestClassifyError_NoMatch(t *testing.T) {
	cases := []struct {
		name   string
		errMsg string
	}{
		{
			name:   "empty message",
			errMsg: "",
		},
		{
			name:   "unrelated error",
			errMsg: "Error: something completely different",
		},
		{
			name:   "terraform plan error",
			errMsg: "Error: terraform plan failed with exit status 1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := ClassifyError(tc.errMsg)
			if hint != nil {
				t.Errorf("ClassifyError() returned hint, want nil")
			}
		})
	}
}

func TestFormatUserError(t *testing.T) {
	// Test with a matching pattern
	errMsg := `Error: Missing required argument; The argument "s3_endpoint" is required, but no definition was found.`
	result := FormatUserError(errMsg)
	if result == errMsg {
		t.Error("FormatUserError() did not enhance the error message")
	}
	if !contains(result, "Required variable not set: s3_endpoint") {
		t.Error("FormatUserError() did not include the summary")
	}
	if !contains(result, "Original error:") {
		t.Error("FormatUserError() did not include the original error")
	}

	// Test with a non-matching pattern
	errMsg2 := "Error: something completely different"
	result2 := FormatUserError(errMsg2)
	if result2 != errMsg2 {
		t.Error("FormatUserError() modified a non-matching error")
	}
}

func TestEnhanceError(t *testing.T) {
	// Test with a matching pattern
	errMsg := `Error: Missing required argument; The argument "model_uuid" is required, but no definition was found.`
	err := enhanceError("terraform import: ", &testError{msg: errMsg})
	if err == nil {
		t.Fatal("enhanceError() returned nil")
	}

	result := err.Error()
	if !contains(result, "terraform import: ") {
		t.Error("enhanceError() did not include the prefix")
	}
	if !contains(result, "Required variable not set: model_uuid") {
		t.Error("enhanceError() did not include the summary")
	}
	if !contains(result, "Original error:") {
		t.Error("enhanceError() did not include the original error")
	}

	// Test Unwrap
	if err.(interface{ Unwrap() error }).Unwrap() == nil {
		t.Error("enhanceError() did not preserve the original error")
	}

	// Test with a non-matching pattern
	errMsg2 := "Error: something completely different"
	err2 := enhanceError("terraform import: ", &testError{msg: errMsg2})
	if err2 == nil {
		t.Fatal("enhanceError() returned nil")
	}
	result2 := err2.Error()
	if !contains(result2, "terraform import: ") {
		t.Error("enhanceError() did not include the prefix")
	}
	if !contains(result2, errMsg2) {
		t.Error("enhanceError() did not include the original error message")
	}
}

// testError is a simple error type for testing.
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
