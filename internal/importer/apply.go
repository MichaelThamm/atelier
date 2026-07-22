package importer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MichaelThamm/atelier/internal/tfexec"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// PlanResult holds the output of PlanCreates.
type PlanResult struct {
	// Creates are resources the plan would create (not in state).
	Creates []PlannedResource
	// AllModuleResources includes both creates and resources already in state.
	// Used for matching live objects to module addresses even when the state
	// is partially populated.
	AllModuleResources []PlannedResource
}

// PlanCreates runs `terraform plan` against the target module *before* any
// imports are performed, and returns both the import candidates (resources
// terraform would create) and the full set of module resources (including
// those already in state). The plan is run with the current state; resources
// present in config but absent from state show as creates, while resources
// already in state show as no-ops.
//
// When WrapperState is set, all variable values are already persisted in
// main.tf via State.Write(), so the temp tfvars is skipped. Otherwise, config
// values are written to a temporary .auto.tfvars file so the plan can resolve
// module variables that reference them (e.g. model_uuid).
func PlanCreates(ctx context.Context, opts Options) (*PlanResult, error) {
	tf, err := tfexec.New(opts.Dir, opts.BinPath)
	if err != nil {
		return nil, err
	}
	atelierDir := filepath.Join(opts.Dir, wrapper.AtelierDir)
	if err := os.MkdirAll(atelierDir, 0o755); err != nil {
		return nil, fmt.Errorf("prepare plan dir: %w", err)
	}
	planFile := filepath.Join(atelierDir, "import-scan.tfplan")
	defer os.Remove(planFile)

	tfvarsPath := filepath.Join(opts.Dir, "atelier-import.auto.tfvars")
	if opts.WrapperState == nil && len(opts.Config) > 0 {
		// No wrapper state — write a temporary .auto.tfvars file so terraform
		// plan can resolve module input variables (e.g. model_uuid).
		var sb strings.Builder
		for _, k := range sortedKeys(opts.Config) {
			fmt.Fprintf(&sb, "%s = %q\n", k, opts.Config[k])
		}
		if err := os.WriteFile(tfvarsPath, []byte(sb.String()), 0o644); err != nil {
			return nil, fmt.Errorf("write temp tfvars: %w", err)
		}
	}

	plan, _, err := tf.Plan(ctx, planFile, nil)
	if err != nil {
		// Check if this is a validation error from variable validation rules.
		// These occur when module variables have validation blocks that fail
		// with the current variable values (e.g. cross-field dependencies).
		if validationErr, ok := parseValidationError(err); ok {
			return nil, validationErr
		}
		return nil, err
	}
	return &PlanResult{
		Creates:           PlannedCreates(plan, false),
		AllModuleResources: PlannedCreates(plan, true),
	}, nil
}

// validationError represents a terraform variable validation error.
type validationError struct {
	Variable string
	Message  string
	RawError error
}

func (e *validationError) Error() string {
	return fmt.Sprintf("terraform plan variable validation failed:\n"+
		"  Variable: %s\n"+
		"  Error: %s\n\n"+
		"The module has validation rules that require specific variable combinations.\n"+
		"Add the missing variable(s) via --var flags:\n"+
		"  Example: --var %s=<value>\n\n"+
		"Or disable validation by removing the validation block from the module's variables.tf.",
		e.Variable, e.Message, e.Variable)
}

func (e *validationError) Unwrap() error {
	return e.RawError
}

// parseValidationError detects terraform variable validation errors and
// extracts the variable name and error message. Returns nil if the error
// is not a validation error.
func parseValidationError(err error) (*validationError, bool) {
	if err == nil {
		return nil, false
	}

	errStr := err.Error()

	// Match the terraform validation error pattern:
	// Error: Invalid value for variable
	//   on  line 0:
	//   (source code not available)
	// <validation message>
	// This was checked by the validation rule at <file>:<line>.
	if strings.Contains(errStr, "Invalid value for variable") {
		// Extract the validation message (appears between "source code not available" and "This was checked")
		// Use (?s) flag to make . match newlines for multi-line messages
		msgPattern := regexp.MustCompile(`(?s)\(source code not available\)\s*\n\s*(.*?)\s*\n\s*This was checked`)
		msgMatch := msgPattern.FindStringSubmatch(errStr)

		var validationMsg string
		if len(msgMatch) > 1 {
			validationMsg = strings.TrimSpace(msgMatch[1])
		}

		if validationMsg != "" {
			return &validationError{
				Variable: "", // Variable name not in error message; user must identify from context
				Message:  validationMsg,
				RawError: err,
			}, true
		}
	}

	return nil, false
}
