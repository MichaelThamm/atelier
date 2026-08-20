package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	tfjson "github.com/hashicorp/terraform-json"

	"github.com/MichaelThamm/atelier/internal/tfexec"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// DefaultQueryFile is the name of the generated query file. The `.tfquery.hcl`
// extension is required by terraform.
const DefaultQueryFile = "atelier-import.tfquery.hcl"

// LocateTerraform returns the path to the terraform/tofu binary, or an error
// if it is not installed. Exposed so cmd/atelier can check before bootstrap.
func LocateTerraform() (string, error) {
	return tfexec.Locate()
}

// PreflightContext carries the inputs a PreflightStep needs. It is handed to
// each step *after* the live objects have been enumerated but *before* the
// target module is planned, which is the only window in which a step can still
// influence the plan.
type PreflightContext struct {
	// Dir is the Terraform root directory.
	Dir string
	// Live lists every object `terraform query` discovered.
	Live []tfexec.LiveResource
	// WrapperState is the parsed wrapper state (nil when --source was not
	// used). A step may mutate it and call Write() to make derived values
	// visible to the plan that follows.
	WrapperState *wrapper.State
	// Config is the module-input config map (from --var and presets). Steps
	// may add keys derived from live data; the additions are visible to
	// BuildImportID later in the run. Never nil.
	Config map[string]string
	// QueryConfig holds the query-engine-only values (from --query-var).
	// Read-only: these are not module inputs.
	QueryConfig map[string]string
}

// PreflightStep is a provider-specific step that runs after the live query but
// before `terraform plan`. This is where values that are knowable only from the
// live deployment (e.g. a Juju model UUID) get folded into the wrapper, so the
// plan produces the right resource addresses and the run can build import IDs
// without the user having to supply them by hand.
type PreflightStep interface {
	Name() string
	Run(ctx context.Context, pctx PreflightContext) error
}

// PlanCheckContext carries the inputs a PlanCheck needs.
type PlanCheckContext struct {
	// Dir is the Terraform root directory.
	Dir string
	// Plan is the module's plan, computed with the current configuration.
	Plan *tfjson.Plan
	// Planned is every module resource the plan describes.
	Planned []PlannedResource
	// Live lists every object the query discovered.
	Live []tfexec.LiveResource
	// Config and QueryConfig are the requested variable values.
	Config      map[string]string
	QueryConfig map[string]string
	// WrapperState is the parsed wrapper state, or nil when --source was not
	// used. A check must not require it: without --source the user's root is an
	// arbitrary Terraform configuration that Atelier has no parsed model of.
	WrapperState *wrapper.State
}

// PlanCheck runs after the module is planned and before anything is imported.
// It is where a run is refused because its plan contradicts the live deployment.
//
// This is a distinct phase because neither neighbour can do the job: a preflight
// step runs before the plan exists, and a post-import step runs after the damage
// would already be recorded in state. Reading the answer out of the plan also
// means a check works whether or not Atelier authored the configuration, which a
// check reading the wrapper cannot.
type PlanCheck interface {
	Name() string
	Check(ctx context.Context, pctx PlanCheckContext) error
}

// PostImportContext carries the inputs a PostImportStep needs.
type PostImportContext struct {
	// Dir is the Terraform root directory.
	Dir string
	// Imported lists every resource successfully imported in this run.
	Imported []ImportResult
	// WrapperState is the parsed wrapper state (nil when --source was not used).
	WrapperState *wrapper.State
	// Plan lazily plans the module as it stands after import. Steps that need
	// to know what the configuration actually wants call this instead of
	// inferring it; the plan is computed at most once per run, and not at all
	// if no step asks. Nil when unavailable.
	Plan func() (*tfjson.Plan, error)
}

// PostImportStep is a provider-specific normalization that runs after
// terraform import but before the user runs plan/apply. Each provider
// implements the steps its resources need; the pipeline calls them in order.
type PostImportStep interface {
	Name() string
	Run(ctx context.Context, pctx PostImportContext) error
}

// ImportIDFunc builds the provider-specific import ID for a matched resource.
// It receives the matched resource (type + name) and the user-supplied config
// (e.g. containing model_uuid for Juju). Returns empty if the required config
// is missing or the resource type is unsupported.
type ImportIDFunc func(m MatchedImport, config map[string]string) string

