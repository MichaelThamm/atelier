// Command atelier is the entry point to Atelier's CLI.
//
// Surface (SPEC §6):
//
//	atelier                                     open the wrapper in CWD
//	atelier init <git-url> [--ref R] [--module M]
//	atelier init --source <path> [--module M]
//
// All operation runs against the current working directory. The CLI defers
// the heavy lifting (clone, candidate discovery, wrapper write, TUI loop) to
// the internal/bootstrap and internal/tui packages.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/canonical/atelier/internal/bootstrap"
	"github.com/canonical/atelier/internal/manifest"
	"github.com/canonical/atelier/internal/tfexec"
	"github.com/canonical/atelier/internal/tui"
	"github.com/canonical/atelier/internal/wrapper"
)

const usage = `Atelier — a terminal UI for configuring Terraform modules.

Usage:
  atelier                                      Open the wrapper in the current directory.
  atelier init <git-url> [--ref REF] [--module SUBDIR]
                                               Bootstrap a wrapper from a git URL.
  atelier init --source PATH [--module SUBDIR]
                                               Bootstrap from a local module directory.
  atelier --help                               Print this help.

The wrapper is the durable artifact: a normal Terraform project Atelier
writes into the current directory. Atelier does not run 'terraform apply';
the user does that themselves from the wrapper using their existing workflow.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "atelier:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Print(usage)
			return nil
		}
	}

	if len(args) == 0 {
		return runOpen()
	}
	if args[0] == "init" {
		return runInit(args[1:])
	}
	return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
}

// runOpen implements `atelier` (no args): open the wrapper in CWD.
func runOpen() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	mainPath := filepath.Join(cwd, wrapper.MainTF)
	if _, err := os.Stat(mainPath); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("not a wrapper directory. Run 'atelier init <source>' to bootstrap")
	} else if err != nil {
		return err
	}
	if _, err := tfexec.Locate(); err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	res, err := bootstrap.LoadExisting(ctx, cwd, nil)
	if err != nil {
		return err
	}
	return launchTUI(res, cwd)
}

// runInit implements `atelier init …`.
func runInit(args []string) error {
	opts, err := parseInitArgs(args)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	opts.WrapperDir = cwd

	// SPEC §6.1: error if main.tf already exists.
	if _, err := os.Stat(filepath.Join(cwd, wrapper.MainTF)); err == nil {
		return fmt.Errorf("wrapper exists. Use 'atelier' to open, or remove main.tf to re-init")
	}
	if _, err := tfexec.Locate(); err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	res, err := bootstrap.InitNew(ctx, opts)
	if err != nil {
		return err
	}
	if res.State == nil {
		// Multiple candidates and no --module supplied. Print the list.
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
	for _, w := range res.ManifestWarnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	return launchTUI(res, cwd)
}

func parseInitArgs(args []string) (bootstrap.InitOptions, error) {
	opts := bootstrap.InitOptions{}
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--source":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--source requires a path")
			}
			opts.LocalSource = true
			opts.Source = args[i]
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
			positional = append(positional, a)
		}
	}
	if len(positional) > 0 {
		if opts.Source != "" {
			return opts, fmt.Errorf("cannot combine positional URL with --source")
		}
		if len(positional) > 1 {
			return opts, fmt.Errorf("init takes exactly one URL argument; got %v", positional)
		}
		opts.Source = positional[0]
	}
	if opts.Source == "" {
		return opts, fmt.Errorf("init requires a git URL or --source <path>")
	}
	return opts, nil
}

func launchTUI(res *bootstrap.Result, wrapperDir string) error {
	state := res.State

	// Load manifest groupings and presets for the left pane.
	var groups []manifest.ResolvedGroup
	var presets []tui.ResolvedPreset
	if man, _, _ := manifest.LoadFromRepo(filepath.Join(wrapperDir, ".atelier", "clone")); man != nil {
		modPath := modulePathFromState(state)
		mod := man.FindModule(modPath)
		names := make([]string, len(state.Vars))
		for i, v := range state.Vars {
			names[i] = v.Name
		}
		groups = manifest.ApplyGroups(mod, names)
		if mod != nil && len(mod.Presets) > 0 {
			presets = tui.ResolvePresets(mod.Presets, state.Vars)
		}
	}

	m := tui.New(state, state.ModuleBlockName)
	m.LiteralRef = res.LiteralRef
	m.ResolvedSHA = res.ResolvedSHA
	m.SetGroups(groups)
	m.SetPresets(presets)

	// Construct a Planner so pressing P in the TUI runs a real terraform
	// plan against the wrapper. A failure to locate terraform was already
	// reported in runOpen / runInit, so this should not error in practice;
	// if it does, we leave Planner nil and the TUI surfaces a clear status
	// message instead of crashing.
	if tf, err := tfexec.New(wrapperDir, ""); err == nil {
		m.Planner = &tui.TfexecPlanner{Tf: tf, WrapperDir: wrapperDir}
	} else {
		fmt.Fprintln(os.Stderr, "warning: planner unavailable:", err)
	}

	prog := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		return err
	}
	// Flush any pending state to disk before exit.
	if err := m.SaveIfDirty(); err != nil {
		return err
	}
	return nil
}

func modulePathFromState(s *wrapper.State) string {
	// Extract the module sub-path from the source attribute. Terraform git
	// sources use "//" to separate the repo URL from the sub-directory, e.g.
	// "git::https://host/repo.git//terraform/cos-lite?ref=main"
	src := s.Source
	idx := strings.LastIndex(src, "//")
	if idx < 0 {
		return ""
	}
	sub := src[idx+2:]
	// Strip ?ref=... query suffix if present.
	if q := strings.IndexByte(sub, '?'); q >= 0 {
		sub = sub[:q]
	}
	return sub
}
