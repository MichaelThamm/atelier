// Command atelier is the entry point to Atelier's CLI.
//
// Surface (SPEC §6):
//
//	atelier                                     open the wrapper in CWD
//	atelier add <git-url|gallery-name> [--as NAME] [--ref REF] [--module SUBDIR] [--dir PATH] [--yes]
//	                                            add a module and open the editor (bootstraps a new
//	                                            wrapper, or appends to one the target already holds)
//	atelier rm <name> [--force]                 remove a module from the wrapper
//	atelier ls                                  list modules in the wrapper
//	atelier wrappers [PATH]                     list wrappers directly under a directory
//	atelier apply <git-url|gallery-name> [--module SUBDIR] [--ref REF] [--dir PATH]
//	                                            compose into the wrapper the target holds and deploy
//	                                            it, or scaffold a new one and init and apply it
//	atelier tidy [PATH] [--write]               prune arguments left at their default
//	atelier purge [PATH] [--force]              remove .atelier/ and .clone/
//	atelier gallery list [--commands]           list the bundled gallery quick starts
//	atelier presets lint --module <dir> <f>...  check preset bundles against a module
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
	"github.com/mattn/go-isatty"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/bootstrap"
	"github.com/MichaelThamm/atelier/internal/gitops"
	"github.com/MichaelThamm/atelier/internal/modulesource"
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
  atelier add <git-url|gallery-name> [--as NAME] [--ref REF] [--module SUBDIR]
                                [--dir PATH] [--var-file PATH|NAME] [--var KEY=VALUE]
                                [--list-var-files] [--strict] [--yes] [--json]
                                               Add a module and open the editor on it. If the target
                                               (--dir, else the current directory) already holds a
                                               wrapper, appends a module block to it; otherwise creates
                                               a directory named after the module (or by --dir/--as)
                                               and scaffolds a wrapper there. Does not
                                               run terraform init or apply — use 'atelier apply' for that.
                                               Warns and asks before writing into a directory that
                                               already holds other files; --yes skips the prompt.
                                               --var-file seeds values from a Terraform variable file: a local
                                               path, a name in an ancestor atelier.presets/ directory, or a name
                                               committed to the module repo. Comma-separate or repeat for several.
                                               --var sets a single module input (repeatable); --var wins over
                                               --var-file.
                                               --list-var-files prints the local and repo .tfvars bundles available.
                                               --strict makes var-file binding warnings fatal.
                                               --json writes the result to stdout as JSON: where the wrapper
                                               went, the block as written, and every block in it.
                                               A gallery name (see 'atelier gallery list') may be given instead
                                               of a URL; it resolves to the module, ref, block, and preset.
  atelier rm <name> [--force]                  Remove a module from the wrapper.
  atelier ls [--json]                          List modules in the wrapper. --json writes them to
                                               stdout instead of a table.
  atelier wrappers [PATH] [--json]             List the wrappers directly under PATH (default: the
                                               current directory): each child directory holding a
                                               main.tf or .atelier/, with the modules it declares.
                                               Read-only and one level only.
  atelier apply <git-url|gallery-name> [--module SUBDIR] [--ref REF] [--as NAME]
                                [--dir PATH] [--var-file PATH|NAME]
                                [--var KEY=VALUE] [--list-var-files]
                                               Compose into the wrapper the target already holds
                                               (--dir, else the current directory) and deploy that
                                               root; otherwise scaffold a wrapper in a new directory
                                               and run 'terraform init' and 'terraform apply'. A new
                                               directory is named after the module candidate unless
                                               --as or --dir says otherwise. When the wrapper already
                                               declares the module, its block is updated rather than
                                               duplicated: --var merges into the values it holds and a
                                               changed --ref re-points it. --as names the block to
                                               update when the wrapper declares the module more than
                                               once, and a name matching no block falls back to the
                                               one that does. At a terminal you confirm the plan at
                                               Terraform's prompt; with no terminal it applies with
                                               -auto-approve.
  atelier purge [PATH] [--force]               Remove .atelier/ and .clone/ from a directory.
  atelier tidy [PATH] [--write]                Prune module arguments left at their default value.
                                               Dry-run by default; --write applies it (backs up main.tf first).
  atelier import [PROVIDER] [--source URL] [--module PATH] [--ref REF]
        [--dir PATH] [--type T] [--var K=V] [--var-file PATH|NAME]
        [--list-var-files] [--query-var K=V] [--dry-run] [--list] [--yes] [--json]
                                                Import a running deployment into Terraform state. With --source,
                                                clones a remote module, writes an Atelier wrapper, and imports
                                                live resources into it. --source takes a gallery name (see
                                                'atelier gallery list') in place of a URL: it supplies the
                                                module, its subdirectory and its pinned revision, but none of
                                                its presets — an import must match what is already deployed.
                                                Without --source, imports into an
                                                already-initialised directory. Discovers live resources via
                                                'terraform query' (requires terraform >= 1.14), matches them to
                                                your module's resource addresses by name, and runs
                                                'terraform import' for each — a state-only operation that
                                                cannot change infrastructure. PROVIDER (e.g. juju) sets which
                                                list-resource types to query. For Juju the model UUID is
                                                derived from the live deployment, so it need not be passed
                                                as a --var. Variables the module declares without a default
                                                must still be supplied via --var or --var-file.
                                                --dir creates the directory when it is missing; without
                                                --source it must already be an initialised Terraform root.
                                                --list-var-files prints the local and repo .tfvars bundles
                                                available (requires --source) and exits without importing.
                                                --dry-run writes an imports.tf artifact and previews the plan
                                                without touching state.
                                                --json writes the report to stdout as JSON: what matched,
                                                what was imported, and what a later apply would create.
                                                The human report still goes to stderr.
  atelier gallery list [--commands]            List Atelier's bundled gallery of module quick starts:
                                                module, pinned ref, preset, and the command to deploy them.
                                                --commands prints the non-applying scaffold command per entry.
  atelier presets lint --module <dir> <file.tfvars>...
                                                Check preset .tfvars bundles against a module's variables.
                                                Reports unknown variable names, unknown keys nested in object
                                                values, and scalar type mismatches; exits non-zero on any finding.
  atelier --version                            Print the version and exit.
  atelier --help                               Print this help.