// Options configures an import run.
type Options struct {
	// Dir is the target Terraform root. If it already declares and is
	// initialised against a provider, import uses it as-is. Otherwise, set
	// Provider to have import scaffold a minimal root and initialise it.
	Dir string
	// Provider is a provider source address (e.g. "juju/juju") used to
	// scaffold provider configuration when Dir has none. Ignored when Dir
	// already declares a provider.
	Provider string
	// ProviderVersion is an optional version constraint for the scaffolded
	// provider (e.g. "~> 1.5"). Empty means "latest from upstream".
	ProviderVersion string
	// SkipInit disables the automatic `terraform init`. By default import
	// runs init so the caller does not have to.
	SkipInit bool
	// Strict makes any list-resource query error fatal. By default, a type
	// that errors (e.g. a facade unsupported on this model kind) is skipped
	// with a warning and the remaining types are still generated.
	Strict bool
	// Types restricts import to these list-resource types. Empty selects
	// every list resource the provider(s) declare.
	Types []string
	// Config holds shared config values threaded to every selected list block
	// that accepts them, e.g. {"model_uuid": "<uuid>"}. Keys become query
	// variables; values are passed via -var at query time.
	Config map[string]string
	// QueryConfig holds query-engine-specific config values that are only
	// used by `terraform query` and never written to main.tf. For example,
	// the Juju provider's list resources require `model_uuid` to know which
	// model to query, but this is not a module input variable.
	QueryConfig map[string]string
	// QueryFile overrides the generated query filename (DefaultQueryFile).
	QueryFile string
	// DryRun stops after writing the imports artifact and previewing the plan.
	// No `terraform import` is run and no post-import step executes, so state
	// is left untouched.
	DryRun bool
	// BinPath overrides terraform/tofu discovery (mainly for tests).
	BinPath string
	// PreflightSteps are provider-specific steps that run after the live
	// query but before `terraform plan`, so they can still influence the
	// plan (e.g. deriving a Juju model UUID from live data and injecting it
	// into the wrapper). The pipeline calls them in order.
	PreflightSteps []PreflightStep
	// PlanChecks are provider-specific safety checks that run after the module
	// is planned and before anything is imported. Any error aborts the run with
	// no state written.
	PlanChecks []PlanCheck
	// PostImportSteps are provider-specific normalization steps that run
	// after terraform import completes. The pipeline calls them in order;
	// each step prints its own progress message.
	PostImportSteps []PostImportStep
	// BuildImportID constructs the provider-specific import ID for a matched
	// resource. When nil, no import ID can be built, so every match is reported
	// in Result.UnresolvedIDs and nothing is imported. Each provider supplies
	// its own implementation.
	BuildImportID ImportIDFunc
	// WrapperState is the parsed wrapper state, carried from setupSourceModule
	// so post-import steps can access variable declarations without re-reading
	// from disk. Nil when --source was not used.
	WrapperState *wrapper.State
	// Verbose enables detailed match-debug output on stderr. When false
	// (the default) the importer prints only high-level progress messages.
	Verbose bool
}

// ImportResult records a single successful `terraform import` invocation.
type ImportResult struct {
	Address  string // module address imported into, e.g. module.cos.juju_application.alertmanager
	Resource string // short resource label, e.g. juju_application.alertmanager
}

// Result reports what an import run produced.
type Result struct {
	// Available is every list resource discovered from the provider schema.
	Available []ListResource
	// Selected is the subset actually queried.
	Selected []ListResource
	// Skipped lists resource types dropped because they errored during the
	// query (empty in strict mode, where any error is fatal).
	Skipped []string
	// IDs maps each matched module address to its provider-specific import ID
	// (e.g. "module.cos.juju_application.alertmanager" → "<uuid>:alertmanager").
	IDs map[string]string
	// Imported lists every resource successfully imported into state.
	Imported []ImportResult
	// UnmatchedPlanned are resources the module wants to create for which no
	// single live object could be identified (zero or ambiguous matches).
	UnmatchedPlanned []PlannedResource
	// UnmatchedLive are live objects that matched no planned resource (e.g.
	// implicit/default resources the module does not declare).
	UnmatchedLive []tfexec.LiveResource
	// MatchedCount is how many live objects were paired with a module address,
	// before filtering to those that still need importing. A non-zero
	// MatchedCount with an empty IDs map means the deployment is already fully
	// in state — a very different situation from having matched nothing.
	MatchedCount int
	// UnresolvedIDs lists resources that were matched to a live object but for
	// which no provider-specific import ID could be built, so they were not
	// imported. Reported explicitly: these would otherwise be absent from both
	// IDs and UnmatchedPlanned, making a matched-but-skipped resource
	// invisible without --verbose.
	UnresolvedIDs []MatchedImport
	// QueryFilePath is the written *.tfquery.hcl (absolute). Empty when the
	// file was removed after a clean run.
	QueryFilePath string
	// QueryFileRetainedReason explains why the query file was kept. Empty when
	// it was removed. Generated Terraform inputs are removed on success and
	// kept only when they would help the user retry.
	QueryFileRetainedReason string
	// ImportsFilePath is the written imports.tf artifact (absolute), or empty
	// when none was written.
	ImportsFilePath string
	// Preview summarises the plan computed with the imports artifact in place.
	// Only populated for a dry run.
	Preview *PlanSummary
	// TerraformVersion is the resolved binary version.
	TerraformVersion string
}

