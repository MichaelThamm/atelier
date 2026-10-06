package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/bootstrap"
	"github.com/MichaelThamm/atelier/internal/candidate"
	"github.com/MichaelThamm/atelier/internal/modulesource"
	"github.com/MichaelThamm/atelier/internal/tfexec"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// moduleOpts holds the flags shared by `atelier add` and `atelier apply`.
type moduleOpts struct {
	Source       string   // positional git URL
	As           string   // --as: explicit HCL block name
	Ref          string   // --ref: git ref
	ModulePath   string   // --module: candidate subdir
	Dir          string   // --dir: target directory (add/apply)
	VarFiles     []string // --var-file: seed values from a .tfvars file or repo-local name (repeatable)
	Vars         []string // --var: KEY=VALUE overrides applied after all var-files (repeatable)
	Yes          bool     // --yes/-y: skip the target-directory confirmation
	ListVarFiles bool     // --list-var-files: print the .tfvars files available to a module and exit
	AllVarFiles  bool     // --all: with --list-var-files, print every bundled bundle, not just this module's
	Strict       bool     // --strict: make var-file binding warnings fatal
	JSON         bool     // --json: report the result as JSON on stdout (add only; apply rejects it)
}

func parseModuleArgs(args []string) (moduleOpts, error) {
	var opts moduleOpts
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--yes", "-y":
			opts.Yes = true
		case "--list-var-files":
			opts.ListVarFiles = true
		case "--all":
			opts.AllVarFiles = true
		case "--strict":
			opts.Strict = true
		case "--json":
			opts.JSON = true
		case "--as":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--as requires a name")
			}
			opts.As = args[i]
		case "--ref":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--ref requires a value")
			}
			opts.Ref = args[i]
		case "--module":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--module requires a path")
			}
			opts.ModulePath = args[i]
		case "--dir":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--dir requires a path")
			}
			opts.Dir = args[i]
		case "--var-file":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--var-file requires a path")
			}
			opts.VarFiles = appendVarFileList(opts.VarFiles, args[i])
		case "--var":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--var requires KEY=VALUE")
			}
			if err := validateVarPair(args[i]); err != nil {
				return opts, err
			}
			opts.Vars = append(opts.Vars, args[i])
		default:
			if strings.HasPrefix(a, "--var-file=") {
				opts.VarFiles = appendVarFileList(opts.VarFiles, strings.TrimPrefix(a, "--var-file="))
				continue
			}
			if strings.HasPrefix(a, "--var=") {
				pair := strings.TrimPrefix(a, "--var=")
				if err := validateVarPair(pair); err != nil {
					return opts, err
				}
				opts.Vars = append(opts.Vars, pair)
				continue
			}
			if strings.HasPrefix(a, "--dir=") {
				opts.Dir = strings.TrimPrefix(a, "--dir=")
				continue
			}
			if strings.HasPrefix(a, "-") {
				return opts, fmt.Errorf("unknown flag %q for module command", a)
			}
			positional = append(positional, a)
		}
	}
	if len(positional) == 0 {
		return opts, fmt.Errorf("module command requires a git URL argument")
	}
	if len(positional) > 1 {
		return opts, fmt.Errorf("module command takes exactly one URL argument; got %v", positional)
	}
	opts.Source = positional[0]
	// --all only widens a listing; on its own it would be silently ignored,
	// which reads as "you asked for everything and got the default".
	if opts.AllVarFiles && !opts.ListVarFiles {
		return opts, errors.New("--all is only meaningful with --list-var-files")
	}
	return opts, nil
}

// appendVarFileList splits a comma-separated --var-file value into individual
// names/paths. Commas are the one-shot syntax for several bundles
// (`--var-file cos-s3,cos-units`); repeating the flag remains equivalent.
func appendVarFileList(dst []string, raw string) []string {
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			dst = append(dst, p)
		}
	}
	return dst
}

// validateVarPair checks a `KEY=VALUE` --var argument and reports the same
// errors as import's --var parsing. The key must be a valid HCL identifier.
func validateVarPair(pair string) error {
	k, _, ok := strings.Cut(pair, "=")
	if !ok || k == "" {
		return fmt.Errorf("--var expects KEY=VALUE, got %q", pair)
	}
	return validateVarKey(k)
}

// varsToMap turns validated `KEY=VALUE` pairs into the map
// wrapper.ApplyVarOverrides expects. A value may itself contain `=`; only the
// first one splits.
func varsToMap(pairs []string) map[string]string {
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		if k, v, ok := strings.Cut(p, "="); ok {
			out[k] = v
		}
	}
	return out
}

// printVarFiles renders the `--list-var-files` output: one line per bundle,
// source-labelled (`local` walk-up or `repo`), name first so a long list stays
// scannable.
func printVarFiles(files []bootstrap.VarFile) {
	printVarFilesTo(os.Stdout, files)
}

func printVarFilesTo(w io.Writer, files []bootstrap.VarFile) {
	if len(files) == 0 {
		fmt.Fprintln(w, "No .tfvars bundles found (checked atelier.presets/ up-tree and the module repo).")
		return
	}
	// Size both leading columns to the widest value present, so the
	// descriptions line up whatever the bundles are called. Fixed widths shift
	// by one space for any value that happens to fill one exactly, and the
	// source labels differ in length ([repo] against [gallery]).
	nameWidth, sourceWidth := 0, 0
	for _, f := range files {
		if n := len(f.Name); n > nameWidth {
			nameWidth = n
		}
		if n := len(f.Source); n > sourceWidth {
			sourceWidth = n
		}
	}
	for _, f := range files {
		fmt.Fprintf(w, "[%-*s] %-*s %s\n", sourceWidth, f.Source, nameWidth, f.Name, f.Display)
	}
}

// listVarFileBundles clones the module to a scratch directory, prints the
// `.tfvars` bundles discoverable locally and in the repo, and returns. It is
// the shared body of `--list-var-files` for `add` and `import`: the
// clone is removed before returning, so nothing is written and no preflight is
// needed. Names are resolved against the same walk-up and repo locations the
// command itself would see.
//
// command names the invocation, so the envelope reports the command the caller
// actually ran rather than the one this helper happens to be shared with.
func listVarFileBundles(wrapperDir, command string, opts moduleOpts) error {
	ctx, cancel := interruptContext()
	defer cancel()
	files, err := bootstrap.ListVarFiles(ctx, bootstrap.InitOptions{
		WrapperDir:    wrapperDir,
		Source:        opts.Source,
		LocalSource:   modulesource.IsLocal(opts.Source),
		Ref:           opts.Ref,
		ModulePath:    opts.ModulePath,
		AllGalleryVar: opts.AllVarFiles,
	})
	if err != nil {
		return err
	}
	if opts.JSON {
		return renderJSON(os.Stdout, command, varFilesPayload(files))
	}
	printVarFiles(files)
	return nil
}

