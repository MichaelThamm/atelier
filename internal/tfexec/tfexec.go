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
	"sort"
	"strings"
	"time"

	hcversion "github.com/hashicorp/go-version"
	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
)

// MinVersion is the lowest terraform version Atelier supports (SPEC §5.2 ¶5).
const MinVersion = "1.5.0"

// QueryMinVersion is the lowest terraform version that supports `terraform
// query` (the list-resource / bulk-import mechanism `atelier import` builds
// on). This is gated per-command rather than raising MinVersion, so every
// other Atelier command keeps working on terraform >= MinVersion.
// See ADR-0027.
const QueryMinVersion = "1.14.0"

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
	// StderrLogName is the always-on persistent copy of terraform's stderr,
	// written under LogDir.
	StderrLogName = "tf-stderr.log"
	// StdoutLogName is the always-on persistent copy of terraform's stdout
	// (plan/apply progress), written under LogDir.
	StdoutLogName = "tf-stdout.log"
	// TraceLogName is terraform's full TRACE log, written under LogDir only
	// when DebugEnvVar is truthy.
	TraceLogName = "tf-trace.log"
)

// LogDirPath returns the absolute path of a wrapper's persistent terraform
// diagnostics directory (<workdir>/.atelier/logs). The TUI surfaces it so a
// user can find the log files after a failure. Resolution is best-effort: if
// the path cannot be made absolute, the joined relative path is returned.
func LogDirPath(workdir string) string {
	dir := filepath.Join(workdir, LogDir)
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// WriteTimestampHeader appends a separator line with the current wall-clock
// time to a persistent log file, delimiting one action's output (init, plan,
// apply) from the next. It is a no-op for a nil handle (logging not
// configured). The files are opened O_APPEND, so the explicit seek to the end
// is only a fallback for a handle opened without it.
func WriteTimestampHeader(f *os.File) {
	if f == nil {
		return
	}
	_, _ = f.Seek(0, 2) // seek to end
	fmt.Fprintf(f, "\n=== action started at %s ===\n", time.Now().Format("2006-01-02 15:04:05"))
}

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
	tf         *tfexec.Terraform
	binPath    string   // resolved terraform/tofu binary, for the interactive path
	workdir    string   // wrapper directory terraform runs in
	stderrFile *os.File // log file handle for .atelier/logs/tf-stderr.log
	stdoutFile *os.File // log file handle for .atelier/logs/tf-stdout.log
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
	t := &Terraform{tf: tf, binPath: binPath, workdir: workdir}
	t.configureLogging(workdir)
	return t, nil
}

