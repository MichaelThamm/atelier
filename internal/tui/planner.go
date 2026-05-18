package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	uptfexec "github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"

	"github.com/MichaelThamm/atelier/internal/tfexec"
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

// Applier is the narrow interface the TUI needs to apply a saved plan.
// Separated from Planner so the capability can be independently stubbed or
// disabled.
type Applier interface {
	// Apply runs `terraform apply` using the most recent saved plan file.
	Apply(ctx context.Context) error
}

// Validator is the narrow interface the TUI needs to run `terraform validate`.
// Separated from Planner so the capability can be independently stubbed.
type Validator interface {
	// Validate runs `terraform validate -json` and returns diagnostics.
	Validate(ctx context.Context) (*tfjson.ValidateOutput, error)
}

// OutputProvider is the narrow interface the TUI needs to fetch terraform
// outputs after a successful apply or on demand.
type OutputProvider interface {
	// Output runs `terraform output -json` and returns the output map.
	Output(ctx context.Context) (map[string]uptfexec.OutputMeta, error)
}

// TfexecPlanner is the production Planner: it shells out via terraform-exec
// against a wrapper directory.
type TfexecPlanner struct {
	Tf         *tfexec.Terraform
	WrapperDir string

	initialised  bool
	needsUpgrade bool // set by ResetInit after a ref switch
}

// ResetInit clears the cached init state so the next EnsureInit call will
// run init -upgrade. Called after a ref switch rewrites the module source.
func (p *TfexecPlanner) ResetInit() {
	if p != nil {
		p.initialised = false
		p.needsUpgrade = true
	}
}

// EnsureInit runs `terraform init` if modules have not been installed in
// the wrapper. The check looks for .terraform/modules/modules.json which
// Terraform writes when module sources are fetched. This catches the case
// where .terraform/ exists (from provider init) but the module block's
// source was added or changed since the last init.
func (p *TfexecPlanner) EnsureInit(ctx context.Context) error {
	if p == nil || p.Tf == nil {
		return errors.New("planner not configured")
	}
	if p.initialised {
		return nil
	}
	// After a ref switch we must run -upgrade to re-fetch the module even
	// though the base URL hasn't changed (only the ?ref= query did).
	if p.needsUpgrade {
		if err := p.Tf.InitUpgrade(ctx); err != nil {
			return fmt.Errorf("terraform init -upgrade: %w", err)
		}
		p.needsUpgrade = false
		p.initialised = true
		return nil
	}
	// Always run `terraform init` on the first plan of a session. This is
	// idempotent and fast when nothing changed, but catches stale
	// .terraform/modules state from a previous session with a different ref.
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

// Apply applies the most recent saved plan file from the cache directory.
func (p *TfexecPlanner) Apply(ctx context.Context) error {
	if p == nil || p.Tf == nil {
		return errors.New("applier not configured")
	}
	planFile := filepath.Join(p.WrapperDir, ".atelier", "cache", "plan.tfplan")
	if _, err := os.Stat(planFile); err != nil {
		return fmt.Errorf("no saved plan file: %w", err)
	}
	return p.Tf.Apply(ctx, planFile)
}

// Validate runs `terraform validate -json` against the wrapper directory.
func (p *TfexecPlanner) Validate(ctx context.Context) (*tfjson.ValidateOutput, error) {
	if p == nil || p.Tf == nil {
		return nil, errors.New("validator not configured")
	}
	if err := p.EnsureInit(ctx); err != nil {
		return nil, fmt.Errorf("init before validate: %w", err)
	}
	return p.Tf.Validate(ctx)
}

// Output runs `terraform output -json` against the wrapper directory.
func (p *TfexecPlanner) Output(ctx context.Context) (map[string]uptfexec.OutputMeta, error) {
	if p == nil || p.Tf == nil {
		return nil, errors.New("output provider not configured")
	}
	return p.Tf.Output(ctx)
}
