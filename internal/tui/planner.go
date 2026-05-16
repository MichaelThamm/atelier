package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/canonical/atelier/internal/tfexec"
)

// Planner is the narrow interface the TUI needs from a terraform executor.
// Defined here (rather than depending on tfexec.Terraform directly) so tests
// can substitute a stub.
type Planner interface {
	// EnsureInit runs `terraform init` once if needed. Subsequent calls are
	// fast no-ops.
	EnsureInit(ctx context.Context) error
	// Plan runs `terraform plan -out=<tmp>; terraform show -json <tmp>` and
	// returns the parsed JSON plan.
	Plan(ctx context.Context) (*tfjson.Plan, error)
}

// TfexecPlanner is the production Planner: it shells out via terraform-exec
// against a wrapper directory.
type TfexecPlanner struct {
	Tf         *tfexec.Terraform
	WrapperDir string

	initialised bool
}

// EnsureInit runs `terraform init` if `.terraform/` does not yet exist in
// the wrapper. The check is filesystem-based — cheap and matches Terraform's
// own first-run convention. We deliberately do not pass -upgrade; the user
// gets to manage provider upgrades themselves.
func (p *TfexecPlanner) EnsureInit(ctx context.Context) error {
	if p == nil || p.Tf == nil {
		return errors.New("planner not configured")
	}
	if p.initialised {
		return nil
	}
	dotTerraform := filepath.Join(p.WrapperDir, ".terraform")
	if _, err := os.Stat(dotTerraform); err == nil {
		p.initialised = true
		return nil
	}
	if err := p.Tf.Init(ctx); err != nil {
		return fmt.Errorf("terraform init: %w", err)
	}
	p.initialised = true
	return nil
}

// Plan runs the plan and parses the resulting plan file. The temporary plan
// file lives under .atelier/cache/ so it doesn't pollute the wrapper root
// and is regenerable.
func (p *TfexecPlanner) Plan(ctx context.Context) (*tfjson.Plan, error) {
	if p == nil || p.Tf == nil {
		return nil, errors.New("planner not configured")
	}
	cacheDir := filepath.Join(p.WrapperDir, ".atelier", "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	planFile := filepath.Join(cacheDir, "plan.tfplan")
	plan, _, err := p.Tf.Plan(ctx, planFile)
	if err != nil {
		return nil, err
	}
	return plan, nil
}
