package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
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
	// ApplyCmd returns a command that applies the most recent saved plan file
	// with the terminal handed back to terraform, so its own output and
	// approval prompt reach the user unmediated (ADR-0052).
	ApplyCmd() tea.Cmd
}

// Validator is the narrow interface the TUI needs to run `terraform validate`.
// Separated from Planner so the capability can be independently stubbed.
type Validator interface {
	// Validate runs `terraform validate -json` and returns diagnostics.
	Validate(ctx context.Context) (*tfjson.ValidateOutput, error)
}

// TfexecPlanner is the production Planner: it shells out via terraform-exec
// against a wrapper directory.
type TfexecPlanner struct {
	Tf         *tfexec.Terraform
	WrapperDir string

	init initGuard
}

// ResetInit clears the cached init state so the next EnsureInit call will
// run init -upgrade. Called after a ref switch rewrites the module source.
// An init already in flight is invalidated rather than cancelled: its result
// described the old module source, so it must not mark the wrapper ready.
func (p *TfexecPlanner) ResetInit() {
	if p != nil {
		p.init.reset()
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
	return ensureInitOnce(ctx, &p.init, p.runInit)
}

// runInit is the body EnsureInit serialises. Only the leader reaches it.
func (p *TfexecPlanner) runInit(ctx context.Context, upgrade bool) error {
	// Terraform's output goes to the durable log files rather than the
	// screen: the TUI renders the plan tree, not terraform's progress, and
	// the files are what a failed plan leaves behind to read (ADR-0052).
	command := "init"
	if upgrade {
		command = "init -upgrade"
	}
	action := p.Tf.BeginAction(command)
	defer action.Close()
	p.Tf.SetStdout(action.Stdout(nil))
	p.Tf.SetStderr(action.Stderr(nil))
	defer p.Tf.SetStdout(nil)
	defer p.Tf.SetStderr(nil)

	// After a ref switch we must run -upgrade to re-fetch the module even
	// though the base URL hasn't changed (only the ?ref= query did).
	if upgrade {
		if err := p.Tf.InitUpgrade(ctx); err != nil {
			return fmt.Errorf("terraform init -upgrade: %w", err)
		}
		return nil
	}
	// Always run `terraform init` on the first plan of a session. This is
	// idempotent and fast when nothing changed, but catches stale
	// .terraform/modules state from a previous session with a different ref.
	if err := p.Tf.Init(ctx); err != nil {
		return fmt.Errorf("terraform init: %w", err)
	}
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

	action := p.Tf.BeginAction("plan")
	defer action.Close()
	p.Tf.SetStdout(action.Stdout(nil))
	p.Tf.SetStderr(action.Stderr(nil))
	defer p.Tf.SetStdout(nil)
	defer p.Tf.SetStderr(nil)

	plan, _, err := p.Tf.Plan(ctx, planFilePath(p.WrapperDir), nil)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

// planFilePath is where a session's plan is cached between the P and A keys.
// It is relative to the wrapper, which is also the handoff's working
// directory, so terraform resolves it the same way the planner wrote it.
func planFilePath(wrapperDir string) string {
	return filepath.Join(wrapperDir, ".atelier", "cache", "plan.tfplan")
}

// ApplyCmd applies the cached plan file with the terminal handed back to
// terraform: tea.ExecProcess releases the alt-screen so terraform prints its
// own progress. The plan was already reviewed in the plan tree and pressing A
// is the confirmation, so the flags match what the plan-file path has always
// passed rather than prompting a second time.
func (p *TfexecPlanner) ApplyCmd() tea.Cmd {
	planFile := ""
	if p != nil {
		planFile = planFilePath(p.WrapperDir)
		if _, err := os.Stat(planFile); err != nil {
			return func() tea.Msg {
				return applyErrorMsg{err: fmt.Errorf("no saved plan file: %w", err)}
			}
		}
	}
	if p == nil || p.Tf == nil {
		return func() tea.Msg {
			return applyErrorMsg{err: errors.New("applier not configured")}
		}
	}

	cmd := applyCmd(p.Tf.ExecPath(), p.WrapperDir, planFile)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return applyErrorMsg{err: err}
		}
		return applyResultMsg{}
	})
}

// applyCmd builds the argv for the terminal handoff. It is separate from
// ApplyCmd so the flags can be asserted without running terraform.
//
// Flags precede the plan file: terraform accepts one positional argument and
// treats a flag after it as a second one ("Too many command line arguments"),
// so `apply <plan> -auto-approve` fails where `apply -auto-approve <plan>`
// succeeds.
func applyCmd(execPath, wrapperDir, planFile string) *exec.Cmd {
	cmd := exec.Command(execPath, "apply", "-auto-approve", "-input=false", planFile)
	cmd.Dir = wrapperDir
	return cmd
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
