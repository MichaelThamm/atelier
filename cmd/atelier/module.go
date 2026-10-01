package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/bootstrap"
	"github.com/MichaelThamm/atelier/internal/candidate"
	"github.com/MichaelThamm/atelier/internal/modulesource"
	"github.com/MichaelThamm/atelier/internal/tfexec"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

const moduleUsage = `Usage:
  atelier module add <git-url> [--as NAME] [--ref REF] [--module SUBDIR]
                                [--var-file PATH|NAME] [--var KEY=VALUE]
                                [--list-var-files] [--strict] [--yes]
                                               Add a module to the wrapper.
                                               --var-file seeds values from a Terraform variable file: a local
                                               path, a name in an ancestor atelier.presets/ directory (walk-up),
                                               or a name committed to the module repo. Comma-separate or repeat
                                               for several (e.g. --var-file cos-s3,cos-units); later files win.
                                               --var sets a single module input (repeatable); --var wins over
                                               --var-file.
                                               --list-var-files prints the local and repo .tfvars bundles available.
                                               --strict makes var-file binding warnings (unknown variables,
                                               type mismatches) fatal instead of warnings.
  atelier module rm <name> [--force]           Remove a module from the wrapper.
  atelier module list                          List modules in the wrapper.
  atelier module apply <git-url> [--module SUBDIR] [--ref REF] [--as NAME]
                                [--dir PATH] [--var-file PATH|NAME]
                                [--var KEY=VALUE] [--list-var-files] [--strict]
                                               Scaffold a wrapper in a new directory (named after the
                                               module candidate, or --as/--dir), then run
                                               'terraform init' and 'terraform apply'. At a terminal
                                               you confirm the plan at Terraform's prompt; with no
                                               terminal it applies with -auto-approve.
`

// runModule dispatches the `atelier module` subcommand.
func runModule(args []string) error {
	if len(args) == 0 {
		fmt.Print(moduleUsage)
		return nil
	}
	switch args[0] {
	case "add":
		return runModuleAdd(args[1:])
	case "apply":
		return runModuleApply(args[1:])
	case "rm", "remove":
		return runModuleRm(args[1:])
	case "list", "ls":
		return runModuleList(args[1:])
	default:
		return fmt.Errorf("unknown module subcommand %q\n\n%s", args[0], moduleUsage)
	}
}

// moduleOpts holds the flags shared by `atelier module add` and
// `atelier module apply`.
type moduleOpts struct {
	Source       string   // positional git URL
	As           string   // --as: explicit HCL block name
	Ref          string   // --ref: git ref
	ModulePath   string   // --module: candidate subdir
	Dir          string   // --dir: target directory (module apply only)
	VarFiles     []string // --var-file: seed values from a .tfvars file or repo-local name (repeatable)
	Vars         []string // --var: KEY=VALUE overrides applied after all var-files (repeatable)
	Yes          bool     // --yes/-y: skip the target-directory confirmation
	ListVarFiles bool     // --list-var-files: print the repo's .tfvars files and exit
	Strict       bool     // --strict: make var-file binding warnings fatal
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
		case "--strict":
			opts.Strict = true
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
	if len(files) == 0 {
		fmt.Println("No .tfvars bundles found (checked atelier.presets/ up-tree and the module repo).")
		return
	}
	for _, f := range files {
		fmt.Printf("[%s] %-24s %s\n", f.Source, f.Name, f.Display)
	}
}

