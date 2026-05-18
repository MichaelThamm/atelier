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
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/bootstrap"
	"github.com/MichaelThamm/atelier/internal/manifest"
	"github.com/MichaelThamm/atelier/internal/session"
	"github.com/MichaelThamm/atelier/internal/tfexec"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/tui"
	"github.com/MichaelThamm/atelier/internal/wrapper"
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

	stop := startSpinner("Cloning and preparing module…")
	res, err := bootstrap.InitNew(ctx, opts)
	stop()
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

	// Load manifest presets for the left pane.
	var presets []tui.ResolvedPreset
	if man, _, _ := manifest.LoadFromRepo(filepath.Join(wrapperDir, ".atelier", "clone")); man != nil {
		modPath := modulePathFromState(state)
		mod := man.FindModule(modPath)
		if mod != nil && len(mod.Presets) > 0 {
			presets = tui.ResolvePresets(mod.Presets, state.Vars)
		}
	}

	m := tui.New(state, state.ModuleBlockName)
	m.LiteralRef = res.LiteralRef
	m.ResolvedSHA = res.ResolvedSHA
	m.SourceURL = sourceURLFromState(state)
	m.SetPresets(presets)

	// Construct a Planner so pressing P in the TUI runs a real terraform
	// plan against the wrapper. A failure to locate terraform was already
	// reported in runOpen / runInit, so this should not error in practice;
	// if it does, we leave Planner nil and the TUI surfaces a clear status
	// message instead of crashing.
	if tf, err := tfexec.New(wrapperDir, ""); err == nil {
		tp := &tui.TfexecPlanner{Tf: tf, WrapperDir: wrapperDir}
		m.Planner = tp
		m.Applier = tp
		m.Validator = tp
		m.OutputProvider = tp
	} else {
		fmt.Fprintln(os.Stderr, "warning: planner unavailable:", err)
	}

	// Construct a RefSwitcher for non-local-source wrappers. Local sources
	// (--source path) don't have a git remote to switch refs on.
	if res.LiteralRef != "" || res.ResolvedSHA != "" {
		m.RefSwitcher = &prodRefSwitcher{
			wrapperDir:    wrapperDir,
			sourceURL:     sourceURLFromState(state),
			modulePath:    modulePathFromState(state),
			currentVars:   state.Vars,
			currentValues: state.Values,
		}
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

// sourceURLFromState extracts the git remote URL from the state's Source,
// stripping the git:: prefix, module path suffix, and ?ref= query.
func sourceURLFromState(s *wrapper.State) string {
	src := s.Source
	src = strings.TrimPrefix(src, "git::")
	// Strip ?ref= query first (it's always at the end).
	if idx := strings.Index(src, "?ref="); idx >= 0 {
		src = src[:idx]
	}
	// Strip module sub-path indicated by "//" after the host/repo portion.
	// Skip past the scheme's "://" to avoid matching it.
	searchFrom := 0
	if schemeEnd := strings.Index(src, "://"); schemeEnd >= 0 {
		searchFrom = schemeEnd + 3
	}
	if idx := strings.Index(src[searchFrom:], "//"); idx >= 0 {
		src = src[:searchFrom+idx]
	}
	return src
}

// prodRefSwitcher implements tui.RefSwitcher by re-cloning the module at a
// new ref, re-parsing variables, and running terraform init -upgrade.
type prodRefSwitcher struct {
	wrapperDir    string
	sourceURL     string
	modulePath    string
	currentVars   []tfvars.Variable
	currentValues map[string]cty.Value
}

func (s *prodRefSwitcher) SwitchRef(ctx context.Context, newRef string) (*tui.RefSwitchResult, error) {
	// Re-clone at the new ref.
	cloneDir, sha, err := bootstrap.ResolveAndClone(ctx, bootstrap.InitOptions{
		WrapperDir: s.wrapperDir,
		Source:     s.sourceURL,
		Ref:        newRef,
	})
	if err != nil {
		return nil, err
	}

	// Re-parse variables from the new clone.
	state, err := bootstrap.PrepareState(s.wrapperDir, cloneDir, s.modulePath, sha, newRef, s.sourceURL)
	if err != nil {
		return nil, err
	}

	// Write the wrapper main.tf with the new source (ref) before running init,
	// so Terraform sees the updated module source during initialisation.
	// Carry over existing user values for variables that still exist in the
	// new ref — required variables must be present in the HCL for init to
	// succeed.
	if state.Values == nil {
		state.Values = make(map[string]cty.Value)
	}
	newVarNames := make(map[string]bool, len(state.Vars))
	for _, v := range state.Vars {
		newVarNames[v.Name] = true
	}
	for name, val := range s.currentValues {
		if newVarNames[name] {
			state.Values[name] = val
		}
	}
	if err := state.Write(); err != nil {
		return nil, fmt.Errorf("write wrapper: %w", err)
	}

	// Run terraform init -upgrade so Terraform fetches the new module revision.
	tf, err := tfexec.New(s.wrapperDir, "")
	if err != nil {
		return nil, fmt.Errorf("terraform init -upgrade: %w", err)
	}
	if err := tf.InitUpgrade(ctx); err != nil {
		return nil, fmt.Errorf("terraform init -upgrade: %w", err)
	}

	// Determine orphaned variables (user had values but no longer in module).
	oldVarNames := make(map[string]bool, len(s.currentVars))
	for _, v := range s.currentVars {
		oldVarNames[v.Name] = true
	}

	var orphaned []string
	for _, v := range s.currentVars {
		if !newVarNames[v.Name] {
			orphaned = append(orphaned, v.Name)
		}
	}
	var newVars []tfvars.Variable
	for _, v := range state.Vars {
		if !oldVarNames[v.Name] {
			newVars = append(newVars, v)
		}
	}

	// Update the switcher's current vars and values for future switches.
	s.currentVars = state.Vars
	s.currentValues = state.Values

	// Save session with new ref.
	if err := session.Save(s.wrapperDir, &session.Session{
		SourceURL:           s.sourceURL,
		LiteralRef:          newRef,
		ResolvedSHA:         sha,
		ModuleCandidatePath: s.modulePath,
		ModuleBlockName:     state.ModuleBlockName,
		LastOpened:          time.Now().UTC(),
	}); err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}

	return &tui.RefSwitchResult{
		State:        state,
		ResolvedSHA:  sha,
		LiteralRef:   newRef,
		OrphanedVars: orphaned,
		NewVars:      newVars,
	}, nil
}

// startSpinner launches a background goroutine that prints a braille spinner
// animation to stderr. It returns a stop function that clears the spinner
// line and waits for the goroutine to exit.
func startSpinner(msg string) func() {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	var once sync.Once
	done := make(chan struct{})

	go func() {
		i := 0
		for {
			select {
			case <-done:
				// Clear the spinner line.
				fmt.Fprintf(os.Stderr, "\r\033[K")
				return
			default:
				fmt.Fprintf(os.Stderr, "\r%s %s", frames[i%len(frames)], msg)
				i++
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()

	return func() {
		once.Do(func() {
			close(done)
			// Give the goroutine a moment to clear the line.
			time.Sleep(20 * time.Millisecond)
		})
	}
}
