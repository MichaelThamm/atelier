package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/MichaelThamm/atelier/internal/bootstrap"
	"github.com/MichaelThamm/atelier/internal/tfexec"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

const moduleUsage = `Usage:
  atelier module add <git-url> [--as NAME] [--ref REF] [--module SUBDIR]
                                [--var-file PATH|NAME] [--list-var-files] [--strict] [--tfvars] [--yes]
                                               Add a module to the wrapper.
                                               --var-file seeds values from a Terraform variable file: a local
                                               path, a name in an ancestor atelier.presets/ directory (walk-up),
                                               or a name committed to the module repo. Comma-separate or repeat
                                               for several (e.g. --var-file cos-s3,cos-units); later files win.
                                               --list-var-files prints the .tfvars files found in the module repo.
                                               --strict makes var-file binding warnings (unknown variables,
                                               type mismatches) fatal instead of warnings.
                                               --tfvars writes an opt-in pass-through wrapper: a mirrored
                                               variables.tf + forwarding main.tf, with values in terraform.tfvars.
  atelier module rm <name> [--force]           Remove a module from the wrapper.
  atelier module list                          List modules in the wrapper.
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
	case "rm", "remove":
		return runModuleRm(args[1:])
	case "list", "ls":
		return runModuleList(args[1:])
	default:
		return fmt.Errorf("unknown module subcommand %q\n\n%s", args[0], moduleUsage)
	}
}

// moduleAddOpts holds parsed flags for `atelier module add`.
type moduleAddOpts struct {
	Source       string   // positional git URL
	As           string   // --as: explicit HCL block name
	Ref          string   // --ref: git ref
	ModulePath   string   // --module: candidate subdir
	VarFiles     []string // --var-file: seed values from a .tfvars file or repo-local name (repeatable)
	Yes          bool     // --yes/-y: skip the target-directory confirmation
	TFVars       bool     // --tfvars: opt-in pass-through wrapper shape (ADR-0031)
	ListVarFiles bool     // --list-var-files: print the repo's .tfvars files and exit
	Strict       bool     // --strict: make var-file binding warnings fatal
}

func parseModuleAddArgs(args []string) (moduleAddOpts, error) {
	var opts moduleAddOpts
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--yes", "-y":
			opts.Yes = true
		case "--tfvars":
			opts.TFVars = true
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
		case "--var-file":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--var-file requires a path")
			}
			opts.VarFiles = appendVarFileList(opts.VarFiles, args[i])
		default:
			if strings.HasPrefix(a, "--var-file=") {
				opts.VarFiles = appendVarFileList(opts.VarFiles, strings.TrimPrefix(a, "--var-file="))
				continue
			}
			if strings.HasPrefix(a, "-") {
				return opts, fmt.Errorf("unknown flag %q for module add", a)
			}
			positional = append(positional, a)
		}
	}
	if len(positional) == 0 {
		return opts, fmt.Errorf("module add requires a git URL argument")
	}
	if len(positional) > 1 {
		return opts, fmt.Errorf("module add takes exactly one URL argument; got %v", positional)
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

// runModuleAdd implements `atelier module add <url>`.
func runModuleAdd(args []string) error {
	opts, err := parseModuleAddArgs(args)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := tfexec.Locate(); err != nil {
		return err
	}

	// --list-var-files clones the module (without writing a wrapper) and
	// prints the `.tfvars` files committed to the repository, then exits
	// (ADR-0032). It needs no preflight because nothing is written.
	if opts.ListVarFiles {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		prep, err := bootstrap.PrepareModule(ctx, bootstrap.InitOptions{
			WrapperDir: cwd,
			Source:     opts.Source,
			Ref:        opts.Ref,
			ModulePath: opts.ModulePath,
		})
		if err != nil {
			return err
		}
		files := bootstrap.ListAllVarFiles(cwd, prep.CloneDir, prep.ModulePath)
		if len(files) == 0 {
			fmt.Println("No .tfvars bundles found (checked atelier.presets/ up-tree and the module repo).")
			return nil
		}
		for _, f := range files {
			fmt.Printf("[%s] %-24s %s\n", f.Source, f.Name, f.Display)
		}
		return nil
	}

	// Determine if this is a fresh bootstrap or an additive operation.
	mainPath := filepath.Join(cwd, wrapper.MainTF)
	wrapperExists := false
	if _, err := os.Stat(mainPath); err == nil {
		wrapperExists = true
	}

	// --tfvars selects a whole-wrapper shape. The additive path appends a
	// second module block to an existing main.tf, which cannot be reconciled
	// with a generated pass-through interface (that is the multi-module
	// namespacing problem ADR-0031 defers), so refuse rather than half-apply.
	if wrapperExists && opts.TFVars {
		return fmt.Errorf("--tfvars applies to a fresh wrapper; this directory already has a main.tf.\n" +
			"       tfvars mode is single-module for now — bootstrap it in an empty directory")
	}

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

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if !wrapperExists {
		// Fresh bootstrap of a new wrapper from the given module URL. Clone,
		// wrapper authoring and failure cleanup are shared with `import
		// --source` (bootstrapFreshWrapper) so the two stay in lockstep.
		res, cleanup, err := bootstrapFreshWrapper(cwd, opts.Source, opts.Ref, opts.ModulePath, opts.TFVars)
		if err != nil {
			return err
		}
		if res.State == nil {
			// Multiple candidates — user needs --module.
			fmt.Println("Multiple module candidates found. Re-run with --module <path>:")
			for _, c := range res.Candidates {
				label := c.Path
				if c.Name != "" {
					label = fmt.Sprintf("%s — %s", c.Path, c.Name)
				}
				fmt.Println("  " + label)
			}
			return nil
		}

		// If --as was provided, rename the module block.
		if opts.As != "" {
			res.State.ModuleBlockName = sanitizeBlockName(opts.As)
			if err := res.State.Write(); err != nil {
				cleanup()
				return err
			}
		}

		// Apply --var-file values (explicit files win), then persist. A name
		// is resolved against the cloned module repo; a local path is used
		// as-is. In TFVarsMode this lands in terraform.tfvars; in the classic
		// shape it becomes module arguments.
		if len(opts.VarFiles) > 0 {
			resolved, rerr := bootstrap.ResolveVarFiles(cwd, res.CloneDir, res.ModulePath, opts.VarFiles)
			if rerr != nil {
				cleanup()
				return rerr
			}
			warns, aerr := applyVarFiles(res.State, resolved, opts.Strict)
			if aerr != nil {
				cleanup()
				return aerr
			}
			for _, w := range warns {
				fmt.Fprintln(os.Stderr, "warning:", w)
			}
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
	// appended with the correct `//<subdir>` source and thus shows its
	// variables in the TUI. Skipping discovery here previously appended such
	// modules at the repo root, leaving them with no editable variables.
	prep, err := bootstrap.PrepareModule(ctx, bootstrap.InitOptions{
		WrapperDir: cwd,
		Source:     opts.Source,
		Ref:        opts.Ref,
		ModulePath: opts.ModulePath,
	})
	stop()
	if err != nil {
		return err
	}
	if prep.State == nil {
		// Multiple candidates — user needs --module. Nothing was written.
		fmt.Println("Multiple module candidates found. Re-run with --module <path>:")
		for _, c := range prep.Candidates {
			label := c.Path
			if c.Name != "" {
				label = fmt.Sprintf("%s — %s", c.Path, c.Name)
			}
			fmt.Println("  " + label)
		}
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

	// Apply --var-file values to the module being added, before writing it.
	if len(opts.VarFiles) > 0 {
		resolved, rerr := bootstrap.ResolveVarFiles(cwd, prep.CloneDir, prep.ModulePath, opts.VarFiles)
		if rerr != nil {
			return rerr
		}
		warns, aerr := applyVarFiles(state, resolved, opts.Strict)
		if aerr != nil {
			return aerr
		}
		for _, w := range warns {
			fmt.Fprintln(os.Stderr, "warning:", w)
		}
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

// bootstrapFreshWrapper clones a remote module source and writes a wrapper
// into dir: the single fresh-bootstrap path shared by `module add` (into the
// working directory) and `import --source` (into the import target). Keeping
// one implementation means the two cannot drift — `import --source` offers
// exactly the same clone, failure cleanup and warnings as `module add`.
//
// It returns a result with a nil State when the module has multiple Terraform
// candidates (nothing was written); the caller decides how to present the
// candidate list — `module add` prints it and exits 0, `import --source`
// prints it and errors.
//
// A bootstrap that fails partway must not leave a clone behind in a directory
// that never became a wrapper — but only an .atelier/ this run created is
// removed, so a retry inside an established wrapper never destroys its state.
// The returned cleanup closure does that removal; callers invoke it on their
// own post-bootstrap failure paths (e.g. a rename or preset-write error),
// matching `module add`'s previous behaviour.
func bootstrapFreshWrapper(dir, source, ref, modulePath string, tfVars bool) (*bootstrap.Result, func(), error) {
	if _, err := tfexec.Locate(); err != nil {
		return nil, nil, err
	}

	atelierDir := filepath.Join(dir, wrapper.AtelierDir)
	createdAtelierDir := false
	if _, err := os.Stat(atelierDir); os.IsNotExist(err) {
		createdAtelierDir = true
	}
	cleanup := func() {
		if createdAtelierDir {
			_ = os.RemoveAll(atelierDir)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	stop := startSpinner("Cloning and preparing module…")
	defer stop()
	res, err := bootstrap.InitNew(ctx, bootstrap.InitOptions{
		WrapperDir: dir,
		Source:     source,
		Ref:        ref,
		ModulePath: modulePath,
		TFVars:     tfVars,
	})
	stop()
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if res.State == nil {
		// Multiple candidates — nothing written. The caller presents them.
		cleanup()
		return res, nil, nil
	}

	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	return res, cleanup, nil
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
		src, ref := decomposeModuleSource(blk.Source)
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
	remote, ref := decomposeModuleSource(source)
	return moduleSourceIdentity{
		remote: normaliseRemote(remote),
		path:   strings.Trim(modulePathFromSource(source), "/"),
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