The wrapper is the durable artifact: a normal Terraform project Atelier
writes into the current directory. The TUI can run 'terraform plan' and, from
the plan view, 'terraform apply'; 'atelier apply' is a one-liner that
scaffolds a wrapper and then runs Terraform's own init and interactive apply.
Either way the wrapper stays runnable on its own without Atelier installed.
`

// version is the build version, injected at link time via
//
//	-ldflags "-X main.version=$(git describe --tags --always --dirty)"
//
// It defaults to "dev" for `go run`/`go build` without ldflags.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "atelier:", err)
		// The code says which side failed: Atelier's, or Terraform's (exit.go).
		os.Exit(exitCodeFor(err))
	}
}

// command is a canonical top-level command.
type command string

const (
	cmdOpen     command = "open"
	cmdAdd      command = "add"
	cmdRm       command = "rm"
	cmdLs       command = "ls"
	cmdApply    command = "apply"
	cmdWrappers command = "wrappers"
	cmdPurge    command = "purge"
	cmdTidy     command = "tidy"
	cmdImport   command = "import"
	cmdPresets  command = "presets"
	cmdGallery  command = "gallery"
)

// resolveCommand maps the first non-empty argument to its canonical command.
// It is pure so the dispatch table is testable without running anything.
func resolveCommand(args []string) (command, []string) {
	if len(args) == 0 {
		return cmdOpen, nil
	}
	switch args[0] {
	case "add":
		return cmdAdd, args[1:]
	case "rm", "remove":
		return cmdRm, args[1:]
	case "ls", "list":
		return cmdLs, args[1:]
	case "apply":
		return cmdApply, args[1:]
	case "wrappers":
		return cmdWrappers, args[1:]
	case "purge":
		return cmdPurge, args[1:]
	case "tidy":
		return cmdTidy, args[1:]
	case "import":
		return cmdImport, args[1:]
	case "presets":
		return cmdPresets, args[1:]
	case "gallery":
		return cmdGallery, args[1:]
	default:
		return command(args[0]), args[1:]
	}
}

func run(args []string) error {
	// Only check top-level --help / --version (not within subcommands).
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Print(usage)
		return nil
	}
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-v") {
		fmt.Printf("atelier %s\n", version)
		return nil
	}

	cmd, rest := resolveCommand(args)
	switch cmd {
	case cmdOpen:
		return runOpen()
	case cmdAdd:
		return runModuleAdd(rest)
	case cmdRm:
		return runModuleRm(rest)
	case cmdLs:
		return runModuleList(rest)
	case cmdApply:
		return runModuleApply(rest)
	case cmdWrappers:
		return runWrappers(rest)
	case cmdPurge:
		return runPurge(rest)
	case cmdTidy:
		return runTidy(rest)
	case cmdImport:
		return runImport(rest)
	case cmdPresets:
		return runPresets(rest)
	case cmdGallery:
		return runGallery(rest)
	default:
		if cmd == "module" {
			return fmt.Errorf("the 'module' namespace was removed; use 'atelier add', 'atelier rm', 'atelier ls', or 'atelier apply'\n\n%s", usage)
		}
		return fmt.Errorf("unknown command %q\n\n%s", string(cmd), usage)
	}
}

// runOpen implements `atelier` (no args): open the wrapper in CWD.
func runOpen() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	mainPath := filepath.Join(cwd, wrapper.MainTF)
	if _, err := os.Stat(mainPath); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("not a wrapper directory. Run 'atelier add <url>' to bootstrap")
	} else if err != nil {
		return err
	}
	if _, err := tfexec.Locate(); err != nil {
		return err
	}

	ctx, cancel := interruptContext()
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

func launchTUI(res *bootstrap.Result, wrapperDir string) error {
	state := res.State

	// The TUI needs a terminal; in scripts and CI (e.g. `add … < /dev/null`)
	// skip it. The wrapper is already written, so the command can just exit.
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		fmt.Fprintln(os.Stderr, "non-interactive: wrapper is ready; run 'atelier' in a terminal to edit.")
		return nil
	}

	// Load presets (`.tfvars` bundles) for the left pane: personal walk-up
	// bundles from atelier.presets/ ancestors, plus presets committed to the
	// module repo (ADR-0031).
	presets := presetsFromBundles(state, wrapperDir, res.CloneDir, res.ModulePath)

	m := tui.New(state, state.ModuleBlockName)
	m.LiteralRef = res.LiteralRef
	m.ResolvedSHA = res.ResolvedSHA
	m.SourceURL = modulesource.Remote(state.Source)
	m.WrapperDir = wrapperDir
	m.SetPresets(presets)

	// When the wrapper opened with an unresolvable ref, carry the marker into
	// the model so Init auto-opens the ref-switch modal with a recovery banner.
	if res.RefUnresolved != nil {
		m.RefUnresolved = &tui.RefUnresolvedInfo{
			Ref:       res.RefUnresolved.Ref,
			Reason:    res.RefUnresolved.Reason,
			Available: res.RefUnresolved.Available,
			Offline:   res.RefUnresolved.Offline,
		}
	}

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
	m.Modules[0].SourceURL = modulesource.Remote(state.Source)
	m.Modules[0].Ref = res.LiteralRef
	m.Modules[0].ResolvedSHA = res.ResolvedSHA
	if res.LiteralRef != "" || res.ResolvedSHA != "" {
		m.Modules[0].Switcher = &prodRefSwitcher{
			wrapperDir:          wrapperDir,
			sourceURL:           modulesource.Remote(state.Source),
			modulePath:          modulesource.ModulePath(state.Source),
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
	// reported in runOpen / runModule, so this should not error in practice;
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
	// (a `source = "./..."` path in main.tf) don't have a git remote to switch
	// refs on. This mirrors m.Modules[0].Switcher and is kept as a global
	// fallback for callers/tests that consult m.RefSwitcher directly.
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

// presetsFromBundles discovers the `.tfvars` presets the TUI picker offers:
// personal walk-up bundles (atelier.presets/) and presets committed to the
// module repo, read against the primary module's schema. Undeclared names and
// type mismatches are excluded and surfaced as an "(N ignored)" note on the
// description (ADR-0031).
func presetsFromBundles(state *wrapper.State, wrapperDir, cloneDir, modulePath string) []tui.ResolvedPreset {
	var out []tui.ResolvedPreset
	for _, b := range bootstrap.ListAllVarFiles(wrapperDir, cloneDir, modulePath) {
		vals, diags, err := wrapper.ReadTFVarsFileChecked(b.Path, state.Vars)
		if err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
			continue
		}
		desc := b.Description
		if skipped := len(diags.Unknown) + len(diags.Mismatched); skipped > 0 {
			desc = strings.TrimSpace(fmt.Sprintf("%s (%d ignored)", desc, skipped))
		}
		out = append(out, tui.ResolvedPreset{
			Name:        b.Name,
			Description: desc,
			Values:      vals,
			Source:      b.Source,
		})
	}
	return out
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

// loadSecondaryModules loads the wrapper's non-primary modules and adds them to
// the TUI model. It is a thin presentation layer over
// bootstrap.LoadSecondaryModules: the core does the reading, cloning and
// ordering; this maps the result to tui.ModuleEntry and attaches the per-module
// ref switcher. Failures are non-fatal — an unreachable module is warned about
// and omitted.
func loadSecondaryModules(m *tui.Model, wrapperDir, primaryBlockName string) {
	// The core loads secondaries; the spinner label needs the total block count
	// (matching what the user sees in the TUI), so read the blocks once here
	// for the label and once inside the core for the load. Both reads are
	// cheap; the costly part is the per-block clone, which the core does once.
	blocks, _ := wrapper.ReadModuleBlocks(wrapperDir)
	total, secondaries := 0, 0
	for _, b := range blocks {
		if b.Source == "" {
			continue
		}
		total++
		if b.Name != primaryBlockName {
			secondaries++
		}
	}
	if secondaries > 0 {
		stop := startSpinner(fmt.Sprintf("Loading %d module(s)…", total))
		defer stop()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	modules, warnings, err := bootstrap.LoadSecondaryModules(ctx, wrapperDir, primaryBlockName, secondaryLoader)
	if err != nil {
		return
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	for _, mod := range modules {
		m.AddModuleEntry(moduleEntry(wrapperDir, mod))
	}
}

// moduleEntry maps a loaded module to its TUI entry, attaching a ref switcher
// for git-sourced modules (local sources get none; R is a no-op for them).
func moduleEntry(wrapperDir string, mod bootstrap.LoadedModule) tui.ModuleEntry {
	entry := tui.ModuleEntry{
		State:       mod.State,
		Name:        mod.Name,
		SourceURL:   mod.SourceURL,
		Ref:         mod.Ref,
		ResolvedSHA: mod.ResolvedSHA,
	}
	if !modulesource.IsLocal(mod.SourceURL) {
		entry.Switcher = &prodRefSwitcher{
			wrapperDir:          wrapperDir,
			sourceURL:           mod.SourceURL,
			modulePath:          modulesource.ModulePath(mod.State.Source),
			blockName:           mod.Name,
			isPrimary:           false,
			currentVars:         mod.State.Vars,
			currentValues:       mod.State.Values,
			currentUnknownAttrs: mod.State.UnknownAttrs,
		}
	}
	return entry
}

// secondaryLoader clones and prepares the non-primary module blocks read from
// main.tf.
var secondaryLoader = &bootstrap.BlockLoader{Runner: &gitops.Git{}}

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
	if s.progress != nil {
		s.progress.SetPhase("Cloning at new ref…")
	}
	// Clone the new revision and re-read its variable schema. Carry over the
	// user's values for variables that still exist — required variables must be
	// present in the HCL for init to succeed. Values not in the new schema are
	// dropped (reported as OrphanedVars below).
	state, cloneDir, sha, err := bootstrap.LoadRefState(ctx, bootstrap.InitOptions{
		WrapperDir: s.wrapperDir,
		Source:     s.sourceURL,
		Ref:        newRef,
		ModulePath: s.modulePath,
	}, s.blockName, s.currentValues, s.currentUnknownAttrs)
	if err != nil {
		return nil, err
	}
	// Write the wrapper main.tf with the new source (ref) before running init,
	// so Terraform sees the updated module source during initialisation.
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
		tf.SetStdout(&tui.ProgressWriter{Tracker: s.progress, FileWriter: tf.StdoutFile()})
		tfexec.WriteTimestampHeader(tf.StdoutFile())
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
	newVarNames := make(map[string]bool, len(state.Vars))
	for _, v := range state.Vars {
		newVarNames[v.Name] = true
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

	// Refresh the preset picker for the new ref: a repo can ship preset
	// bundles on one ref but not another (e.g. a presets/ directory added on
	// a feature branch), and the list is otherwise only built at launch.
	presets := presetsFromBundles(state, s.wrapperDir, cloneDir, s.modulePath)

	return &tui.RefSwitchResult{
		State:          state,
		ResolvedSHA:    sha,
		LiteralRef:     newRef,
		OrphanedVars:   orphaned,
		NewVars:        newVars,
		InitIncomplete: initIncomplete,
		Presets:        presets,
	}, nil
}

// ListRefs returns the remote's current branch and tag names via a single
// `git ls-remote`, for the ref-switch modal's availability hint.
func (s *prodRefSwitcher) ListRefs(ctx context.Context) ([]string, error) {
	refs, err := gitops.LsRemote(ctx, &gitops.Git{}, s.sourceURL)
	if err != nil {
		return nil, err
	}
	return gitops.AvailableRefNames(refs), nil
}

// isTerminal reports whether f is an interactive terminal.
//
// This delegates to an isatty(3) check rather than testing os.ModeCharDevice,
// which was the previous implementation and was wrong: /dev/null is a character
// device, so `atelier add < /dev/null` was treated as interactive and the
// confirmation prompt read an immediate EOF instead of failing with a message
// telling the user to pass --yes.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// interruptContext returns a context cancelled by Ctrl-C. Every command that
// can be interrupted mid-clone or mid-plan uses this one prologue, so the
// SIGINT policy is defined in a single place.
func interruptContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

// startSpinner prints a progress message to stderr and returns a stop function.
//
// On a terminal it animates a braille spinner in place and clears the line when
// stopped. Otherwise it prints the message once and does nothing further.
//
// The distinction matters because the animation depends on the carriage return
// in "\r<frame> <msg>" moving the cursor back to column 0, so each write
// overwrites the previous one. Nothing interprets \r when stderr is a file or a
// pipe, so every 100ms tick appends another full copy of the line instead: a
// single `atelier import` run emitted 1344 of them, burying the actual output.
// The line-clearing escape sequence has the same problem, appearing as literal
// "[K" in captured logs.
func startSpinner(msg string) func() {
	if !isTerminal(os.Stderr) {
		fmt.Fprintln(os.Stderr, msg)
		return func() {}
	}

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
