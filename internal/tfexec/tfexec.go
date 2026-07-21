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
