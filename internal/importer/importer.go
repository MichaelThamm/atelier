package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

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

// PostImportContext carries the inputs a PostImportStep needs.
type PostImportContext struct {
	// Dir is the Terraform root directory.
	Dir string
	// Imported lists every resource successfully imported in this run.
	Imported []ImportResult
	// WrapperState is the parsed wrapper state (nil when --source was not used).
	WrapperState *wrapper.State
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
	// QueryFile overrides the generated query filename (DefaultQueryFile).
	QueryFile string
	// BinPath overrides terraform/tofu discovery (mainly for tests).
	BinPath string
	// PostImportSteps are provider-specific normalization steps that run
	// after terraform import completes. The pipeline calls them in order;
	// each step prints its own progress message.
	PostImportSteps []PostImportStep
	// BuildImportID constructs the provider-specific import ID for a matched
	// resource. When nil, Generate returns an error for every match (no
	// imports performed). Each provider supplies its own implementation.
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
	// QueryFilePath is the written *.tfquery.hcl (absolute).
	QueryFilePath string
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
func Generate(ctx context.Context, opts Options) (*Result, error) {
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
	for {
		existingVars := ExistingVars(opts.Dir)
		if err := os.WriteFile(res.QueryFilePath, RenderQueryFile(active, opts.Config, existingVars...), 0o644); err != nil {
			return nil, fmt.Errorf("write query file: %w", err)
		}
		found, qErr := tf.QueryList(ctx, opts.Config)
		if qErr == nil {
			live = found
			break
		}
		var qerr *tfexec.QueryError
		if opts.Strict || !errors.As(qErr, &qerr) {
			return nil, qErr
		}
		failed := failedTypes(qerr, res.QueryFilePath)
		if len(failed) == 0 {
			return nil, qErr // not attributable to a type; real failure
		}
		active, res.Skipped = dropTypes(active, failed, res.Skipped)
		if len(active) == 0 {
			return nil, fmt.Errorf("every selected list resource failed:\n%w", qErr)
		}
	}
	res.Selected = active

	// Plan the existing module (empty state) to find resources it wants to
	// create — the import candidates — then match each to a live object.
	planned, err := PlanCreates(ctx, opts)
	if err != nil {
		return nil, err
	}
	// Ensure the temporary .auto.tfvars written by PlanCreates is cleaned up
	// even if a later step (match, import, post-import) fails.
	tfvarsPath := filepath.Join(opts.Dir, "atelier-import.auto.tfvars")
	defer os.Remove(tfvarsPath) // best-effort
	matched, unmatchedPlanned, unmatchedLive := Match(live, planned, opts.Verbose)

	res.UnmatchedPlanned = unmatchedPlanned
	res.UnmatchedLive = unmatchedLive

	if len(matched) == 0 {
		return res, nil
	}

	// Build the provider-specific import ID for each match and run
	// `terraform import <address> <id>`.
	res.IDs = make(map[string]string, len(matched))
	for _, m := range matched {
		if opts.BuildImportID == nil {
			continue
		}
		id := opts.BuildImportID(m, opts.Config)
		if id == "" {
			continue
		}
		res.IDs[m.Address] = id
	}

	addrs := make([]string, 0, len(res.IDs))
	for addr := range res.IDs {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)

	for _, addr := range addrs {
		id := res.IDs[addr]
		if err := tf.Import(ctx, addr, id); err != nil {
			return res, fmt.Errorf("terraform import %s %s: %w", addr, id, err)
		}
		res.Imported = append(res.Imported, ImportResult{
			Address:  addr,
			Resource: shortName(addr),
		})
	}

	// Run provider-specific post-import normalization steps.
	pctx := PostImportContext{
		Dir:          opts.Dir,
		Imported:     res.Imported,
		WrapperState: opts.WrapperState,
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
