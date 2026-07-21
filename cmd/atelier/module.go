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
                                               Add a module to the wrapper.
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
	Source     string // positional git URL
	As         string // --as: explicit HCL block name
	Ref        string // --ref: git ref
	ModulePath string // --module: candidate subdir
}

func parseModuleAddArgs(args []string) (moduleAddOpts, error) {
	var opts moduleAddOpts
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
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
		default:
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

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Determine if this is a fresh bootstrap or an additive operation.
	mainPath := filepath.Join(cwd, wrapper.MainTF)
	wrapperExists := false
	if _, err := os.Stat(mainPath); err == nil {
		wrapperExists = true
	}

	if !wrapperExists {
		// Fresh bootstrap of a new wrapper from the given module URL.
		initOpts := bootstrap.InitOptions{
			WrapperDir: cwd,
			Source:     opts.Source,
			Ref:        opts.Ref,
			ModulePath: opts.ModulePath,
		}

		stop := startSpinner("Cloning and preparing module…")
		defer stop()
		res, err := bootstrap.InitNew(ctx, initOpts)
		stop()
		if err != nil {
			return err
		}
		if res.State == nil {
			// Multiple candidates — user needs --module.
			_ = os.RemoveAll(filepath.Join(cwd, wrapper.AtelierDir))
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
				return err
			}
		}

		for _, w := range res.Warnings {
			fmt.Fprintln(os.Stderr, "warning:", w)
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

	// Determine the block name.
	blockName := state.ModuleBlockName
	if opts.As != "" {
		blockName = sanitizeBlockName(opts.As)
	}

	// Ensure uniqueness against existing blocks.
	existingBlocks, _ := wrapper.ReadModuleBlocks(cwd)
	blockName = uniqueBlockName(blockName, existingBlocks)
	state.ModuleBlockName = blockName

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
		if a == "--force" || a == "-f" {
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
		fmt.Fprintf(os.Stderr, "Remove module %q from the wrapper? This removes the module block from main.tf.\n", name)
		fmt.Fprintf(os.Stderr, "Note: existing Terraform state for this module is NOT destroyed. Run 'terraform destroy -target=module.%s' first if needed.\n", name)
		fmt.Fprint(os.Stderr, "Proceed? [y/N] ")
		var answer string
		fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" {
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
