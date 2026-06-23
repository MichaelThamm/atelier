// Package tfexec wraps hashicorp/terraform-exec to give Atelier a narrow,
// testable surface over the Terraform binary: locate it, query its version,
// run init / validate / providers schema / plan / apply, and parse the JSON
// output.
package tfexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	hcversion "github.com/hashicorp/go-version"
	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
)

// MinVersion is the lowest terraform version Atelier supports (SPEC §5.2 ¶5).
const MinVersion = "1.5.0"

// DebugEnvVar, when set to a truthy value, switches on terraform's own
// TRACE-level logging (TF_LOG/TF_LOG_PATH) for every command Atelier runs.
// That trace records the exact git subprocess commands terraform's module
// installer shells out to during `init` and their full output — the detail
// needed to diagnose intermittent module-fetch failures such as git's
// "unknown error occurred while reading the configuration files".
const DebugEnvVar = "ATELIER_DEBUG"

// LogDir is the subdirectory of a wrapper where Atelier persists terraform
// diagnostics, alongside the existing .atelier/cache/.
const LogDir = ".atelier/logs"

const (
	stderrLogName = "tf-stderr.log"
	traceLogName  = "tf-trace.log"
)

// Locate returns the path to the terraform (or tofu) binary on $PATH, or an
// actionable error message if it isn't installed.
func Locate() (string, error) {
	for _, name := range []string{"terraform", "tofu"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("could not find terraform or tofu on $PATH; install Terraform >= " + MinVersion)
}

// Terraform wraps a *tfexec.Terraform and re-exports the small set of
// operations Atelier needs. Callers should treat the returned type as
// opaque: it exists primarily so tests can substitute a stub via the
// Operations interface.
type Terraform struct {
	tf *tfexec.Terraform
}

// New returns a Terraform pinned to the wrapper directory `workdir`. If
// `binPath` is empty, Locate is used to find a terraform/tofu binary.
//
// New also wires terraform's diagnostics to persistent log files under
// <workdir>/.atelier/logs/ so an intermittent failure leaves a durable
// artifact to inspect after the fact (the TUI otherwise streams output to a
// progress widget and discards it). See configureLogging.
func New(workdir, binPath string) (*Terraform, error) {
	if binPath == "" {
		var err error
		binPath, err = Locate()
		if err != nil {
			return nil, err
		}
	}
	tf, err := tfexec.NewTerraform(workdir, binPath)
	if err != nil {
		return nil, fmt.Errorf("init terraform-exec: %w", err)
	}
	t := &Terraform{tf: tf}
	t.configureLogging(workdir)
	return t, nil
}

// configureLogging persists terraform's diagnostics under
// <workdir>/.atelier/logs/. It is best-effort: any failure to set up logging
// is swallowed, because diagnostics must never prevent terraform from running.
//
//   - Always on: terraform's stderr is teed to tf-stderr.log (appended).
//     terraform-exec still captures stderr internally for its error messages,
//     so this only adds a durable copy — it changes nothing the caller sees.
//     Successful commands write little or nothing to stderr, so the file stays
//     small and fills mainly with the warnings and errors worth keeping.
//   - Opt-in (ATELIER_DEBUG truthy): terraform's own TRACE log is written to
//     tf-trace.log via TF_LOG_PATH. This is verbose, so it stays off by
//     default; leave it enabled and the next failure records the exact git
//     command the module installer ran and git's full output.
func (t *Terraform) configureLogging(workdir string) {
	logDir := filepath.Join(workdir, LogDir)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return
	}
	if f, err := os.OpenFile(filepath.Join(logDir, stderrLogName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		// The *os.File is referenced by tf via SetStderr for the lifetime of
		// this Terraform, so it won't be closed out from under terraform; the
		// OS reclaims the handle when the (short-lived) process exits.
		t.tf.SetStderr(f)
	}
	if debugEnabled() {
		// SetLogPath alone defaults TF_LOG to TRACE (see terraform-exec docs),
		// which avoids the `terraform version` probe SetLog would trigger.
		_ = t.tf.SetLogPath(filepath.Join(logDir, traceLogName))
	}
}

// debugEnabled reports whether ATELIER_DEBUG requests verbose terraform
// logging. Any value other than empty/0/false/no/off (case-insensitive) is
// treated as enabled.
func debugEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(DebugEnvVar))) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// Inner exposes the underlying tfexec.Terraform for callers that need
// advanced configuration (e.g. logging).
func (t *Terraform) Inner() *tfexec.Terraform { return t.tf }