// printCandidates lists module candidates when a repository has several and
// none was chosen. Callers print and exit without writing.
func printCandidates(w io.Writer, cands []candidate.Candidate) {
	fmt.Fprintln(w, "Multiple module candidates found. Re-run with --module <path>:")
	for _, c := range cands {
		label := c.Path
		if c.Name != "" {
			label = fmt.Sprintf("%s — %s", c.Path, c.Name)
		}
		fmt.Fprintln(w, "  "+label)
	}
}

// errNeedsModule reports an ambiguous source under --json, where the candidate
// list has already gone to stderr.
var errNeedsModule = errors.New("several modules match this source; re-run with --module <path>")

// candidatesChannel decides where an ambiguous module's candidate list is printed
// and whether the run counts as a failure.
//
// Interactively the list goes to stdout and the run succeeds, because a person
// reads the list and re-runs with --module. Neither half survives --json: stdout
// is the payload channel, so prose there is unparseable, and exiting 0 would
// report the one outcome a consumer cannot detect. So the list moves to stderr
// and the caller gets an error to return.
func candidatesChannel(opts moduleOpts) (io.Writer, error) {
	if !opts.JSON {
		return os.Stdout, nil
	}
	return os.Stderr, errNeedsModule
}

// applyVarFlags layers `--var-file` bundles then `--var` overrides onto state
// and prints any binding warnings to stderr, in the `--var`-wins-over-
// `--var-file` order (ADR-0031). Names resolve against the cloned module repo
// or a walk-up bundle; a local path is used as-is. It does not write, so the
// caller persists with state.Write(). import uses its own value flow because
// it interleaves seeding from query variables.
func applyVarFlags(state *wrapper.State, wrapperDir, cloneDir, modulePath string, varFiles, vars []string, strict bool) error {
	if len(varFiles) > 0 {
		// Names the walk-up, the module repo, and the gallery do not resolve are
		// skipped with a warning: a gallery entry's preset may be superseded by a
		// --var-file the user supplies (for example one that also carries the
		// model_uuid and S3 credentials the gallery preset omits).
		resolved := make([]string, 0, len(varFiles))
		for _, ref := range varFiles {
			if p, ok := bootstrap.ResolveVarFile(wrapperDir, cloneDir, modulePath, ref); ok {
				resolved = append(resolved, p)
				continue
			}
			if _, err := os.Stat(ref); err == nil {
				resolved = append(resolved, ref)
				continue
			}
			// A bundle that resolved to nothing contributes nothing, and the run
			// goes on to write a wrapper missing every value it held. That is the one
			// outcome a reader cannot detect: the wrapper looks fine, and the
			// omission surfaces later as a missing required input, or not at all if
			// the module has a default. Under --strict it is an error, alongside the
			// unknown keys and type mismatches already fatal there.
			if strict {
				return fmt.Errorf("var-file %q not found: not a path, and no such bundle in an ancestor atelier.presets/, the module repository, or the gallery", ref)
			}
			fmt.Fprintf(os.Stderr, "warning: var-file %q not found; ignored\n", ref)
		}
		warns, err := wrapper.ApplyVarFiles(state, resolved, strict)
		if err != nil {
			return err
		}
		for _, w := range warns {
			fmt.Fprintln(os.Stderr, "warning:", w)
		}
	}
	for _, w := range wrapper.ApplyVarOverrides(state, varsToMap(vars)) {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	return nil
}

// runModuleAdd implements `atelier add <url>`. It scaffolds a wrapper for
// the module and opens the editor on it. `add` is the TUI path: unlike
// `apply`, it does not run `terraform init`/`apply`, and it never picks a
// revision for you (the gallery name supplies one).
func runModuleAdd(args []string) error {
	opts, err := parseModuleArgs(args)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	// A positional that is neither a URL nor a local path is a gallery entry
	// name; expand it before anything reads the source (ADR-0035).
	if err := resolveModuleSource(&opts); err != nil {
		return err
	}

	// --list-var-files clones the module to a scratch directory, prints the
	// `.tfvars` bundles discoverable locally and in the repository, then exits
	// (ADR-0031). The clone is removed before returning, so nothing is written
	// and no preflight is needed. It runs before the terraform check because
	// listing needs only git.
	if opts.ListVarFiles {
		return listVarFileBundles(cwd, "add", opts)
	}

	if _, err := tfexec.Locate(); err != nil {
		return err
	}

	// A target that already holds a wrapper is the additive case: append a
	// block to it and open the editor there. --dir is resolved as well as the
	// CWD, so a wrapper can be composed from outside it (ADR-0044). Otherwise
	// `add` creates a directory of its own, named after the candidate (or
	// --dir/--as), exactly as `apply` does — but stops before init/apply.
	if target := existingWrapperTarget(cwd, opts.Dir); target != "" {
		out, err := addModuleToWrapper(cwd, target, opts)
		if err != nil {
			return err
		}
		if opts.JSON {
			return renderJSON(os.Stdout, "add", addPayload(out))
		}
		return nil
	}
	return addModuleInNewDir(cwd, opts)
}

// existingWrapperTarget returns the wrapper this command should append to: the
// --dir target when it holds a main.tf, otherwise the CWD when it does. "" means
// there is none, and the command should scaffold a new wrapper.
//
// mainTFExists, not isWrapperDir: a hand-authored Terraform root is something
// `add` already appends to rather than scaffolding over, so it composes the
// same way. A --dir naming no wrapper at all is left to the scaffold path,
// which refuses a non-empty target (ADR-0044).
func existingWrapperTarget(cwd, dir string) string {
	if dir != "" {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		if dir = filepath.Clean(dir); mainTFExists(dir) {
			return dir
		}
		return ""
	}
	if mainTFExists(cwd) {
		return cwd
	}
	return ""
}

// addModuleToWrapper appends a module block to an existing wrapper in dir and
// opens the editor on it.
// jsonUnsupported rejects --json on a command that has no report of its own to
// give. A silent no-op would be worse than the error: a CI job reads a missing
// payload as success.
func jsonUnsupported(command string, opts moduleOpts) error {
	if !opts.JSON {
		return nil
	}
	return fmt.Errorf("--json is not supported by %q, which reports Terraform's own output.\n"+
		"Use 'atelier add --json' and run Terraform yourself.", command)
}

// addOutcome is what `atelier add` wrote, for the --json report.
type addOutcome struct {
	Dir   string                    // the wrapper directory the block landed in
	Block jsonModule                // the block as it reads back from main.tf
	All   []wrapper.ModuleBlockInfo // every block in the wrapper afterwards
}

// describeAdd reads the wrapper back to describe what was added, rather than
// echoing the request: --as is sanitised into a valid HCL identifier on the way
// in, and the ref a gallery entry resolves to is not the one that was asked for.
func describeAdd(dir, blockName string) (addOutcome, error) {
	blocks, err := wrapper.ReadModuleBlocks(dir)
	if err != nil {
		return addOutcome{}, err
	}
	for _, b := range blocks {
		if b.Name == blockName {
			return addOutcome{Dir: dir, Block: newJSONModule(b.Name, b.Source), All: blocks}, nil
		}
	}
	return addOutcome{}, fmt.Errorf("module block %q is missing from %s after writing it", blockName, dir)
}

func addModuleToWrapper(cwd, dir string, opts moduleOpts) (addOutcome, error) {
	state, err := composeModuleBlock(cwd, dir, opts, composeAdd, opts.Yes)
	if err != nil {
		return addOutcome{}, err
	}
	// --json reports and stops: there is no terminal to edit, so loading the
	// wrapper back into the TUI would only spend time.
	if opts.JSON {
		return describeAdd(dir, state.ModuleBlockName)
	}
	return addOutcome{}, openWrapper(dir)
}

// composeMode selects what a compose does when the target wrapper already
// declares the module being written.
type composeMode int

const (
	// composeAdd never writes into an existing block. `add` authors a wrapper,
	// and a module already in it is a duplicate to be refused, not silently
	// updated behind the user's back.
	composeAdd composeMode = iota

	// composeApply writes the requested declaration into the wrapper's block
	// for that module — re-pointing its ref, merging --var into the values it
	// already holds, and deploying the result (ADR-0050).
	composeApply
)

// composeRequest is what the fresh clone tells a compose about the module:
// the `source =` value it resolved to, the block name it implies, and the
// block name the user asked for.
type composeRequest struct {
	// source is the composed source — sub-path and ref included — which is both
	// what the block will record and what existing blocks are matched against.
	source string
	// requested is the URL or gallery name as typed, for a remedy line.
	requested string
	// derivedName is the block name the module implies, before --as.
	derivedName string
	// as is the sanitised --as, or "" when the flag was not given.
	as string
}

// composePlan is where a compose writes: an existing block to update in place,
// or a fresh name to append under.
type composePlan struct {
	update    wrapper.ModuleBlockInfo // non-empty Name: update this block in place
	blockName string                  // set when appending
	// ignoredAs is an --as that named no block, so the compose fell back to the
	// one block already declaring the module. Reported rather than silently
	// dropped: a gallery entry supplies the name without the user typing it.
	ignoredAs string
}

// planCompose decides which module block a compose writes, given the blocks the
// wrapper already declares.
//
// A wrapper's own declaration is the unit of identity: a block *is* the module
// the user asked for when its repository and sub-directory match, whatever ref
// it is pinned at. Refs are what a compose changes, not what makes a block a
// different module — that is what makes re-running a command converge instead
// of appending a second copy (ADR-0050).
//
// --as selects the block to write, and never declares a second copy of a module
// the wrapper already has: two copies of one module means Terraform tries to
// create the same resources twice, which usually collides at apply rather than
// here. A name that matches no block falls back to the block that does, so a
// name the user did not type — a gallery entry's — cannot dead-end the command.
func planCompose(mode composeMode, existing []wrapper.ModuleBlockInfo, req composeRequest) (composePlan, error) {
	// A block declaring this module at the same ref and one declaring it at a
	// different ref are one question to a compose: which blocks already declare
	// it, since the ref is what the compose may change.
	same, otherRef := findExistingInstances(existing, req.source)
	instances := slices.Concat(same, otherRef)

	if mode == composeAdd {
		if len(instances) > 0 {
			return composePlan{}, duplicateModuleError(instances, req)
		}
		return composePlan{blockName: firstNonEmpty(req.as, req.derivedName)}, nil
	}

	if req.as != "" {
		// --as names an existing block: that is the one to write, provided it is
		// this module's.
		if named := blockByName(existing, req.as); named != nil {
			if !slices.ContainsFunc(instances, func(b wrapper.ModuleBlockInfo) bool { return b.Name == named.Name }) {
				return composePlan{}, asMismatchError(*named, req)
			}
			return composePlan{update: *named}, nil
		}
		// --as names no block. It can still mean "this module": a gallery entry
		// supplies the name from its own entry, and the wrapper may well hold
		// the module under a different one. So when the module is declared
		// exactly once, that block is what was meant — the name is reported
		// rather than obeyed, and never used to declare a second copy.
		switch len(instances) {
		case 0:
			return composePlan{blockName: req.as}, nil
		case 1:
			return composePlan{update: instances[0], ignoredAs: req.as}, nil
		default:
			return composePlan{}, ambiguousModuleError(instances, req)
		}
	}

	switch len(instances) {
	case 0:
		return composePlan{blockName: req.derivedName}, nil
	case 1:
		return composePlan{update: instances[0]}, nil
	default:
		return composePlan{}, ambiguousModuleError(instances, req)
	}
}

// blockByName returns the named block, or nil when the wrapper has none.
func blockByName(blocks []wrapper.ModuleBlockInfo, name string) *wrapper.ModuleBlockInfo {
	for i := range blocks {
		if blocks[i].Name == name {
			return &blocks[i]
		}
	}
	return nil
}

// composeModuleBlock writes opts.Source's module into the wrapper in dir: it
// updates the block already declaring that module, or appends a new one, then
// returns the state it wrote. `add` then opens the editor on it and `apply` then
// deploys that root, so both compose through one implementation (ADR-0044).
//
// cwd is the invocation directory, which a relative local `source` resolves
// against. It differs from dir whenever `--dir` named the wrapper, and without
// it `atelier add ../module --dir elsewhere` would look for the module under
// the wrapper.
//
// prompt answers the target-directory preflight. `add` asks, with --yes as its
// escape hatch; `apply` does not, because Terraform's plan prompt is that
// command's confirmation and it rejects --yes (ADR-0034).
func composeModuleBlock(cwd, dir string, opts moduleOpts, mode composeMode, prompt bool) (*wrapper.State, error) {
	ctx, cancel := interruptContext()
	defer cancel()

	// Confirm the target directory before writing anything into it. A target
	// with no path of its own is the CWD, so this is the only thing standing
	// between a mistyped `cd` and a main.tf in the user's home directory.
	//
	// This runs BEFORE the SIGINT handler below is installed, deliberately.
	// signal.NotifyContext converts Ctrl-C into a context cancellation instead
	// of terminating the process, so with the handler in place a Ctrl-C at this
	// prompt is swallowed: the read keeps waiting for a line, and answering `y`
	// afterwards proceeds with an already-cancelled context and fails with a
	// bare "context canceled". While the only thing running is a prompt, the
	// default SIGINT behaviour — exit immediately — is exactly what the user is
	// asking for.
	if !isWrapperDir(dir) {
		ok, err := confirmTargetDir(dir, "Add a module block to main.tf in", prompt)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
	}

	stop := startSpinner("Cloning and preparing module…")
	defer stop()

	// Run the same clone + candidate-discovery flow as a fresh bootstrap, so a
	// module whose Terraform lives in a subdirectory (e.g. `terraform/`) is
	// appended with the correct `//<subdir>` source and shows its variables.
	prep, err := bootstrap.PrepareModule(ctx, bootstrap.InitOptions{
		WrapperDir:    dir,
		Source:        opts.Source,
		LocalSource:   modulesource.IsLocal(opts.Source),
		SourceBaseDir: cwd,
		Ref:           opts.Ref,
		ModulePath:    opts.ModulePath,
	})
	stop()
	if err != nil {
		return nil, err
	}
	if prep.State == nil {
		// Multiple candidates — user needs --module. Nothing was written.
		w, err := candidatesChannel(opts)
		printCandidates(w, prep.Candidates)
		return nil, err
	}
	state := prep.State

	existing, err := wrapper.ReadModuleBlocks(dir)
	if err != nil {
		return nil, fmt.Errorf("reading main.tf: %w", err)
	}

	plan, err := planCompose(mode, existing, composeRequest{
		source:      state.Source,
		requested:   opts.Source,
		derivedName: state.ModuleBlockName,
		as:          sanitizedAs(opts.As),
	})
	if err != nil {
		return nil, err
	}

	if plan.update.Name != "" {
		if err := updateModuleBlock(dir, plan, state, prep, opts); err != nil {
			return nil, err
		}
		return state, nil
	}

	state.ModuleBlockName = plan.blockName
	if taken := blockNameTaken(state.ModuleBlockName, existing); taken {
		unique := uniqueBlockName(state.ModuleBlockName, existing)
		fmt.Fprintf(os.Stderr, "note: block name %q is taken; using %q.\n", plan.blockName, unique)
		state.ModuleBlockName = unique
	}

	// Apply --var-file values to the module being added, before writing it,
	// then --var overrides on top.
	if err := applyVarFlags(state, dir, prep.CloneDir, prep.ModulePath, opts.VarFiles, opts.Vars, opts.Strict); err != nil {
		return nil, err
	}

	// Write the new module block to main.tf.
	if err := state.Write(); err != nil {
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "Added module %q from %s\n", state.ModuleBlockName, opts.Source)

	return state, nil
}

// updateModuleBlock rewrites the block a wrapper already declares for this
// module, in place. It carries that block's current declaration onto the schema
// just cloned, layers --var-file then --var on top, and writes it back under
// its existing name — which is what makes RenderMain update the block rather
// than append a second one (ADR-0050).
//
// Carrying the old declaration over first is the whole point. The freshly
// cloned state carries the module's defaults, not the values the wrapper
// already holds, so writing it as it stands would revert every input the flags
// do not mention back to its default.
func updateModuleBlock(dir string, plan composePlan, state *wrapper.State, prep *bootstrap.ModulePrep, opts moduleOpts) error {
	block := plan.update
	// Read against the *new* schema, so a value that survived the revision is
	// typed and can be merged, and a meta-argument or wired expression is
	// recognised as something other than an input.
	prior, err := wrapper.ReadMainForBlock(dir, block.Name, state.Vars)
	if err != nil {
		return fmt.Errorf("reading the current block %q: %w", block.Name, err)
	}
	priorNames := priorAttrNames(prior)

	state.ModuleBlockName = block.Name
	state.AdoptPrior(prior.Values, prior.UnknownAttrs)

	if plan.ignoredAs != "" {
		fmt.Fprintf(os.Stderr,
			"note: --as %s names no block in this wrapper; updating %q, which already declares this module.\n",
			plan.ignoredAs, block.Name)
	}
	reportRefTransition(block, state, priorNames)

	if err := applyVarFlags(state, dir, prep.CloneDir, prep.ModulePath, opts.VarFiles, opts.Vars, opts.Strict); err != nil {
		return err
	}
	if pruned := prunedArgs(state, priorNames); len(pruned) > 0 {
		verb := "match the module default and were pruned"
		if len(pruned) == 1 {
			verb = "matches the module default and was pruned"
		}
		fmt.Fprintf(os.Stderr, "note: %s %s: %s\n",
			plural(len(pruned), "argument", "arguments"), verb, strings.Join(pruned, ", "))
	}

	if err := state.Write(); err != nil {
		return err
	}
	// .atelier/session.json records which module the wrapper is pinned at. Left
	// stale it would reopen the TUI on the previous ref, and the next save from
	// there would write that ref back over this one.
	if err := bootstrap.ReconcileSession(dir, block.Name, state, prep.ResolvedSHA); err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	fmt.Fprintf(os.Stderr, "Updated module %q in %s\n", block.Name, dir)
	return nil
}

// priorAttrNames is the set of attribute names the block carried, whether it
// held a value or an expression Atelier preserves verbatim.
func priorAttrNames(prior *wrapper.ParsedMain) map[string]bool {
	names := make(map[string]bool, len(prior.Values)+len(prior.UnknownAttrs))
	for name := range prior.Values {
		names[name] = true
	}
	for _, ra := range prior.UnknownAttrs {
		names[ra.Name] = true
	}
	return names
}

// reportRefTransition says what the update changed, because a block rewritten
// under the same name looks like a second command doing nothing. It stays quiet
// when there is nothing worth a line.
func reportRefTransition(block wrapper.ModuleBlockInfo, state *wrapper.State, priorNames map[string]bool) {
	if block.Source == state.Source {
		fmt.Fprintf(os.Stderr, "note: %q already declares this module; updating it in place.\n", block.Name)
	} else {
		fmt.Fprintf(os.Stderr, "note: re-pointing %q from ref %s to ref %s.\n",
			block.Name, refOrHead(block.Source), refOrHead(state.Source))
	}
	orphaned, added := diffVarNames(priorNames, state.Vars)
	if len(orphaned) > 0 {
		fmt.Fprintf(os.Stderr, "note: dropped %s the new revision no longer declares: %s\n",
			plural(len(orphaned), "argument", "arguments"), strings.Join(orphaned, ", "))
	}
	if len(added) > 0 {
		var required int
		names := make([]string, 0, len(added))
		for _, v := range added {
			names = append(names, v.Name)
			if !v.HasDefault {
				required++
			}
		}
		note := fmt.Sprintf("note: %s this revision adds: %s", plural(len(added), "new input", "new inputs"), strings.Join(names, ", "))
		if required > 0 {
			note += fmt.Sprintf(" (%d required)", required)
		}
		fmt.Fprintln(os.Stderr, note)
	}
}

// refOrHead renders a source's ref for a message, naming the unpinned case
// rather than showing an empty string.
func refOrHead(source string) string {
	if _, ref := modulesource.Decompose(source); ref != "" {
		return ref
	}
	return "HEAD (unpinned)"
}

// prunedArgs names the arguments the sparse rule is about to drop from the
// block: inputs the wrapper already carried that now hold their declared
// default. They are reported rather than silently removed, so an argument
// disappearing from the user's file is never a surprise (ADR-0007).
func prunedArgs(state *wrapper.State, priorNames map[string]bool) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(priorNames)) {
		v := state.FindVar(name)
		if v == nil {
			continue
		}
		if _, wired := state.WiredExpression(name); wired {
			continue
		}
		current, _ := state.VariableValue(name)
		if !wrapper.ShouldEmit(v, current) {
			out = append(out, name)
		}
	}
	return out
}