// listVarFileBundles clones the module to a scratch directory, prints the
// `.tfvars` bundles discoverable locally and in the repo, and returns. It is
// the shared body of `--list-var-files` for `module add` and `import`: the
// clone is removed before returning, so nothing is written and no preflight is
// needed. Names are resolved against the same walk-up and repo locations the
// command itself would see.
func listVarFileBundles(wrapperDir, source, ref, modulePath string) error {
	ctx, cancel := interruptContext()
	defer cancel()
	files, err := bootstrap.ListVarFiles(ctx, bootstrap.InitOptions{
		WrapperDir:  wrapperDir,
		Source:      source,
		LocalSource: modulesource.IsLocal(source),
		Ref:         ref,
		ModulePath:  modulePath,
	})
	if err != nil {
		return err
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

// applyVarFlags layers `--var-file` bundles then `--var` overrides onto state
// and prints any binding warnings to stderr, in the `--var`-wins-over-
// `--var-file` order (ADR-0031). Names resolve against the cloned module repo
// or a walk-up bundle; a local path is used as-is. It does not write, so the
// caller persists with state.Write(). import uses its own value flow because
// it interleaves seeding from query variables.
func applyVarFlags(state *wrapper.State, wrapperDir, cloneDir, modulePath string, varFiles, vars []string, strict bool) error {
	if len(varFiles) > 0 {
		resolved, err := bootstrap.ResolveVarFiles(wrapperDir, cloneDir, modulePath, varFiles)
		if err != nil {
			return err
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

// runModuleAdd implements `atelier module add <url>`.
func runModuleAdd(args []string) error {
	opts, err := parseModuleArgs(args)
	if err != nil {
		return err
	}
	// `module add` writes into the current directory; only `module apply`
	// creates and moves into a directory of its own.
	if opts.Dir != "" {
		return fmt.Errorf("--dir is only valid for 'atelier module apply'; 'module add' writes to the current directory")
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
		return listVarFileBundles(cwd, opts.Source, opts.Ref, opts.ModulePath)
	}

	if _, err := tfexec.Locate(); err != nil {
		return err
	}

	// Determine if this is a fresh bootstrap or an additive operation.
	wrapperExists := mainTFExists(cwd)

	// Confirm the target directory before writing anything into it. `module
	// add` has no path argument, so the only thing standing between a
	// mistyped `cd` and a main.tf in the user's home directory is this check.
	// An established wrapper (main.tf plus .atelier/) is skipped: the user has
	// already told us this directory is a wrapper.
	//
	// This runs BEFORE the SIGINT handler below is installed, deliberately.
	// signal.NotifyContext converts Ctrl-C into a context cancellation instead
	// of terminating the process, so with the handler in place a Ctrl-C at this
	// prompt is swallowed: the read keeps waiting for a line, and answering `y`
	// afterwards proceeds with an already-cancelled context and fails with a
	// bare "context canceled". While the only thing running is a prompt, the
	// default SIGINT behaviour — exit immediately — is exactly what the user is
	// asking for.
	if !isWrapperDir(cwd) {
		action := "Bootstrap an Atelier wrapper in"
		if wrapperExists {
			action = "Add a module block to main.tf in"
		}
		ok, err := confirmTargetDir(cwd, action, opts.Yes)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}

	ctx, cancel := interruptContext()
	defer cancel()

	if !wrapperExists {
		// Fresh bootstrap of a new wrapper from the given module URL. Clone,
		// wrapper authoring and failure cleanup are shared with `import
		// --source` (bootstrapFreshWrapper) so the two stay in lockstep.
		res, cleanup, err := bootstrapFreshWrapper(cwd, cwd, opts.Source, opts.Ref, opts.ModulePath)
		if err != nil {
			return err
		}
		if res.State == nil {
			// Multiple candidates — user needs --module.
			printCandidates(os.Stdout, res.Candidates)
			return nil
		}

		// If --as was provided, rename the block the bootstrap just wrote under
		// the candidate-derived name. Re-writing the state under the new name
		// would append a second block rather than rename the first.
		if opts.As != "" {
			if err := res.State.RenameModuleBlock(sanitizeBlockName(opts.As)); err != nil {
				cleanup()
				return err
			}
		}

		// Apply --var-file values (explicit files win), then --var overrides.
		if err := applyVarFlags(res.State, cwd, res.CloneDir, res.ModulePath, opts.VarFiles, opts.Vars, opts.Strict); err != nil {
			cleanup()
			return err
		}
		if len(opts.VarFiles) > 0 || len(opts.Vars) > 0 {
			if err := res.State.Write(); err != nil {
				cleanup()
				return err
			}
		}
		return launchTUI(res, cwd)
	}

	// Wrapper already exists — additive: append a new module block.
	stop := startSpinner("Cloning and preparing module…")
	defer stop()

	// Run the same clone + candidate-discovery flow as a fresh bootstrap, so a
	// module whose Terraform lives in a subdirectory (e.g. `terraform/`) is
	// appended with the correct `//<subdir>` source and shows its variables.
	prep, err := bootstrap.PrepareModule(ctx, bootstrap.InitOptions{
		WrapperDir:  cwd,
		Source:      opts.Source,
		LocalSource: modulesource.IsLocal(opts.Source),
		Ref:         opts.Ref,
		ModulePath:  opts.ModulePath,
	})
	stop()
	if err != nil {
		return err
	}
	if prep.State == nil {
		// Multiple candidates — user needs --module. Nothing was written.
		printCandidates(os.Stdout, prep.Candidates)
		return nil
	}
	state := prep.State

	existingBlocks, _ := wrapper.ReadModuleBlocks(cwd)

	// Refuse to add a module the wrapper already has at the same ref.
	//
	// Without this, uniqueBlockName silently renamed the collision to
	// `mimir_2` and appended a second block with an identical source, so
	// running the same `module add` twice quietly declared two copies of the
	// module. Terraform accepts that config and fails much later, at apply,
	// with colliding resource names.
	//
	// This is an error rather than a prompt because there is a precise way to
	// say "yes, I really want another instance" — naming it with --as — and
	// that is better than a yes/no on an ambiguous question. --yes does not
	// bypass it: the flag means "don't ask me", not "let me build a wrapper
	// that cannot apply".
	sameModule, otherRef := findExistingInstances(existingBlocks, state.Source)

	// Determine the block name. An explicit --as that does not collide is the
	// user distinguishing this instance from the existing one, which is exactly
	// the signal needed to allow a second copy.
	blockName := state.ModuleBlockName
	namedDistinctly := false
	if opts.As != "" {
		blockName = sanitizeBlockName(opts.As)
		namedDistinctly = !blockNameTaken(blockName, existingBlocks)
	}

	if len(sameModule) > 0 && !namedDistinctly {
		return duplicateModuleError(sameModule, state.Source, opts.Source)
	}
	if len(sameModule) > 0 {
		fmt.Fprintf(os.Stderr,
			"warning: %q already references this module at the same ref; adding %q as a second instance.\n"+
				"         Both blocks must be configured so their resources do not collide.\n",
			sameModule[0].Name, blockName)
	}
	// The same module at a different ref is a supported configuration, but it is
	// worth saying out loud — an accidental re-add with a different --ref looks
	// identical to a deliberate two-revision setup.
	if len(otherRef) > 0 && len(sameModule) == 0 {
		fmt.Fprintf(os.Stderr, "note: %q already references this module at a different ref (%s).\n",
			otherRef[0].Name, otherRef[0].Source)
	}

	// Ensure uniqueness against existing blocks.
	if taken := blockNameTaken(blockName, existingBlocks); taken {
		unique := uniqueBlockName(blockName, existingBlocks)
		fmt.Fprintf(os.Stderr, "note: block name %q is taken; using %q.\n", blockName, unique)
		blockName = unique
	}
	state.ModuleBlockName = blockName

	// Apply --var-file values to the module being added, before writing it,
	// then --var overrides on top.
	if err := applyVarFlags(state, cwd, prep.CloneDir, prep.ModulePath, opts.VarFiles, opts.Vars, opts.Strict); err != nil {
		return err
	}

	// Write the new module block to main.tf.
	if err := state.Write(); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Added module %q from %s\n", blockName, opts.Source)

	// Load existing wrapper and launch TUI.
	res, err := bootstrap.LoadExisting(ctx, cwd, nil)
	if err != nil {
		return err
	}
	return launchTUI(res, cwd)
}

// runModuleApply implements `atelier module apply <url>`: scaffold a wrapper in
// a new directory, then run `terraform init` and `terraform apply` (ADR-0034).
// It saves the user from `mkdir && cd && terraform init && terraform apply`.
func runModuleApply(args []string) error {
	opts, err := parseModuleArgs(args)
	if err != nil {
		return err
	}
	// The apply approval comes from Terraform's own prompt, so it only has a
	// gate to show when stdin is a terminal. Detect that here: an
	// auto-approved apply is used otherwise (see applyWrapper), so a pipe,
	// CI, or `atelier module apply … < /dev/null` still runs unattended.
	// --yes is still rejected below: the flag means "don't prompt", and here
	// the prompt is the confirmation, so it would be a second, confusing
	// spelling of auto-approve.
	interactive := isTerminal(os.Stdin)
	if opts.Yes {
		return fmt.Errorf("--yes is not valid for 'module apply': Terraform's apply prompt is the confirmation; apply runs without one when stdin is not a terminal")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := resolveModuleSource(&opts); err != nil {
		return err
	}
	if opts.ListVarFiles {
		return listVarFileBundles(cwd, opts.Source, opts.Ref, opts.ModulePath)
	}
	if _, err := tfexec.Locate(); err != nil {
		return err
	}

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
		if err := checkApplyTarget(explicitTarget); err != nil {
			return err
		}
	}

	// Stage under the target's parent so the final move is a same-filesystem
	// rename. Staging keeps the clone to one round trip while still letting the
	// directory be named after the discovered module candidate.
	parent := cwd
	if explicitTarget != "" {
		parent = filepath.Dir(explicitTarget)
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	staging, err := os.MkdirTemp(parent, ".atelier-apply-*")
	if err != nil {
		return err
	}
	moved := false
	defer func() {
		if !moved {
			_ = os.RemoveAll(staging)
		}
	}()

	res, cleanup, err := bootstrapFreshWrapper(cwd, staging, opts.Source, opts.Ref, opts.ModulePath)
	if err != nil {
		return err
	}
	if res.State == nil {
		// Multiple candidates — user needs --module. Nothing was written.
		printCandidates(os.Stdout, res.Candidates)
		return nil
	}

	// --as renames the HCL block the bootstrap wrote under the candidate-derived
	// name. Re-writing under the new name would append a second block, so use
	// the rename primitive (which also updates State.ModuleBlockName). The
	// later Write persists any --var values on top of it.
	if opts.As != "" {
		if err := res.State.RenameModuleBlock(sanitizeBlockName(opts.As)); err != nil {
			cleanup()
			return err
		}
	}
	// Apply --var-file values, then --var overrides on top, before the wrapper
	// moves (the clone the names resolve against still lives under staging).
	if err := applyVarFlags(res.State, staging, res.CloneDir, res.ModulePath, opts.VarFiles, opts.Vars, opts.Strict); err != nil {
		cleanup()
		return err
	}
	if opts.As != "" || len(opts.VarFiles) > 0 || len(opts.Vars) > 0 {
		if err := res.State.Write(); err != nil {
			cleanup()
			return err
		}
	}

	target := explicitTarget
	if target == "" {
		name := bootstrap.ModuleDirName(res.ModulePath, modulesource.RepoBasename(opts.Source))
		target = filepath.Join(cwd, name)
		if err := checkApplyTarget(target); err != nil {
			cleanup()
			return err
		}
	}

	// Move the staged wrapper into place. An existing empty directory is
	// reused; a non-empty one was refused by checkApplyTarget.
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		if err := os.Remove(target); err != nil {
			cleanup()
			return err
		}
	}
	if err := os.Rename(staging, target); err != nil {
		cleanup()
		return err
	}
	moved = true

	// Required variables with no value would make Terraform reject the apply.
	// The wrapper is already in place, so point at the flag that fixes it.
	if missing := unsetRequiredVars(res.State); len(missing) > 0 {
		return fmt.Errorf("module requires a value for %s; pass --var NAME=VALUE (or --var-file) and re-run.\nWrapper written to %s",
			strings.Join(missing, ", "), target)
	}

	fmt.Fprintf(os.Stderr, "Wrapper written to %s\n", target)
	return applyWrapper(target, !interactive)
}

// checkApplyTarget refuses a target directory that already holds files.
// `module apply` creates a fresh wrapper; a non-empty directory is almost
// always a mistyped --dir or --as, so it is refused rather than scaffolded
// over. An existing empty directory is allowed and reused.
func checkApplyTarget(target string) error {
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
		return fmt.Errorf("target directory %s already exists and is not empty; choose another name with --dir or --as", target)
	}
	return nil
}

// unsetRequiredVars lists required module variables (declared without a
// default) that have no value. `module apply` checks this before running
// Terraform, which would otherwise reject the run with a less direct message.
func unsetRequiredVars(state *wrapper.State) []string {
	var missing []string
	for _, v := range state.Vars {
		if !v.VarIsRequired() {
			continue
		}
		val, ok := state.Values[v.Name]
		if !ok || val == cty.NilVal || val.IsNull() {
			missing = append(missing, v.Name)
		}
	}
	return missing
}

// applyWrapper runs `terraform init` then `terraform apply` in dir. When there
// is a terminal, apply is Terraform's interactive prompt (autoApprove=false);
// otherwise it is auto-approved (autoApprove=true) so a pipe or CI run does not
// hang on a question nobody can answer. See ADR-0034.
func applyWrapper(dir string, autoApprove bool) error {
	ctx, cancel := interruptContext()
	defer cancel()

	tf, err := tfexec.New(dir, "")
	if err != nil {
		return err
	}

	// init is non-interactive; stream its progress so module and provider
	// fetches are visible. Clear the writers before apply, which attaches the
	// terminal itself when interactive.
	stop := startSpinner("Running terraform init…")
	tf.SetStdout(os.Stdout)
	tf.SetStderr(os.Stderr)
	err = tf.Init(ctx)
	stop()
	tf.SetStdout(nil)
	tf.SetStderr(nil)
	if err != nil {
		return fmt.Errorf("terraform init: %w", err)
	}

	if autoApprove {
		fmt.Fprintln(os.Stderr, "no terminal on stdin: applying without the plan prompt.")
	}
	return tf.ApplyDirect(ctx, autoApprove)
}

// bootstrapFreshWrapper clones a remote module source and writes a wrapper
// into dir, printing a spinner and any bootstrap warnings. The clone, authoring
// and transactional cleanup are bootstrap.FreshWrapper, so `module add` and
// `import --source` cannot drift.
//
// It returns a result with a nil State when the module has multiple Terraform
// candidates (nothing was written); the caller decides how to present the
// candidate list — `module add` prints it and exits 0, `import --source`
// prints it and errors. The returned cleanup closure removes an .atelier/
// this run created; callers invoke it on their own post-bootstrap failure
// paths.
//
// sourceBaseDir resolves a relative local `source` path (`.`.`/…`); it defaults
// to dir, which is right when the wrapper is written where the user invoked the
// command. `module apply` stages the wrapper elsewhere and passes the
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

// runModuleRm implements `atelier module rm <name>`.
func runModuleRm(args []string) error {
	var force bool
	var name string
	for _, a := range args {
		if a == "--force" || a == "-f" || a == "--yes" || a == "-y" {
			force = true
		} else if strings.HasPrefix(a, "-") {
			return fmt.Errorf("unknown flag %q for module rm", a)
		} else {
			if name != "" {
				return fmt.Errorf("module rm takes exactly one module name")
			}
			name = a
		}
	}
	if name == "" {
		return fmt.Errorf("module rm requires a module name. Use 'atelier module list' to see modules")
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
		return fmt.Errorf("no module %q found in main.tf. Use 'atelier module list' to see modules", name)
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

// runModuleList implements `atelier module list`.
func runModuleList(args []string) error {
	for _, a := range args {
		if strings.HasPrefix(a, "-") && a != "--help" && a != "-h" {
			return fmt.Errorf("unknown flag %q for module list", a)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	blocks, err := wrapper.ReadModuleBlocks(cwd)
	if err != nil {
		return fmt.Errorf("reading main.tf: %w", err)
	}
	if len(blocks) == 0 {
		fmt.Println("No modules found in this wrapper.")
		return nil
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

// sameModule reports whether two identities name the same module at the same
// revision.
func (a moduleSourceIdentity) sameModule(b moduleSourceIdentity) bool {
	return a.remote == b.remote && a.path == b.path && a.ref == b.ref
}

// sameModuleDifferentRef reports whether two identities name the same module in
// the same repository sub-directory, but pinned at different refs. That is a
// supported configuration — two blocks of one module at two revisions — so it is
// reported rather than refused.
func (a moduleSourceIdentity) sameModuleDifferentRef(b moduleSourceIdentity) bool {
	return a.remote == b.remote && a.path == b.path && a.ref != b.ref
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
		case want.sameModuleDifferentRef(got):
			otherRef = append(otherRef, blk)
		}
	}
	return same, otherRef
}

// duplicateModuleError explains why an add was refused and how to get what the
// user probably wanted.
func duplicateModuleError(dups []wrapper.ModuleBlockInfo, source, sourceArg string) error {
	names := make([]string, len(dups))
	for i, blk := range dups {
		names[i] = fmt.Sprintf("%q", blk.Name)
	}
	subject := "module " + names[0]
	if len(names) > 1 {
		subject = "modules " + strings.Join(names, ", ")
	}
	return fmt.Errorf(`%s already references this module at the same ref:
  %s

Adding it again would declare a second copy of the same resources, which
Terraform will try to create alongside the first — usually failing at apply
with name collisions rather than here.

  configure the existing one:  atelier
  add a genuinely separate instance:
                               atelier module add %s --as <name>
  add it at a different revision:
                               atelier module add %s --ref <ref>`,
		subject, source, sourceArg, sourceArg)
}