// Version returns the resolved terraform version string.
func (t *Terraform) Version(ctx context.Context) (string, error) {
	v, _, err := t.tf.Version(ctx, true)
	if err != nil {
		return "", fmt.Errorf("query terraform version: %w", err)
	}
	return v.String(), nil
}

// CheckVersion ensures the binary is at least MinVersion.
func (t *Terraform) CheckVersion(ctx context.Context) (string, error) {
	v, _, err := t.tf.Version(ctx, true)
	if err != nil {
		return "", fmt.Errorf("query terraform version: %w", err)
	}
	mv, _ := hcversion.NewVersion(MinVersion)
	if v.LessThan(mv) {
		return v.String(), fmt.Errorf("terraform version %s is older than the required %s", v, MinVersion)
	}
	return v.String(), nil
}

// Init runs `terraform init`.
func (t *Terraform) Init(ctx context.Context) error {
	return t.tf.Init(ctx, tfexec.Upgrade(false))
}

// InitUpgrade runs `terraform init -upgrade` to update module sources and
// providers to the latest allowed versions. Used after a ref switch so
// Terraform fetches the new module revision.
func (t *Terraform) InitUpgrade(ctx context.Context) error {
	return t.tf.Init(ctx, tfexec.Upgrade(true))
}

// SetStdout sets a writer for streaming terraform's human-readable stdout.
// Pass nil to clear.
func (t *Terraform) SetStdout(w io.Writer) {
	t.tf.SetStdout(w)
}

// Validate runs `terraform validate -json`.
func (t *Terraform) Validate(ctx context.Context) (*tfjson.ValidateOutput, error) {
	return t.tf.Validate(ctx)
}

// ProvidersSchema runs `terraform providers schema -json`.
func (t *Terraform) ProvidersSchema(ctx context.Context) (*tfjson.ProviderSchemas, error) {
	return t.tf.ProvidersSchema(ctx)
}

// Plan runs `terraform plan -out=<tmp>` and then `terraform show -json
// <tmp>`. Returns the parsed plan plus a `hasChanges` boolean and the raw
// human-readable output captured during the plan. If stdout is non-nil,
// terraform's human-readable progress output is streamed to it during the
// plan phase (but not during show -json).
func (t *Terraform) Plan(ctx context.Context, planFile string, stdout io.Writer) (*tfjson.Plan, bool, error) {
	if stdout != nil {
		t.tf.SetStdout(stdout)
	}
	hasChanges, err := t.tf.Plan(ctx, tfexec.Out(planFile))
	// Clear stdout before ShowPlanFile so JSON doesn't go to the progress writer.
	t.tf.SetStdout(nil)
	if err != nil {
		return nil, false, fmt.Errorf("terraform plan: %w", err)
	}
	plan, err := t.tf.ShowPlanFile(ctx, planFile)
	if err != nil {
		return nil, hasChanges, fmt.Errorf("terraform show -json: %w", err)
	}
	return plan, hasChanges, nil
}

// Apply runs `terraform apply <planFile>` using a previously saved plan.
// If stdout is non-nil, terraform's progress output is streamed to it.
func (t *Terraform) Apply(ctx context.Context, planFile string, stdout io.Writer) error {
	if stdout != nil {
		t.tf.SetStdout(stdout)
		defer t.tf.SetStdout(nil)
	}
	return t.tf.Apply(ctx, tfexec.DirOrPlan(planFile))
}

// Output runs `terraform output -json` and returns the parsed output map.
func (t *Terraform) Output(ctx context.Context) (map[string]tfexec.OutputMeta, error) {
	return t.tf.Output(ctx)
}

// Show runs `terraform show -json` on the current state and returns the
// parsed state structure. Used to discover resource addresses for state
// migration.
func (t *Terraform) Show(ctx context.Context) (*tfjson.State, error) {
	return t.tf.Show(ctx)
}

// StateMv runs `terraform state mv <src> <dst>` to move a resource address
// in the state file. Used during convert to reparent resources under a module
// namespace.
func (t *Terraform) StateMv(ctx context.Context, src, dst string) error {
	return t.tf.StateMv(ctx, src, dst)
}

// SetEnv configures additional environment variables on the underlying
// tfexec runner.
func (t *Terraform) SetEnv(env map[string]string) error {
	return t.tf.SetEnv(env)
}