// diffVarNames compares the inputs a block carried with the schema of the
// revision being written, naming the ones that revision no longer declares and
// the ones it introduces. A dropped input silently loses the user's value, so
// both directions are worth a line.
func diffVarNames(priorNames map[string]bool, vars []tfvars.Variable) (orphaned []string, added []tfvars.Variable) {
	declared := make(map[string]bool, len(vars))
	for _, v := range vars {
		declared[v.Name] = true
		if !priorNames[v.Name] {
			added = append(added, v)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(priorNames)) {
		if !declared[name] && !wrapper.IsMetaArgument(name) {
			orphaned = append(orphaned, name)
		}
	}
	return orphaned, added
}

// sanitizedAs is the block name --as asks for, or "" when it was not given.
func sanitizedAs(as string) string {
	if as == "" {
		return ""
	}
	return sanitizeBlockName(as)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// plural picks the noun form for a count, so a message never reads "1 arguments".
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// addModuleInNewDir scaffolds a wrapper into a fresh directory — named after the
// discovered candidate, or by --dir/--as — and opens the editor on it. It shares
// target resolution and the staged write with `apply`, but stops before
// `terraform init`/`apply`: `add` is the TUI path.
func addModuleInNewDir(cwd string, opts moduleOpts) error {
	ctx, cancel := interruptContext()
	defer cancel()

	target, state, err := scaffoldIntoTarget(ctx, cwd, opts)
	if err != nil {
		return err
	}
	if target == "" {
		return nil // multiple candidates; already reported
	}
	if opts.JSON {
		out, err := describeAdd(target, state.ModuleBlockName)
		if err != nil {
			return err
		}
		return renderJSON(os.Stdout, "add", addPayload(out))
	}
	return openWrapper(target)
}

// scaffoldIntoTarget clones the module, writes a wrapper into a staging
// directory beside the target, applies --as/--var-file/--var, and moves it into
// place. It returns the target path and the authored State, or "" when the
// module had multiple candidates (nothing written; the candidate list has been
// printed).
//
// It is the shared body of the "create a new directory" path for `add`
// and `apply`, which differ only in their tail (open the TUI vs. run
// init/apply).
func scaffoldIntoTarget(ctx context.Context, cwd string, opts moduleOpts) (string, *wrapper.State, error) {
	// Resolve an explicit target now so a collision is caught before the
	// clone. A derived name is only known after candidate discovery.
	explicitTarget := ""
	if opts.Dir != "" || opts.As != "" {
		explicitTarget = opts.Dir
		if explicitTarget == "" {
			explicitTarget = opts.As
		}
		if !filepath.IsAbs(explicitTarget) {
			explicitTarget = filepath.Join(cwd, explicitTarget)
		}
		explicitTarget = filepath.Clean(explicitTarget)
		if err := checkApplyTarget(explicitTarget, "choose another name with --dir or --as"); err != nil {
			return "", nil, err
		}
	}

	// Stage under the target's parent so the final move is a same-filesystem
	// rename. Staging keeps the clone to one round trip while still letting the
	// directory be named after the discovered module candidate.
	parent := cwd
	if explicitTarget != "" {
		parent = filepath.Dir(explicitTarget)
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", nil, err
		}
	}
	staging, err := os.MkdirTemp(parent, ".atelier-add-*")
	if err != nil {
		return "", nil, err
	}
	moved := false
	defer func() {
		if !moved {
			_ = os.RemoveAll(staging)
		}
	}()

	res, cleanup, err := bootstrapFreshWrapper(cwd, staging, opts.Source, opts.Ref, opts.ModulePath)
	if err != nil {
		return "", nil, err
	}
	if res.State == nil {
		// Multiple candidates — user needs --module. Nothing was written.
		w, err := candidatesChannel(opts)
		printCandidates(w, res.Candidates)
		return "", nil, err
	}

	// --as renames the HCL block the bootstrap wrote under the candidate-derived
	// name. Re-writing under the new name would append a second block, so use
	// the rename primitive (which also updates State.ModuleBlockName). The
	// later Write persists any --var values on top of it.
	if opts.As != "" {
		if err := res.State.RenameModuleBlock(sanitizeBlockName(opts.As)); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	// Apply --var-file values, then --var overrides on top, before the wrapper
	// moves (the clone the names resolve against still lives under staging).
	if err := applyVarFlags(res.State, staging, res.CloneDir, res.ModulePath, opts.VarFiles, opts.Vars, opts.Strict); err != nil {
		cleanup()
		return "", nil, err
	}
	if opts.As != "" || len(opts.VarFiles) > 0 || len(opts.Vars) > 0 {
		if err := res.State.Write(); err != nil {
			cleanup()
			return "", nil, err
		}
	}

	target := explicitTarget
	if target == "" {
		name := bootstrap.ModuleDirName(res.ModulePath, modulesource.RepoBasename(opts.Source))
		target = filepath.Join(cwd, name)
		if err := checkApplyTarget(target, "choose another name with --dir or --as"); err != nil {
			cleanup()
			return "", nil, err
		}
	}

	// Move the staged wrapper into place. An existing empty directory is
	// reused; a non-empty one was refused by checkApplyTarget.
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		if err := os.Remove(target); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	if err := os.Rename(staging, target); err != nil {
		cleanup()
		return "", nil, err
	}
	moved = true
	return target, res.State, nil
}

// runModuleApply implements `atelier apply <url>`: scaffold a wrapper in
// a new directory — or compose into the wrapper the target already names — then
// run `terraform init` and `terraform apply` (ADR-0034). It saves the user from
// `mkdir && cd && terraform init && terraform apply`.
func runModuleApply(args []string) error {
	opts, err := parseModuleArgs(args)
	if err != nil {
		return err
	}
	if err := jsonUnsupported("atelier apply", opts); err != nil {
		return err
	}
	// The apply approval comes from Terraform's own prompt, so it only has a
	// gate to show when stdin is a terminal. Detect that here: an
	// auto-approved apply is used otherwise (see applyWrapper), so a pipe,
	// CI, or `atelier apply … < /dev/null` still runs unattended.
	// --yes is still rejected below: the flag means "don't prompt", and here
	// the prompt is the confirmation, so it would be a second, confusing
	// spelling of auto-approve.
	interactive := isTerminal(os.Stdin)
	if opts.Yes {
		return fmt.Errorf("--yes is not valid for 'atelier apply': Terraform's apply prompt is the confirmation; apply runs without one when stdin is not a terminal")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := resolveModuleSource(&opts); err != nil {
		return err
	}
	if opts.ListVarFiles {
		return listVarFileBundles(cwd, "apply", opts)
	}
	if _, err := tfexec.Locate(); err != nil {
		return err
	}

	// A target that already holds a wrapper composes: the module block is
	// written into that wrapper and the root is deployed, so `--dir` composes
	// and running `apply` inside a wrapper deploys it rather than nesting a
	// second root inside it (ADR-0044). What the block already declares decides
	// whether it is updated or a new one appended (ADR-0050).
	if target := existingWrapperTarget(cwd, opts.Dir); target != "" {
		// prompt=false: --yes is rejected above, so there is no escape hatch
		// from a preflight question here — Terraform's plan prompt is the gate.
		state, err := composeModuleBlock(cwd, target, opts, composeApply, false)
		if err != nil || state == nil {
			return err
		}
		if err := requireNoUnsetRequiredVars(state, target,
			"pass --var NAME=VALUE (or --var-file) and re-run"); err != nil {
			return err
		}
		return applyWrapper(target, !interactive)
	}

	ctx, cancel := interruptContext()
	defer cancel()
	target, state, err := scaffoldIntoTarget(ctx, cwd, opts)
	if err != nil {
		return err
	}
	if target == "" {
		return nil // multiple candidates; already reported
	}

	if err := requireNoUnsetRequiredVars(state, target,
		"pass --var NAME=VALUE (or --var-file) and re-run"); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Wrapper written to %s\n", target)
	return applyWrapper(target, !interactive)
}

// requireNoUnsetRequiredVars reports the variables `apply` cannot deploy
// without. The block is already in the target's main.tf at this point, so
// howToFix says how to fill them — the two paths differ, because re-running a
// composed `add` would be refused as a duplicate of the block just written.
func requireNoUnsetRequiredVars(state *wrapper.State, target, howToFix string) error {
	missing := unsetRequiredVars(state)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("module requires a value for %s; %s\nWrapper written to %s",
		strings.Join(missing, ", "), howToFix, target)
}

// openWrapper loads the wrapper in dir and launches the TUI on it.
func openWrapper(dir string) error {
	ctx, cancel := interruptContext()
	defer cancel()
	res, err := bootstrap.LoadExisting(ctx, dir, nil)
	if err != nil {
		return err
	}
	return launchTUI(res, dir)
}

// checkApplyTarget refuses a target directory that already holds files.
// `apply` creates a fresh wrapper; a non-empty directory is almost
// always a mistyped --dir or --as, so it is refused rather than scaffolded
// over. An existing empty directory is allowed and reused. remedy is the
// command's own way of naming another target, since the flags differ per verb.
func checkApplyTarget(target, remedy string) error {
	info, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("target %s exists and is not a directory", target)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("target directory %s already exists and is not empty; %s", target, remedy)
	}
	return nil
}

// unsetRequiredVars lists required module variables (declared without a
// default) that have no value. `apply` checks this before running
// Terraform, which would otherwise reject the run with a less direct message.
//
// A variable wired to an expression counts as set: Atelier cannot evaluate
// `model_uuid = module.loki.endpoint` to a value, but Terraform can, so
// reporting it missing would refuse a wrapper that deploys. This matches the
// TUI's own required-unset test.
func unsetRequiredVars(state *wrapper.State) []string {
	var missing []string
	for _, v := range state.Vars {
		if !v.VarIsRequired() {
			continue
		}
		if _, wired := state.WiredExpression(v.Name); wired {
			continue
		}
		val, ok := state.Values[v.Name]
		if !ok || val == cty.NilVal || val.IsNull() {
			missing = append(missing, v.Name)
		}
	}
	return missing
}

// applyWrapper runs `terraform init -upgrade` then `terraform apply` in dir.
// When there is a terminal, apply is Terraform's interactive prompt
// (autoApprove=false); otherwise it is auto-approved (autoApprove=true) so a
// pipe or CI run does not hang on a question nobody can answer. See ADR-0034.
//
// -upgrade is required, not incidental. Composing a module into an already
// initialised root widens the root's provider constraints, and a plain init
// then refuses the lock file's selection — "must use terraform init -upgrade to
// allow selection of new versions" — which is the difference between a composed
// wrapper deploying and not. It is the same condition the TUI's ResetInit marks
// after a ref switch rewrites a module source.
func applyWrapper(dir string, autoApprove bool) error {
	ctx, cancel := interruptContext()
	defer cancel()

	tf, err := tfexec.New(dir, "")
	if err != nil {
		// Terraform could not be located or its log directory opened. Nothing ran,
		// so this is the environment's problem, not a deployment failure.
		return err
	}

	// init is non-interactive; stream Terraform's own progress (module and
	// provider fetches) rather than animating a spinner alongside it. The two
	// would race on the same terminal: the spinner writes carriage-return frames
	// with no newline while Terraform writes newline-terminated lines, so a frame
	// and Terraform's output land on the same line. Clear the writers before
	// apply, which attaches the terminal itself when interactive.
	fmt.Fprintln(os.Stderr, "Running terraform init…")
	tf.SetStdout(os.Stdout)
	tf.SetStderr(os.Stderr)
	err = tf.InitUpgrade(ctx)
	tf.SetStdout(nil)
	tf.SetStderr(nil)
	if err != nil {
		return terraformFailure(fmt.Errorf("terraform init: %w", err))
	}

	if autoApprove {
		fmt.Fprintln(os.Stderr, "no terminal on stdin: applying without the plan prompt.")
	}
	return terraformFailure(tf.ApplyDirect(ctx, autoApprove))
}

// bootstrapFreshWrapper clones a remote module source and writes a wrapper
// into dir, printing a spinner and any bootstrap warnings. The clone, authoring
// and transactional cleanup are bootstrap.FreshWrapper, so `add` and
// `import --source` cannot drift.
//
// It returns a result with a nil State when the module has multiple Terraform
// candidates (nothing was written); the caller decides how to present the
// candidate list — `add` prints it and exits 0, `import --source`
// prints it and errors. The returned cleanup closure removes an .atelier/
// this run created; callers invoke it on their own post-bootstrap failure
// paths.
//
// sourceBaseDir resolves a relative local `source` path (`.`.`/…`); it defaults
// to dir, which is right when the wrapper is written where the user invoked the
// command. `apply` stages the wrapper elsewhere and passes the
// invocation directory so a local source still resolves.
func bootstrapFreshWrapper(sourceBaseDir, dir, source, ref, modulePath string) (*bootstrap.Result, func(), error) {
	if _, err := tfexec.Locate(); err != nil {
		return nil, nil, err
	}

	ctx, cancel := interruptContext()
	defer cancel()
	if sourceBaseDir == "" {
		sourceBaseDir = dir
	}

	stop := startSpinner("Cloning and preparing module…")
	defer stop()
	fresh, err := bootstrap.FreshWrapper(ctx, bootstrap.InitOptions{
		WrapperDir:    dir,
		Source:        source,
		LocalSource:   modulesource.IsLocal(source),
		SourceBaseDir: sourceBaseDir,
		Ref:           ref,
		ModulePath:    modulePath,
	})
	stop()
	if err != nil {
		return nil, nil, err
	}
	if fresh.State == nil {
		// Multiple candidates — nothing written. The caller presents them.
		return fresh.Result, nil, nil
	}

	for _, w := range fresh.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	return fresh.Result, fresh.Release, nil
}

// runModuleRm implements `atelier rm <name>`.
func runModuleRm(args []string) error {
	var force bool
	var name string
	for _, a := range args {
		if a == "--force" || a == "-f" || a == "--yes" || a == "-y" {
			force = true
		} else if strings.HasPrefix(a, "-") {
			return fmt.Errorf("unknown flag %q for rm", a)
		} else {
			if name != "" {
				return fmt.Errorf("rm takes exactly one module name")
			}
			name = a
		}
	}
	if name == "" {
		return fmt.Errorf("rm requires a module name. Use 'atelier ls' to see modules")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	blocks, err := wrapper.ReadModuleBlocks(cwd)
	if err != nil {
		return fmt.Errorf("reading main.tf: %w", err)
	}

	// Find the block.
	found := false
	for _, blk := range blocks {
		if blk.Name == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("no module %q found in main.tf. Use 'atelier ls' to see modules", name)
	}

	if !force {
		fmt.Fprintf(os.Stderr, "Removing module %q deletes its block from main.tf.\n", name)
		fmt.Fprintf(os.Stderr, "Note: existing Terraform state for this module is NOT destroyed. Run 'terraform destroy -target=module.%s' first if needed.\n", name)
		ok, err := confirm("Proceed?")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "aborted")
			return nil
		}
	}

	// Remove the module block from main.tf.
	if err := wrapper.RemoveModuleBlock(cwd, name); err != nil {
		return fmt.Errorf("removing module block: %w", err)
	}

	// Clean up the clone directory if it exists.
	cloneBase := filepath.Join(cwd, wrapper.AtelierDir, "clone")
	if entries, err := os.ReadDir(cloneBase); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.Contains(e.Name(), name) {
				_ = os.RemoveAll(filepath.Join(cloneBase, e.Name()))
			}
		}
	}

	fmt.Fprintf(os.Stderr, "Removed module %q from wrapper.\n", name)
	return nil
}