// configureLogging persists terraform's diagnostics under
// <workdir>/.atelier/logs/. It is best-effort: any failure to set up logging
// is swallowed, because diagnostics must never prevent terraform from running.
//
//   - Always on: terraform's stderr is teed to tf-stderr.log and its stdout to
//     tf-stdout.log (both appended). terraform-exec still captures them
//     internally for its error messages and progress, so this only adds a
//     durable copy — it changes nothing the caller sees. Successful commands
//     write little or nothing to stderr, so tf-stderr.log stays small and fills
//     mainly with the warnings and errors worth keeping; tf-stdout.log holds
//     the plan/apply progress the logs view shows.
//   - Opt-in (ATELIER_DEBUG truthy): terraform's own TRACE log is written to
//     tf-trace.log via TF_LOG_PATH. This is verbose, so it stays off by
//     default; leave it enabled and the next failure records the exact git
//     command the module installer ran and git's full output.
func (t *Terraform) configureLogging(workdir string) {
	logDir := filepath.Join(workdir, LogDir)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return
	}
	// Open (or create) the log files and store the handles. O_APPEND keeps
	// every session's output: init writes without seeking first, and the
	// planner's timestamp header seeks to the end, so without append a fresh
	// session would clobber the start of the previous one's log. The handles
	// are NOT set as stdout/stderr here — the planner writes a timestamp header
	// and sets them before each action so binary junk never precedes the header.
	if f, err := os.OpenFile(filepath.Join(logDir, StderrLogName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		t.stderrFile = f
	}
	if f, err := os.OpenFile(filepath.Join(logDir, StdoutLogName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		t.stdoutFile = f
	}
	if debugEnabled() {
		_ = t.tf.SetLogPath(filepath.Join(logDir, TraceLogName))
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

// ExecPath is the absolute path of the resolved terraform binary, for callers
// that exec it directly rather than through this wrapper.
func (t *Terraform) ExecPath() string { return t.tf.ExecPath() }

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

// SetStderr redirects terraform's stderr to the given writer.
// Pass nil to clear.
func (t *Terraform) SetStderr(w io.Writer) {
	t.tf.SetStderr(w)
}

// StderrFile returns the log file handle for .atelier/logs/tf-stderr.log,
// or nil if logging is not configured. Used by callers that need to tee
// stderr to both the file and a progress tracker.
func (t *Terraform) StderrFile() *os.File {
	return t.stderrFile
}

// StdoutFile returns the log file handle for .atelier/logs/tf-stdout.log,
// or nil if logging is not configured. Used by callers that need to tee
// stdout to both the file and a progress tracker.
func (t *Terraform) StdoutFile() *os.File {
	return t.stdoutFile
}

// MirrorStdout returns w teed with the wrapper's durable tf-stdout.log, or w
// unchanged when logging is not configured. The CLI `apply` streams terraform
// straight to the terminal, so without this a failed CLI apply leaves no log to
// read — the opposite of the TUI, which streams through the log file itself.
func (t *Terraform) MirrorStdout(w io.Writer) io.Writer { return mirrorLog(w, t.stdoutFile) }

// MirrorStderr is MirrorStdout for stderr and tf-stderr.log.
func (t *Terraform) MirrorStderr(w io.Writer) io.Writer { return mirrorLog(w, t.stderrFile) }

// mirrorLog writes to the durable log first, so a broken terminal (a closed
// pipe, say) cannot swallow the copy that outlives the run.
func mirrorLog(w io.Writer, log *os.File) io.Writer {
	if log == nil {
		return w
	}
	return io.MultiWriter(log, w)
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

// applyInterruptGrace is how long an interrupted apply waits for Terraform to
// cancel and persist state before it is force-killed. A second Ctrl-C reaches
// Terraform directly and forces it sooner, so this can be generous.
const applyInterruptGrace = 30 * time.Second

// applyDirectCmd builds the terminal-owning `terraform apply`, split from
// ApplyDirect so the interrupt contract is testable.
//
// Cancel is cleared because Terraform shares Atelier's foreground process group:
// the terminal's Ctrl-C already reaches it, and Go's default Cancel would kill
// it before its own SIGINT handler could persist state. WaitDelay is the
// backstop that still kills a Terraform that ignores SIGINT.
func applyDirectCmd(ctx context.Context, execPath, workdir string, autoApprove bool) *exec.Cmd {
	args := []string{"apply"}
	if autoApprove {
		args = append(args, "-auto-approve", "-input=false")
	}
	cmd := exec.CommandContext(ctx, execPath, args...)
	cmd.Dir = workdir
	cmd.Cancel = nil
	cmd.WaitDelay = applyInterruptGrace
	return cmd
}

// ApplyDirect runs `terraform apply` in the wrapper with the process's stdin
// attached, so Terraform prints the plan and reads the approval answer from
// stdin. It deliberately passes neither -auto-approve nor -input=false when
// interactive: the user reviews and confirms the plan, which is the point of
// `atelier apply` (ADR-0034).
//
// When autoApprove is true it instead passes -auto-approve -input=false and
// detaches stdin, for the non-interactive case (a pipe or `< /dev/null`) where
// there is no one to answer the prompt. The choice is the caller's, based on
// whether stdin is a terminal.
//
// stdout and stderr are mirrored into .atelier/logs/, so a CLI apply leaves the
// same durable evidence a TUI plan/apply does. Mirroring routes them through a
// pipe, so terraform sees no terminal on stdout and drops its color; the
// approval prompt is unaffected (it is read from stdin, which stays attached).
//
// An interrupted apply lets Terraform cancel itself rather than killing it; see
// applyDirectCmd.
func (t *Terraform) ApplyDirect(ctx context.Context, autoApprove bool) error {
	cmd := applyDirectCmd(ctx, t.binPath, t.workdir, autoApprove)
	WriteTimestampHeader(t.stdoutFile)
	WriteTimestampHeader(t.stderrFile)
	cmd.Stdout = t.MirrorStdout(os.Stdout)
	cmd.Stderr = t.MirrorStderr(os.Stderr)
	if !autoApprove {
		cmd.Stdin = os.Stdin
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("terraform apply: %w", err)
	}
	return nil
}

// Import runs `terraform import <address> <id>`, bringing a single live
// resource into Terraform state at the given module address.
func (t *Terraform) Import(ctx context.Context, address, id string) error {
	return t.tf.Import(ctx, address, id)
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

// CheckQueryVersion ensures the binary is new enough for `terraform query`
// (>= QueryMinVersion). `atelier import` calls this before attempting a query
// so the user gets an actionable message instead of terraform-exec's generic
// compatibility error. See ADR-0027.
func (t *Terraform) CheckQueryVersion(ctx context.Context) (string, error) {
	v, _, err := t.tf.Version(ctx, true)
	if err != nil {
		return "", fmt.Errorf("query terraform version: %w", err)
	}
	mv, _ := hcversion.NewVersion(QueryMinVersion)
	if v.LessThan(mv) {
		return v.String(), fmt.Errorf("atelier import requires terraform (or tofu) >= %s for 'terraform query'; found %s", QueryMinVersion, v)
	}
	return v.String(), nil
}

// QueryDiagnostic is a single diagnostic emitted by `terraform query`, with
// enough location detail for callers to map it back to a specific list block.
type QueryDiagnostic struct {
	Severity string
	Summary  string
	Detail   string
	Filename string
	Line     int
}

// QueryError is returned by QueryList when the query fails. It
// carries the parsed error diagnostics so callers can, for example, identify
// and skip the specific list resource types that failed.
type QueryError struct {
	Diagnostics []QueryDiagnostic
	Err         error
}

func (e *QueryError) Error() string {
	if len(e.Diagnostics) == 0 {
		if e.Err != nil {
			return "terraform query: " + e.Err.Error()
		}
		return "terraform query failed"
	}
	parts := make([]string, len(e.Diagnostics))
	for i, d := range e.Diagnostics {
		parts[i] = d.String()
	}
	return "terraform query failed:\n" + strings.Join(parts, "\n\n")
}

func (e *QueryError) Unwrap() error { return e.Err }

// String renders a diagnostic as "summary: detail (at file:line)".
func (d QueryDiagnostic) String() string {
	msg := d.Summary
	if d.Detail != "" {
		msg += ": " + d.Detail
	}
	if d.Filename != "" {
		msg += fmt.Sprintf(" (at %s:%d)", d.Filename, d.Line)
	}
	return msg
}

// LiveResource is one live object discovered by `terraform query`, carrying
// the provider-declared resource identity (the generic key used to match it to
// a resource in the target module — see internal/importer). No provider is
// special-cased: everything here comes from the query's JSON stream.
type LiveResource struct {
	// ResourceType is the resource type, e.g. "juju_application".
	ResourceType string
	// Address is the flat address terraform assigned in the query result, e.g.
	// "list.juju_application.apps[0]". Informational only; import targets are
	// the module addresses matched separately.
	Address string
	// DisplayName is terraform's human label for the object, if any.
	DisplayName string
	// Identity is the resource identity object (schema-defined by the
	// provider). This is the generic match key against a plan's AfterIdentity.
	Identity map[string]any
	// IdentityVersion is the identity schema version reported by the provider.
	IdentityVersion int64
	// Attributes is the object's attribute values (query "resource_object"),
	// used as a fallback match key when identity is unavailable on the plan
	// side.
	Attributes map[string]any
}

// QueryList runs `terraform query -json` (no -generate-config-out) with the
// given `-var` assignments and harvests the live objects the directory's
// *.tfquery.hcl list blocks match. It deliberately does NOT generate config:
// `atelier import` imports into an existing module, so it needs only the live
// resources' identities, not fresh resource blocks (ADR-0027).
//
// It drains terraform's JSON log stream to completion (required, or the
// underlying process blocks). On failure it returns a *QueryError carrying the
// parsed error diagnostics so callers can attribute failures to a specific
// list block and skip it.
func (t *Terraform) QueryList(ctx context.Context, vars map[string]string) ([]LiveResource, error) {
	var opts []tfexec.QueryOption
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		opts = append(opts, tfexec.Var(k+"="+vars[k]))
	}

	seq, err := t.tf.QueryJSON(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("terraform query: %w", err)
	}
	// QueryJSON redirects stdout to an internal pipe; clear it afterwards so a
	// later command on this Terraform doesn't inherit the closed writer.
	defer t.tf.SetStdout(nil)

	var (
		found []LiveResource
		diags []QueryDiagnostic
	)
	for msg := range seq {
		if msg.Msg == nil {
			// Terminal message: carries the command's exit error, if any.
			if msg.Err != nil {
				return found, &QueryError{Diagnostics: diags, Err: msg.Err}
			}
			break
		}
		switch m := msg.Msg.(type) {
		case tfjson.ListResourceFoundMessage:
			d := m.ListResourceFound
			found = append(found, LiveResource{
				ResourceType:    d.ResourceType,
				Address:         d.Address,
				DisplayName:     d.DisplayName,
				Identity:        d.Identity,
				IdentityVersion: d.IdentityVersion,
				Attributes:      d.ResourceObject,
			})
		case tfjson.DiagnosticLogMessage:
			if m.Diagnostic.Severity == tfjson.DiagnosticSeverityError {
				diags = append(diags, toQueryDiagnostic(m.Diagnostic))
			}
		}
	}
	return found, nil
}

func toQueryDiagnostic(d tfjson.Diagnostic) QueryDiagnostic {
	qd := QueryDiagnostic{
		Severity: string(d.Severity),
		Summary:  d.Summary,
		Detail:   d.Detail,
	}
	if d.Range != nil {
		qd.Filename = d.Range.Filename
		qd.Line = d.Range.Start.Line
	}
	return qd
}
