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
  atelier module add <git-url> [--as NAME] [--ref REF] [--module SUBDIR] [--preset NAME] [--yes]
                                               Add a module to the wrapper.
                                               --preset applies a named preset from atelier.local.yaml.
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
	Source     string   // positional git URL
	As         string   // --as: explicit HCL block name
	Ref        string   // --ref: git ref
	ModulePath string   // --module: candidate subdir
	Presets    []string // --preset: named preset from atelier.local.yaml
	Yes        bool     // --yes/-y: skip the target-directory confirmation
}

func parseModuleAddArgs(args []string) (moduleAddOpts, error) {
	var opts moduleAddOpts
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--yes", "-y":
			opts.Yes = true
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
		case "--preset":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--preset requires a name")
			}
			opts.Presets = append(opts.Presets, args[i])
		default:
			if strings.HasPrefix(a, "--preset=") {
				opts.Presets = append(opts.Presets, strings.TrimPrefix(a, "--preset="))
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

	// Determine if this is a fresh bootstrap or an additive operation.
	mainPath := filepath.Join(cwd, wrapper.MainTF)
	wrapperExists := false
	if _, err := os.Stat(mainPath); err == nil {
		wrapperExists = true
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
		// Fresh bootstrap of a new wrapper from the given module URL.
		initOpts := bootstrap.InitOptions{
			WrapperDir: cwd,
			Source:     opts.Source,
			Ref:        opts.Ref,
			ModulePath: opts.ModulePath,
		}

		// A bootstrap that fails partway leaves a clone under .atelier/ in a
		// directory the user may not have wanted touched at all. Remove it —
		// but only if this run is what created it, so a retry in a wrapper
		// that already had state doesn't destroy that state.
		atelierDir := filepath.Join(cwd, wrapper.AtelierDir)
		createdAtelierDir := false
		if _, err := os.Stat(atelierDir); os.IsNotExist(err) {
			createdAtelierDir = true
		}
		cleanup := func() {
			if createdAtelierDir {
				_ = os.RemoveAll(atelierDir)
			}
		}

		stop := startSpinner("Cloning and preparing module…")
		defer stop()
		res, err := bootstrap.InitNew(ctx, initOpts)
		stop()
		if err != nil {
			cleanup()
			return err
		}
		if res.State == nil {
			// Multiple candidates — user needs --module.
			cleanup()
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

		for _, w := range res.Warnings {
			fmt.Fprintln(os.Stderr, "warning:", w)
		}

		// Apply --preset values, then persist them to main.tf.
		if len(opts.Presets) > 0 {
			if err := applyPresets(cwd, res.State, opts.Presets); err != nil {
				cleanup()
				return err
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

	// Apply --preset values to the module being added, before writing it.
	if len(opts.Presets) > 0 {
		if err := applyPresets(cwd, state, opts.Presets); err != nil {
			return err
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
