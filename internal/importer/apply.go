package importer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelThamm/atelier/internal/tfexec"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// PlanCreates runs `terraform plan` against the target module *before* any
// imports are performed, and returns the resources it would create — the
// set of import candidates whose module addresses the matcher pairs to live
// objects. The plan is run with empty (or partial) state; resources present in
// config but absent from state show as creates.
//
// Config values from opts.Config are written to a temporary .auto.tfvars file
// so the plan can resolve module variables that reference them (e.g. model_uuid).
func PlanCreates(ctx context.Context, opts Options) ([]PlannedResource, error) {
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

	// Write a temporary .auto.tfvars file so terraform plan can resolve
	// module input variables (e.g. model_uuid).
	tfvarsPath := filepath.Join(opts.Dir, "atelier-import.auto.tfvars")
	if len(opts.Config) > 0 {
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
		return nil, err
	}
	return PlannedCreates(plan), nil
}