// Discover locates terraform, verifies it is new enough for `terraform query`,
// and returns the list resources available in the target directory's
// provider schema. It performs no writes — useful for populating a selection
// UI before committing to a query.
func Discover(ctx context.Context, opts Options) (*Result, error) {
	if opts.Dir == "" {
		return nil, fmt.Errorf("import: Dir is required")
	}
	tf, err := tfexec.New(opts.Dir, opts.BinPath)
	if err != nil {
		return nil, err
	}
	ver, err := tf.CheckQueryVersion(ctx)
	if err != nil {
		return nil, err
	}

	// Scaffold provider configuration if the directory has none, so an empty
	// target can be imported into without hand-writing a provider block first.
	if !HasProviderConfig(opts.Dir) {
		if opts.Provider == "" {
			return nil, fmt.Errorf("no provider configured in %s.\n"+
				"Name a provider to scaffold one (e.g. 'atelier import juju'),\n"+
				"or add a provider block yourself.", opts.Dir)
		}
		if err := scaffoldProviderRoot(opts.Dir, opts.Provider, opts.ProviderVersion); err != nil {
			return nil, fmt.Errorf("scaffold provider config: %w", err)
		}
	}

	// Ensure providers are installed so `providers schema -json` works, sparing
	// the caller a manual `terraform init`.
	if !opts.SkipInit {
		if err := tf.Init(ctx); err != nil {
			return nil, fmt.Errorf("terraform init: %w", err)
		}
	}

	schemas, err := tf.ProvidersSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("read provider schema (has 'terraform init' been run in %s?): %w", opts.Dir, err)
	}
	available := DiscoverListResources(schemas)
	if len(available) == 0 {
		if schemas == nil || len(schemas.Schemas) == 0 {
			return nil, fmt.Errorf("no providers found in %s.\n"+
				"Run 'atelier import' inside an initialised Terraform root — a directory that\n"+
				"configures a provider and where 'terraform init' has been run. (A workspace that\n"+
				"only contains sub-directories has no provider schema of its own.)", opts.Dir)
		}
		return nil, fmt.Errorf("the configured provider(s) declare no list resources, so there is\n"+
			"nothing to import via 'terraform query': %v\n"+
			"List resources require a provider version that supports 'terraform query' export.",
			providerNames(schemas))
	}
	return &Result{Available: available, TerraformVersion: ver}, nil
}

