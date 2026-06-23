// Command atelier is the entry point to Atelier's CLI.
//
// Surface (SPEC §6):
//
//	atelier                                     open the wrapper in CWD
//	atelier init <git-url> [--ref R] [--module M]
//	atelier init --source <path> [--module M]
//	atelier init                                adopt existing project in CWD
//	atelier init --module-dir <name>            adopt with custom subdir name
//	atelier tidy [PATH] [--write]               prune arguments left at their default
//	atelier purge [PATH] [--force]              remove .atelier/ and .clone/
//
// All operation runs against the current working directory. The CLI defers
// the heavy lifting (clone, candidate discovery, wrapper write, TUI loop) to
// the internal/bootstrap, internal/convert, and internal/tui packages.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/bootstrap"
	"github.com/MichaelThamm/atelier/internal/convert"
	"github.com/MichaelThamm/atelier/internal/gitops"
	"github.com/MichaelThamm/atelier/internal/manifest"
	"github.com/MichaelThamm/atelier/internal/session"
	tfstate "github.com/MichaelThamm/atelier/internal/state"
	"github.com/MichaelThamm/atelier/internal/tfexec"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/tui"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

const usage = `Atelier — a terminal UI for configuring Terraform modules.

Usage:
  atelier                                      Open the wrapper in the current directory.
  atelier module add <git-url> [--as NAME] [--ref REF] [--module SUBDIR]
                                               Add a module to the wrapper (bootstraps if needed).
  atelier module rm <name> [--force]           Remove a module from the wrapper.
  atelier module list                          List modules in the wrapper.
  atelier init [--module-dir NAME]             Adopt an existing Terraform project in-place.
  atelier init --source PATH [--module SUBDIR]
                                               Bootstrap from a local module directory.
  atelier purge [PATH] [--force]               Remove .atelier/ and .clone/ from a directory.
  atelier tidy [PATH] [--write]                Prune module arguments left at their default value.
                                               Dry-run by default; --write applies it (backs up main.tf first).
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
	// Only check top-level --help (not within subcommands).
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Print(usage)
		return nil
	}

	if len(args) == 0 {
		return runOpen()
	}
	if args[0] == "module" {
		return runModule(args[1:])
	}
	if args[0] == "init" {
		return runInit(args[1:])
	}
	if args[0] == "purge" {
		return runPurge(args[1:])
	}
	if args[0] == "tidy" {
		return runTidy(args[1:])
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
		return fmt.Errorf("not a wrapper directory. Run 'atelier module add <url>' to bootstrap")
	} else if err != nil {
		return err
	}
	if _, err := tfexec.Locate(); err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	// LoadExisting may re-resolve and (cold) re-clone the primary module before
	// the alt-screen comes up. Show a spinner labelled with the total module
	// count so it matches what the user sees in the TUI (the secondary-load
	// phase below reuses the same label, so it reads as one continuous step).
	stop := startSpinner(loadingMessage(cwd))
	res, err := bootstrap.LoadExisting(ctx, cwd, nil)
	stop()
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

	if _, err := tfexec.Locate(); err != nil {
		return err
	}

	// No source provided — adopt/convert an existing project in CWD.
	if opts.Source == "" {
		return runInitAdopt(cwd, opts.ModuleDir)
	}

	// If the source is a git URL (not local), redirect to `atelier module add`.
	if !opts.LocalSource {
		return fmt.Errorf("'atelier init <url>' is removed. Use 'atelier module add %s' instead", opts.Source)
	}

	// Local source provided — bootstrap a new wrapper from local path.
	opts.WrapperDir = cwd

	// Error if .atelier/ already exists (already initialized).
	if _, err := os.Stat(filepath.Join(cwd, wrapper.AtelierDir)); err == nil {
		return fmt.Errorf("already initialized. Use 'atelier' to open")
	}
	// SPEC §6.1: error if main.tf already exists.
	if _, err := os.Stat(filepath.Join(cwd, wrapper.MainTF)); err == nil {
		return fmt.Errorf("wrapper exists. Use 'atelier' to open, or remove main.tf to re-init")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	stop := startSpinner("Cloning and preparing module…")
	defer stop()
	res, err := bootstrap.InitNew(ctx, opts.InitOptions)
	stop()
	if err != nil {
		return err
	}
	if res.State == nil {
		// Multiple candidates and no --module supplied. Print the list.
		// Clean up the .atelier/ directory created during clone so
		// the user can re-run with --module without "already initialized".
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
	for _, w := range res.ManifestWarnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	return launchTUI(res, cwd)
}

// runInitAdopt handles `atelier init` with no source: adopt an existing
// Terraform project in CWD as an Atelier-managed wrapper.
func runInitAdopt(cwd, moduleDir string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	stop := startSpinner("Initializing from existing project…")
	defer stop()
	res, err := convert.Run(ctx, convert.Options{
		Dir:       cwd,
		ModuleDir: moduleDir,
	})
	stop()
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Initialized successfully.\n")
	if res.Adopted {
		fmt.Fprintf(os.Stderr, "  Existing module block adopted as Atelier wrapper.\n")
	} else {
		fmt.Fprintf(os.Stderr, "  Module files moved to: ./%s/\n", res.ModuleDir)
		if res.BackupStatePath != "" {
			fmt.Fprintf(os.Stderr, "  State backup: %s\n", filepath.Base(res.BackupStatePath))
		}
		if res.ResourcesMoved > 0 {
			fmt.Fprintf(os.Stderr, "  Resources migrated: %d\n", res.ResourcesMoved)
		}
	}
	fmt.Fprintf(os.Stderr, "\nRun 'atelier' to open the TUI.\n")
	return nil
}

// initOpts extends bootstrap.InitOptions with convert-specific fields.
type initOpts struct {
	bootstrap.InitOptions
	ModuleDir string // for adopt/relocate path
}

func parseInitArgs(args []string) (initOpts, error) {
	opts := initOpts{}
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
		case "--module-dir":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--module-dir requires a name")
			}
			opts.ModuleDir = args[i]
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
	// Source is now optional — empty means adopt/convert existing project.
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
	m.WrapperDir = wrapperDir
	m.SetPresets(presets)

	// Discover and load secondary modules from main.tf. Also use the
	// actual block name from main.tf to ensure the primary module's display
	// name is correct (PrepareState may derive a different name).
	actualPrimaryName := state.ModuleBlockName
	if blocks, err := wrapper.ReadModuleBlocks(wrapperDir); err == nil {
		// Find the block whose source matches the primary state's source.
		for _, blk := range blocks {
			if blk.Source == state.Source {
				actualPrimaryName = blk.Name
				break
			}
		}
	}
	if actualPrimaryName != state.ModuleBlockName {
		// Correct the primary module's display name and internal block name.
		state.ModuleBlockName = actualPrimaryName
		m.Modules[0] = tui.ModuleEntry{State: state, Name: actualPrimaryName}
		m.ModuleName = actualPrimaryName
	}

	// Populate the primary module's ref identity + per-module switcher so the
	// R key can switch it independently of any secondaries.
	m.Modules[0].SourceURL = sourceURLFromState(state)
	m.Modules[0].Ref = res.LiteralRef
	m.Modules[0].ResolvedSHA = res.ResolvedSHA
	if res.LiteralRef != "" || res.ResolvedSHA != "" {
		m.Modules[0].Switcher = &prodRefSwitcher{
			wrapperDir:          wrapperDir,
			sourceURL:           sourceURLFromState(state),
			modulePath:          modulePathFromState(state),
			blockName:           state.ModuleBlockName,
			isPrimary:           true,
			currentVars:         state.Vars,
			currentValues:       state.Values,
			currentUnknownAttrs: state.UnknownAttrs,
		}
	}

	loadSecondaryModules(m, wrapperDir, actualPrimaryName)

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
	} else {
		fmt.Fprintln(os.Stderr, "warning: planner unavailable:", err)
	}

	// Construct a RefSwitcher for non-local-source wrappers. Local sources
	// (--source path) don't have a git remote to switch refs on. This mirrors
	// m.Modules[0].Switcher and is kept as a global fallback for callers/tests
	// that consult m.RefSwitcher directly.
	if res.LiteralRef != "" || res.ResolvedSHA != "" {
		m.RefSwitcher = m.Modules[0].Switcher
	}

	// Load terraform state for the plan view context line.
	if s, _ := tfstate.Read(wrapperDir); s != nil {
		m.SetTFState(s)
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

// loadingMessage returns the startup spinner label for a wrapper, reflecting
// how many module blocks it declares so the message matches what the user
// sees in the TUI (e.g. "Loading 3 module(s)…"). Falls back to a generic
// label when the count can't be determined.
func loadingMessage(wrapperDir string) string {
	if n := countModuleBlocks(wrapperDir); n > 0 {
		return fmt.Sprintf("Loading %d module(s)…", n)
	}
	return "Loading wrapper…"
}

// countModuleBlocks returns the number of module {} blocks in main.tf that
// carry a source (i.e. the modules Atelier will load).
func countModuleBlocks(wrapperDir string) int {
	blocks, err := wrapper.ReadModuleBlocks(wrapperDir)
	if err != nil {
		return 0
	}
	n := 0
	for _, blk := range blocks {
		if blk.Source != "" {
			n++
		}
	}
	return n
}

// loadSecondaryModules discovers module blocks in main.tf beyond the primary
// one, clones their sources, parses their variables, and adds them to the TUI
// model. Failures are non-fatal — the secondary module is simply not shown.
func loadSecondaryModules(m *tui.Model, wrapperDir, primaryBlockName string) {
	blocks, err := wrapper.ReadModuleBlocks(wrapperDir)
	if err != nil {
		return
	}

	// Count total vs. secondary blocks. The spinner reports the TOTAL (so it
	// matches the count the user sees in the TUI), but we only show it when
	// there is at least one secondary still to clone — the primary was already
	// loaded under the same label in runOpen.
	totalCount, secondaryCount := 0, 0
	for _, blk := range blocks {
		if blk.Source == "" {
			continue
		}
		totalCount++
		if blk.Name != primaryBlockName {
			secondaryCount++
		}
	}
	if secondaryCount > 0 {
		stop := startSpinner(fmt.Sprintf("Loading %d module(s)…", totalCount))
		defer stop()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	type result struct {
		entry tui.ModuleEntry
		name  string
	}
	var results []result
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, blk := range blocks {
		// Skip the primary block (already loaded) and blocks without a source.
		// Modules are keyed by their unique HCL label, NOT by source URL, so a
		// second block pointing at the same source at a different ref is shown
		// as its own module.
		if blk.Name == primaryBlockName || blk.Source == "" {
			continue
		}
		wg.Add(1)
		go func(blk wrapper.ModuleBlockInfo) {
			defer wg.Done()
			e := loadSecondaryModule(ctx, wrapperDir, blk)
			if e != nil {
				mu.Lock()
				results = append(results, result{entry: *e, name: blk.Name})
				mu.Unlock()
			}
		}(blk)
	}
	wg.Wait()

	// Add in the order they appear in main.tf.
	nameOrder := make(map[string]int, len(blocks))
	for i, blk := range blocks {
		nameOrder[blk.Name] = i
	}
	sort.Slice(results, func(i, j int) bool {
		return nameOrder[results[i].name] < nameOrder[results[j].name]
	})
	for _, r := range results {
		m.AddModuleEntry(r.entry)
	}
}

// loadSecondaryModule clones and parses a secondary module's variables and
// builds its ref identity + per-module switcher.
func loadSecondaryModule(ctx context.Context, wrapperDir string, blk wrapper.ModuleBlockInfo) *tui.ModuleEntry {
	srcURL, ref := decomposeModuleSource(blk.Source)
	if srcURL == "" {
		return nil
	}
	modPath := modulePathFromSource(blk.Source)

	// Clone into .atelier/clone/<reponame>, limited to the module subdir.
	cloneDir, sha, err := bootstrap.ResolveAndClone(ctx, bootstrap.InitOptions{
		WrapperDir:  wrapperDir,
		Source:      srcURL,
		LocalSource: isLocalPath(srcURL),
		Ref:         ref,
		ModulePath:  modPath,
		GitRunner:   &gitops.Git{},
	})
	if err != nil {
		return nil
	}

	state, err := bootstrap.PrepareState(wrapperDir, cloneDir, modPath, sha, ref, srcURL)
	if err != nil {
		return nil
	}
	state.ModuleBlockName = blk.Name
	state.Source = blk.Source

	// Overlay existing values from main.tf.
	pm, err := wrapper.ReadMainForBlock(wrapperDir, blk.Name, state.Vars)
	if err == nil && pm != nil {
		for k, v := range pm.Values {
			state.Values[k] = v
		}
		state.UnknownAttrs = pm.UnknownAttrs
	}

	entry := tui.ModuleEntry{
		State:       state,
		Name:        blk.Name,
		SourceURL:   srcURL,
		Ref:         ref,
		ResolvedSHA: sha,
	}
	// Only git-sourced modules can be ref-switched; local sources get no
	// switcher (R is a no-op for them).
	if !isLocalPath(srcURL) {
		entry.Switcher = &prodRefSwitcher{
			wrapperDir:          wrapperDir,
			sourceURL:           srcURL,
			modulePath:          modPath,
			blockName:           blk.Name,
			isPrimary:           false,
			currentVars:         state.Vars,
			currentValues:       state.Values,
			currentUnknownAttrs: state.UnknownAttrs,
		}
	}
	return &entry
}

// decomposeModuleSource parses a terraform module source string into the
// git URL and ref components.
func decomposeModuleSource(source string) (url, ref string) {
	s := source
	if i := strings.Index(s, "?ref="); i >= 0 {
		ref = s[i+len("?ref="):]
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "git::")
	// Strip the "//<path>" module sub-path suffix.
	searchFrom := 0
	if schemeEnd := strings.Index(s, "://"); schemeEnd >= 0 {
		searchFrom = schemeEnd + 3
	}
	if idx := strings.Index(s[searchFrom:], "//"); idx >= 0 {
		s = s[:searchFrom+idx]
	}
	return s, ref
}

// modulePathFromSource extracts the module sub-path from a terraform source.
func modulePathFromSource(source string) string {
	s := source
	// Strip ?ref= query.
	if q := strings.Index(s, "?ref="); q >= 0 {
		s = s[:q]
	}
	s = strings.TrimPrefix(s, "git::")
	// Find the "//" separator after the scheme.
	searchFrom := 0
	if schemeEnd := strings.Index(s, "://"); schemeEnd >= 0 {
		searchFrom = schemeEnd + 3
	}
	if idx := strings.Index(s[searchFrom:], "//"); idx >= 0 {
		return s[searchFrom+idx+2:]
	}
	return ""
}

// isLocalPath heuristically checks if a source looks like a local path.
func isLocalPath(source string) bool {
	return strings.HasPrefix(source, "/") ||
		strings.HasPrefix(source, "./") ||
		strings.HasPrefix(source, "../")
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
	blockName     string
	isPrimary     bool
	currentVars   []tfvars.Variable
	currentValues map[string]cty.Value
	// currentUnknownAttrs holds wired expressions (e.g.
	// model_uuid = data.juju_model.x.uuid) that live outside Values. They
	// must be carried into the mid-switch state.Write() below, otherwise the
	// rewritten module block silently loses the reference.
	currentUnknownAttrs []wrapper.RawAttr
	progress            *tui.ProgressTracker
}

func (s *prodRefSwitcher) SetProgress(p *tui.ProgressTracker) {
	s.progress = p
}

func (s *prodRefSwitcher) SwitchRef(ctx context.Context, newRef string) (*tui.RefSwitchResult, error) {
	// Re-clone at the new ref.
	if s.progress != nil {
		s.progress.SetPhase("Cloning at new ref…")
	}
	cloneDir, sha, err := bootstrap.ResolveAndClone(ctx, bootstrap.InitOptions{
		WrapperDir: s.wrapperDir,
		Source:     s.sourceURL,
		Ref:        newRef,
		ModulePath: s.modulePath,
	})
	if err != nil {
		return nil, err
	}

	// Re-parse variables from the new clone.
	if s.progress != nil {
		s.progress.SetPhase("Parsing variables…")
	}
	state, err := bootstrap.PrepareState(s.wrapperDir, cloneDir, s.modulePath, sha, newRef, s.sourceURL)
	if err != nil {
		return nil, err
	}
	// PrepareState derives a block name from the module path, which may not
	// match the actual HCL label in main.tf (especially for secondary
	// modules). Pin it to the block we're switching so Write targets the
	// correct module block.
	if s.blockName != "" {
		state.ModuleBlockName = s.blockName
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
	// Carry over wired expressions (UnknownAttrs) for variables that still
	// exist in the new ref. These do NOT live in Values, so without this the
	// state.Write() below would drop them from the rewritten module block —
	// e.g. model_uuid = data.juju_model.service_model.uuid disappears, leaving
	// an invalid module that terraform init/plan rejects.
	if len(s.currentUnknownAttrs) > 0 {
		carried := make([]wrapper.RawAttr, 0, len(s.currentUnknownAttrs))
		for _, ra := range s.currentUnknownAttrs {
			if newVarNames[ra.Name] {
				carried = append(carried, ra)
			}
		}
		state.UnknownAttrs = carried
	}
	if err := state.Write(); err != nil {
		return nil, fmt.Errorf("write wrapper: %w", err)
	}

	// Run terraform init -upgrade so Terraform fetches the new module revision.
	tf, err := tfexec.New(s.wrapperDir, "")
	if err != nil {
		return nil, fmt.Errorf("terraform init -upgrade: %w", err)
	}
	if s.progress != nil {
		s.progress.SetPhase("Running terraform init…")
		tf.SetStdout(&tui.ProgressWriter{Tracker: s.progress})
		defer tf.SetStdout(nil)
	}
	// A ref switch that changes the module's API can leave the wrapper
	// temporarily invalid — most commonly when the new ref adds a required
	// variable the user hasn't filled yet, which Terraform reports as
	// "Missing required argument" during init's config-load phase. That phase
	// runs *after* module installation, so by the time it fails the new module
	// revision is already fetched and the switch is otherwise complete. Treat
	// such a failure as non-fatal: surface the new schema, let the user fill
	// the gaps, and rely on the planner's ResetInit() (which re-runs
	// init -upgrade on the next plan) plus `terraform validate` to resolve and
	// report the specifics. This mirrors the fresh-bootstrap flow, which never
	// gates on init at all. Hard init failures (bad ref, provider install)
	// resurface fatally on the next plan with the full message. The TUI
	// inspects the new schema to phrase the user-facing condition (e.g. how
	// many required variables are unset), so we report only the bare signal.
	initIncomplete := tf.InitUpgrade(ctx) != nil

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

	// Update the switcher's current vars, values, and wired expressions for
	// future switches.
	s.currentVars = state.Vars
	s.currentValues = state.Values
	s.currentUnknownAttrs = state.UnknownAttrs

	// Save session with new ref. Only the primary module owns session.json;
	// secondary modules are tracked entirely by their main.tf source string,
	// so a secondary switch must not overwrite the primary's session identity.
	if s.isPrimary {
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
	}

	return &tui.RefSwitchResult{
		State:          state,
		ResolvedSHA:    sha,
		LiteralRef:     newRef,
		OrphanedVars:   orphaned,
		NewVars:        newVars,
		InitIncomplete: initIncomplete,
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
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-done:
				// Clear the spinner line.
				fmt.Fprintf(os.Stderr, "\r\033[K")
				return
			case <-ticker.C:
				fmt.Fprintf(os.Stderr, "\r%s %s", frames[i%len(frames)], msg)
				i++
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