// runModuleList implements `atelier ls`.
func runModuleList(args []string) error {
	asJSON := false
	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
		case strings.HasPrefix(a, "-") && a != "--help" && a != "-h":
			return fmt.Errorf("unknown flag %q for ls", a)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	if !mainTFExists(cwd) {
		if asJSON {
			return renderJSON(os.Stdout, "ls", lsPayload(false, nil))
		}
		fmt.Println("Not a wrapper directory (no main.tf). Run 'atelier wrappers' to find wrappers under here.")
		return nil
	}

	blocks, err := wrapper.ReadModuleBlocks(cwd)
	if err != nil {
		return fmt.Errorf("reading main.tf: %w", err)
	}
	if len(blocks) == 0 {
		if asJSON {
			return renderJSON(os.Stdout, "ls", lsPayload(true, nil))
		}
		fmt.Println("No modules found in this wrapper.")
		return nil
	}

	if asJSON {
		return renderJSON(os.Stdout, "ls", lsPayload(true, blocks))
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSOURCE\tREF")
	for _, blk := range blocks {
		src, ref := modulesource.Decompose(blk.Source)
		if ref == "" {
			ref = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", blk.Name, src, ref)
	}
	tw.Flush()
	return nil
}

// sanitizeBlockName converts a user-provided name to a valid HCL identifier.
// HCL identifiers must match [a-zA-Z_][a-zA-Z0-9_]*.
func sanitizeBlockName(name string) string {
	// Replace hyphens and dots with underscores.
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, ".", "_")
	// Strip any character that is not a letter, digit, or underscore.
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	name = b.String()
	// Strip leading digits.
	for len(name) > 0 && name[0] >= '0' && name[0] <= '9' {
		name = name[1:]
	}
	if name == "" {
		name = "module"
	}
	return name
}

// uniqueBlockName appends _2, _3, etc. if blockName collides with existing blocks.
func uniqueBlockName(blockName string, existing []wrapper.ModuleBlockInfo) string {
	names := make(map[string]bool, len(existing))
	for _, blk := range existing {
		names[blk.Name] = true
	}
	if !names[blockName] {
		return blockName
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s_%d", blockName, i)
		if !names[candidate] {
			return candidate
		}
	}
}

// blockNameTaken reports whether any existing block already uses name.
func blockNameTaken(name string, existing []wrapper.ModuleBlockInfo) bool {
	for _, blk := range existing {
		if blk.Name == name {
			return true
		}
	}
	return false
}

// moduleSourceIdentity is the comparable identity of a module reference: which
// repository, which sub-directory within it, and which ref.
type moduleSourceIdentity struct {
	remote string
	path   string
	ref    string
}

// moduleIdentity parses a Terraform module source string into its identity.
//
// The comparison is on the parsed parts rather than the raw string because the
// same module can be written several ways — with or without the `git::` prefix,
// with or without the `.git` suffix, with a trailing slash — and a raw string
// compare would call those different modules.
func moduleIdentity(source string) moduleSourceIdentity {
	remote, ref := modulesource.Decompose(source)
	return moduleSourceIdentity{
		remote: normaliseRemote(remote),
		path:   strings.Trim(modulesource.ModulePath(source), "/"),
		ref:    ref,
	}
}

// normaliseRemote reduces a git remote URL to a comparable form. Host names are
// case-insensitive and the `.git` suffix is optional, so neither should make two
// references to one repository look distinct.
func normaliseRemote(remote string) string {
	s := strings.ToLower(strings.TrimSpace(remote))
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	return strings.TrimSuffix(s, "/")
}

// sameRepository reports whether two identities name the same module in the
// same repository sub-directory, at any ref. That is the unit of identity a
// compose matches on: a ref is what it changes, not what makes a block a
// different module.
func (a moduleSourceIdentity) sameRepository(b moduleSourceIdentity) bool {
	return a.remote == b.remote && a.path == b.path
}

// sameModule reports whether two identities name the same module at the same
// revision.
func (a moduleSourceIdentity) sameModule(b moduleSourceIdentity) bool {
	return a.sameRepository(b) && a.ref == b.ref
}

// findExistingInstances splits the wrapper's module blocks into those that
// already reference exactly the module being added, and those that reference the
// same module at a different ref.
//
// Refs are compared literally, as they are written in main.tf. Resolving each
// existing block's ref to a SHA would catch `--ref main` duplicating an
// unpinned block whose HEAD is also main, but it would cost a network round trip
// per block on every add. The literal compare catches the case that actually
// happens — the same command run twice — and never blocks a distinct revision.
func findExistingInstances(existing []wrapper.ModuleBlockInfo, source string) (same, otherRef []wrapper.ModuleBlockInfo) {
	want := moduleIdentity(source)
	for _, blk := range existing {
		if blk.Source == "" {
			continue
		}
		got := moduleIdentity(blk.Source)
		switch {
		case want.sameModule(got):
			same = append(same, blk)
		case want.sameRepository(got):
			otherRef = append(otherRef, blk)
		}
	}
	return same, otherRef
}

// duplicateModuleError explains why an add was refused and how to get what the
// user probably wanted. `add` authors a wrapper, so a module already in one is
// refused outright; only `apply`, whose job is to converge and deploy, updates a
// block instead.
func duplicateModuleError(dups []wrapper.ModuleBlockInfo, req composeRequest) error {
	subject := subjectOf(dups)
	return fmt.Errorf(`%s already references this module:
  %s

Adding it again would declare a second copy of the same resources, which
Terraform will try to create alongside the first — usually failing at apply
with name collisions rather than here.

  configure the existing one:  atelier
  deploy a different revision:  atelier apply %s --ref <ref>
  keep a genuinely separate module:  add the block to main.tf by hand`,
		subject, req.source, req.requested)
}

// ambiguousModuleError reports that the wrapper declares the module more than
// once, so `apply` cannot tell which block the request is about. This is a state
// an earlier version of the command produced by appending rather than updating.
func ambiguousModuleError(dups []wrapper.ModuleBlockInfo, req composeRequest) error {
	var blocks strings.Builder
	for _, blk := range dups {
		fmt.Fprintf(&blocks, "  %-16s %s\n", blk.Name, blk.Source)
	}
	return fmt.Errorf(`this wrapper references this module more than once:
%s
atelier apply updates the block you name, rather than guessing which one you
meant.

  name the block to update:  atelier apply %s --as <name>
  drop the one you don't want:  atelier rm <name>`,
		strings.TrimRight(blocks.String(), "\n"), req.requested)
}

// asMismatchError reports that --as named a block which is a different module.
func asMismatchError(named wrapper.ModuleBlockInfo, req composeRequest) error {
	return fmt.Errorf(`--as %s names module %q, which references a different module:
  %s

--as selects the block to update, so it has to name this module's block. Drop
it to update the block %q, or name that one instead.`,
		req.as, named.Name, named.Source, firstNonEmpty(req.derivedName, named.Name))
}

// subjectOf names the modules a duplicate refers to, for an error message.
func subjectOf(dups []wrapper.ModuleBlockInfo) string {
	names := make([]string, len(dups))
	for i, blk := range dups {
		names[i] = strconv.Quote(blk.Name)
	}
	if len(names) == 1 {
		return "module " + names[0]
	}
	return "modules " + strings.Join(names, ", ")
}