// providerNames returns the provider source addresses present in the schema,
// sorted, for diagnostics.
func providerNames(schemas *tfjson.ProviderSchemas) []string {
	if schemas == nil {
		return nil
	}
	names := make([]string, 0, len(schemas.Schemas))
	for k := range schemas.Schemas {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// Generate runs the full import flow: discover importable types, enumerate
// the live objects via `terraform query`, plan the target module to find the
// resources it wants to create, match each live object to a module address by
// resource type and name, and run `terraform import` for each match.
//
// The caller's module already declares all resources — Generate imports their
// live state without generating any config.
func Generate(ctx context.Context, opts Options) (_ *Result, rerr error) {
	res, err := Discover(ctx, opts)
	if err != nil {
		return nil, err
	}

	selected, missing := SelectByType(res.Available, opts.Types)
	if len(missing) > 0 {
		return nil, fmt.Errorf("no such list resource(s): %v\navailable: %v", missing, typeNames(res.Available))
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no list resources selected")
	}
	res.Selected = selected

	queryFile := opts.QueryFile
	if queryFile == "" {
		queryFile = DefaultQueryFile
	}
	res.QueryFilePath = filepath.Join(opts.Dir, queryFile)

	tf, err := tfexec.New(opts.Dir, opts.BinPath)
	if err != nil {
		return nil, err
	}

	// Enumerate live objects. Unless in strict mode, drop any list resource
	// type that errors (e.g. a facade unsupported on this model kind) and retry
	// with the rest, so one unsupported type doesn't abort the whole import.
	// Errors that can't be attributed to a specific list block (a bad var, a
	// connection failure) are always fatal.
	active := selected
	var live []tfexec.LiveResource
	// Merge Config and QueryConfig for query operations. Config holds module
	// inputs that may also be needed by list blocks (e.g. s3_endpoint).
	// QueryConfig holds query-engine-only values (e.g. model_uuid for Juju).
	queryVars := mergeMaps(opts.Config, opts.QueryConfig)
	// Keep the first, complete rendering. The retry loop below prunes the list
	// blocks that errored, so the file left on disk at the end no longer
	// contains them — and a pruned file cannot reproduce the failure it was
	// kept to explain. Retention always writes this version back.
	var fullRender []byte
	// queryFailure retains the reproducing query file and annotates the error
	// with the exact command to re-run by hand.
	queryFailure := func(err error) error {
		if len(fullRender) > 0 {
			_ = os.WriteFile(res.QueryFilePath, fullRender, 0o644)
		}
		// Route through the hint classifier. A query failure is very often a
		// missing module variable — `terraform query` loads the root module, so
		// it is the first command to notice — and hints.go explains that far
		// better than the raw provider diagnostics do.
		hint := ClassifyError(err.Error())
		wrapped := enhanceError("", err)
		if hint != nil && hint.IsUserConfig {
			// Re-running the query verbatim would fail identically, so pointing
			// at it as a reproduction step is noise. The file is still kept.
			return fmt.Errorf("%w\n\nQuery file kept: %s", wrapped, res.QueryFilePath)
		}
		return fmt.Errorf("%w\n\nQuery file kept for reproduction: %s\n  terraform query%s",
			wrapped, res.QueryFilePath, varArgs(queryVars))
	}
	for {
		existingVars := ExistingVars(opts.Dir)
		render := RenderQueryFile(active, queryVars, existingVars...)
		if fullRender == nil {
			fullRender = render
		}
		if err := os.WriteFile(res.QueryFilePath, render, 0o644); err != nil {
			return nil, queryFailure(fmt.Errorf("write query file: %w", err))
		}
		found, qErr := tf.QueryList(ctx, queryVars)
		if qErr == nil {
			live = found
			break
		}
		var qerr *tfexec.QueryError
		if opts.Strict || !errors.As(qErr, &qerr) {
			return nil, queryFailure(qErr)
		}
		failed := failedTypes(qerr, res.QueryFilePath)
		if len(failed) == 0 {
			return nil, queryFailure(qErr) // not attributable to a type; real failure
		}
		active, res.Skipped = dropTypes(active, failed, res.Skipped)
		if len(active) == 0 {
			return nil, queryFailure(fmt.Errorf("every selected list resource failed:\n%w", qErr))
		}
	}
	res.Selected = active

	// Generated Terraform inputs are removed on success and kept only when they
	// would help the user retry. Deferring the decision to the end of the run is
	// what makes that true: removing the query file as soon as the *query*
	// succeeded left a later failure — a model mismatch, a failed plan, a failed
	// import — with nothing to reproduce from.
	//
	// Retention keys off the run's outcome, never off which list resource types
	// failed. Deciding from the identity of a failure would mean encoding, per
	// provider and per provider version, which failures are routine: the query
	// engine legitimately errors for types whose facade does not apply to the
	// target, and those are expected rather than actionable. Types dropped along
	// the way are reported as facts, and --strict promotes them to a hard failure
	// for anyone who wants the reproducing file.
	defer func() {
		if res.QueryFilePath == "" {
			return
		}
		switch {
		case opts.DryRun:
			res.QueryFileRetainedReason = "dry run"
		case rerr != nil:
			res.QueryFileRetainedReason = "the run did not complete"
		default:
			_ = os.Remove(res.QueryFilePath) // best-effort
			res.QueryFilePath = ""
			return
		}
		// Write back the first, complete rendering: the retry loop prunes the
		// list blocks that errored, and a pruned file cannot reproduce the
		// failure it was kept to explain.
		_ = os.WriteFile(res.QueryFilePath, fullRender, 0o644)
	}()

	// Run provider-specific preflight steps. These sit deliberately between the
	// query and the plan: the live objects are now known, and the plan has not
	// yet been computed, so a step can still fold live-derived values into the
	// wrapper and have the plan pick them up. Doing this after the plan (or
	// after import) is too late — the addresses would already be wrong. Config
	// is shared by reference so derived keys reach BuildImportID.
	if opts.Config == nil {
		opts.Config = map[string]string{}
	}
	for _, step := range opts.PreflightSteps {
		if err := step.Run(ctx, PreflightContext{
			Dir:          opts.Dir,
			Live:         live,
			WrapperState: opts.WrapperState,
			Config:       opts.Config,
			QueryConfig:  opts.QueryConfig,
		}); err != nil {
			return nil, fmt.Errorf("%s: %w", step.Name(), err)
		}
	}

	importsPath := filepath.Join(opts.Dir, DefaultImportsFile)
	RemoveGeneratedImportsFile(importsPath)

	// Plan the existing module to find resources it wants to create (import
	// candidates) and the full set of module resources (for matching live
	// objects to module addresses). When the state is partially populated,
	// some resources show as no-ops — we still need them for matching.
	// Register the cleanup for the temporary .auto.tfvars *before* planning:
	// PlanCreates writes it, so a deferred removal registered after the call
	// never runs when the plan itself fails, leaking a generated input.
	tfvarsPath := filepath.Join(opts.Dir, "atelier-import.auto.tfvars")
	defer os.Remove(tfvarsPath) // best-effort

	planResult, err := PlanCreates(ctx, opts)
	if err != nil {
		return nil, err
	}

	// Refuse a run whose plan contradicts the live deployment, before anything
	// is written. Runs for every mode, including --dry-run: a dry run exists to
	// surface exactly this kind of problem.
	for _, check := range opts.PlanChecks {
		if err := check.Check(ctx, PlanCheckContext{
			Dir:          opts.Dir,
			Plan:         planResult.Plan,
			Planned:      planResult.AllModuleResources,
			Live:         live,
			Config:       opts.Config,
			QueryConfig:  opts.QueryConfig,
			WrapperState: opts.WrapperState,
		}); err != nil {
			return nil, fmt.Errorf("%s: %w", check.Name(), err)
		}
	}

	matched, unmatchedPlanned, unmatchedLive := Match(live, planResult.AllModuleResources, opts.Verbose)

	res.UnmatchedPlanned = unmatchedPlanned
	res.UnmatchedLive = unmatchedLive
	res.MatchedCount = len(matched)

	res.IDs, res.UnresolvedIDs = BuildImportIDs(matched, planResult.Creates, opts.BuildImportID, opts.Config)

	addrs := make([]string, 0, len(res.IDs))
	for addr := range res.IDs {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)

	// A dry run writes the artifact and re-plans with it in place, so the user
	// can review both what would be imported and — crucially — what would
	// still be *created*, before any state is touched. Nothing is imported.
	if opts.DryRun {
		// Write the artifact only when it would contain something. An empty
		// imports.tf is not a useful review object, and leaving one behind is
		// just litter.
		if len(res.IDs) > 0 {
			note := fmt.Sprintf("Dry run: %d resource(s) matched a live object.", len(res.IDs))
			if err := os.WriteFile(importsPath, RenderImportsFile(res.IDs, note), 0o644); err != nil {
				return nil, fmt.Errorf("write imports file: %w", err)
			}
			res.ImportsFilePath = importsPath
		}
		// Preview regardless. "Nothing matched" and "everything is already in
		// state" are exactly the runs where the user most needs to see what the
		// module would still do.
		preview, err := PlanCreates(ctx, opts)
		if err != nil {
			return res, err
		}
		summary := SummarizePlan(preview.Plan)
		res.Preview = &summary
		return res, nil
	}

	if len(res.IDs) == 0 {
		return res, nil
	}

	for i, addr := range addrs {
		id := res.IDs[addr]
		if err := tf.Import(ctx, addr, id); err != nil {
			// Leave behind the work that did not happen, so the user can
			// inspect, fix and retry rather than reconstructing the matched
			// pairs by hand. Execution stays `terraform import`, so retrying
			// from this file cannot create infrastructure.
			remaining := make(map[string]string, len(addrs)-i)
			for _, r := range addrs[i:] {
				remaining[r] = res.IDs[r]
			}
			note := fmt.Sprintf("Import failed at %s; %d of %d resource(s) remain.",
				addr, len(remaining), len(addrs))
			if werr := os.WriteFile(importsPath, RenderImportsFile(remaining, note), 0o644); werr == nil {
				res.ImportsFilePath = importsPath
				fmt.Fprintf(os.Stderr,
					"\nImported %d of %d before failing. The %d remaining resource(s) were\n"+
						"written to %s so you can inspect, edit and retry.\n",
					i, len(addrs), len(remaining), importsPath)
			}
			return res, enhanceError(fmt.Sprintf("terraform import %s %s: ", addr, id), err)
		}
		res.Imported = append(res.Imported, ImportResult{
			Address:  addr,
			Resource: shortName(addr),
		})
	}

	// Run provider-specific post-import normalization steps.
	// The post-import plan is shared and lazy: steps that need to know what the
	// configuration wants get one plan between them, and a run whose steps never
	// ask pays nothing.
	var (
		postPlan     *tfjson.Plan
		postPlanErr  error
		postPlanOnce sync.Once
	)
	pctx := PostImportContext{
		Dir:          opts.Dir,
		Imported:     res.Imported,
		WrapperState: opts.WrapperState,
		Plan: func() (*tfjson.Plan, error) {
			postPlanOnce.Do(func() {
				var pr *PlanResult
				pr, postPlanErr = PlanCreates(ctx, opts)
				if pr != nil {
					postPlan = pr.Plan
				}
			})
			return postPlan, postPlanErr
		},
	}
	for _, step := range opts.PostImportSteps {
		if err := step.Run(ctx, pctx); err != nil {
			return res, fmt.Errorf("%s: %w", step.Name(), err)
		}
	}

	return res, nil
}

// failedTypes maps the error diagnostics back to the list resource types that
// produced them, by matching each diagnostic's line to the list block spanning
// it in the generated query file.
func failedTypes(qerr *tfexec.QueryError, queryFilePath string) []string {
	lineType := listBlockLines(queryFilePath)
	seen := map[string]bool{}
	var out []string
	for _, d := range qerr.Diagnostics {
		if t, ok := lineType[d.Line]; ok && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// listBlockLines parses the query file and returns a map from each source line
// to the list resource type of the block spanning that line.
func listBlockLines(queryFilePath string) map[int]string {
	out := map[int]string{}
	data, err := os.ReadFile(queryFilePath)
	if err != nil {
		return out
	}
	f, diags := hclparse.NewParser().ParseHCL(data, filepath.Base(queryFilePath))
	if diags.HasErrors() {
		return out
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return out
	}
	for _, b := range body.Blocks {
		if b.Type != "list" || len(b.Labels) == 0 {
			continue
		}
		for ln := b.TypeRange.Start.Line; ln <= b.CloseBraceRange.End.Line; ln++ {
			out[ln] = b.Labels[0]
		}
	}
	return out
}

// dropTypes removes the named types from `active`, appending their names to
// `skipped`, and returns the reduced slice and the extended skipped list.
func dropTypes(active []ListResource, drop []string, skipped []string) ([]ListResource, []string) {
	dropSet := make(map[string]bool, len(drop))
	for _, d := range drop {
		dropSet[d] = true
	}
	kept := active[:0:0]
	for _, lr := range active {
		if dropSet[lr.Type] {
			skipped = append(skipped, lr.Type)
		} else {
			kept = append(kept, lr)
		}
	}
	return kept, skipped
}

func typeNames(rs []ListResource) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Type
	}
	return out
}

// mergeMaps returns a new map containing all entries from both maps. When a
// key appears in both, the second map wins.
func mergeMaps(a, b map[string]string) map[string]string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// enhanceError wraps an error with better context if it matches a known
// pattern. The prefix is prepended to the enhanced message, and the original
// error is preserved via Unwrap().
func enhanceError(prefix string, err error) error {
	if err == nil {
		return nil
	}

	errMsg := err.Error()
	if hint := ClassifyError(errMsg); hint != nil {
		return &enhancedError{
			prefix:   prefix,
			hint:     hint,
			original: err,
		}
	}
	return fmt.Errorf("%s%w", prefix, err)
}

// enhancedError wraps an error with helpful context from an ErrorHint.
type enhancedError struct {
	prefix   string
	hint     *ErrorHint
	original error
}

func (e *enhancedError) Error() string {
	var b strings.Builder
	b.WriteString(e.prefix)
	b.WriteString(e.hint.Summary)
	b.WriteString("\n\n")
	b.WriteString(e.hint.Details)
	b.WriteString("\n\n")
	b.WriteString("Original error:\n")
	b.WriteString(e.original.Error())
	return b.String()
}

func (e *enhancedError) Unwrap() error {
	return e.original
}
