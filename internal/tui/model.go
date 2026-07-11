// Package tui implements Atelier's Bubble Tea TUI.
//
// The TUI is a two-pane layout (left=variable list, right=editor) with a
// status pane at the bottom (SPEC §7, ADR-0006). The top-level Model owns
// the wrapper.State and routes input to the active editor based on the
// selected variable's type.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/gitops"
	"github.com/MichaelThamm/atelier/internal/state"
	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// Model is the top-level TUI model.
type Model struct {
	State *wrapper.State
	// Modules holds all module states for multi-module wrappers. When
	// len(Modules) > 1, the left pane shows section headers to group
	// variables by module. When empty or len==1, behaves as before.
	Modules []ModuleEntry

	// Module display info shown in the status bar.
	ModuleName   string
	LiteralRef   string
	ResolvedSHA  string
	SourceURL    string
	ManifestPath string

	// WrapperDir is the directory containing main.tf and terraform.tfstate.
	// Used for reloading state after apply.
	WrapperDir string

	rows   []rowEntry

	cursor       int
	leftScroll   int // scroll offset (first visible row) in the left pane
	focus        focusPane
	width, height int

	// editor is the active right-pane editor, type-specific per the
	// currently selected variable.
	editor       Editor
	editorScroll int // scroll offset for the right pane content

	// status text shown at the bottom. Cleared when a new edit lands.
	status       string
	statusLvl    statusLevel
	statusAt     time.Time
	statusDetail string // full multi-line error for [E] detail view
	errorDetail  bool   // true when the error detail modal is visible

	// Planner runs `terraform plan` asynchronously when the user presses P.
	// May be nil (e.g. in tests or read-only contexts); the P key just
	// produces a friendly status message in that case.
	Planner Planner

	// Applier runs `terraform apply` when the user presses A from the plan
	// view. May be nil; the A key is hidden if unset.
	Applier Applier

	// Validator runs `terraform validate` after edits (debounced). May be
	// nil; validation is skipped if unset.
	Validator Validator

	// plan tree + cursor when planState == planReady.
	planState    planState
	plan         *tfjson.Plan
	planTree     *planNode
	stateTree    *planNode // state resources as a tree (for browsing when no changes)
	planCursor   int
	planScroll   int // scroll offset for the plan tree pane
	planDiffScroll int // scroll offset for the plan diff pane
	planDiffFocus  bool // true when the diff pane is focused (Tab toggle)
	planShowState  bool // true when left+right panes show state instead of diff
	planErr      string
	planSpinnerFrame int
	progress     *ProgressTracker // live progress from terraform subprocess

	// applyState tracks the apply flow (idle → loading → done/error).
	applyState applyState
	applyErr   string

	// validateGen is a generation counter incremented on every edit. The
	// debounce tick carries the generation at scheduling time; if the model's
	// generation has advanced by the time the tick fires, the tick is stale.
	validateGen    uint64
	validateOutput *tfjson.ValidateOutput // most recent validate result

	// quitSignal: when set, the runtime tea.Quit will follow.
	quit bool

	// dirty tracks whether we have unsaved in-memory edits. Used by the
	// auto-save path that ends each Update tick.
	dirty bool

	// presets holds resolved presets from the manifest. When non-empty, the
	// user can press F to open the picker overlay.
	presets      []ResolvedPreset
	presetPicker bool // true when the picker overlay is visible
	presetCursor int  // cursor within the picker list

	// RefSwitcher handles the backend logic of switching module refs.
	// May be nil (e.g., local source wrappers where ref switching is N/A).
	RefSwitcher RefSwitcher

	// refModal state: tracks the ref-switch prompt and in-flight switch.
	refModal     bool   // true when the ref input prompt is visible
	refInput     string // current text in the ref input field
	refSwitching bool   // true when a ref switch is in flight (spinner)
	refErr       string // error from last ref switch attempt
	refOrphaned  []string // vars that no longer exist after a ref switch
	refModuleIdx int      // index of the module being ref-switched

	// availableRefs holds the remote's current ref names for the module the
	// modal targets, populated asynchronously via RefSwitcher.ListRefs and
	// shown as a hint under the input. refsLoading tracks the in-flight fetch.
	availableRefs []string
	refsLoading   bool

	// RefUnresolved, when non-nil, marks the primary module as opened with a
	// ref that no longer resolves on the remote (e.g. the branch was deleted).
	// The TUI auto-opens the ref-switch modal on startup and shows a banner
	// explaining why the variable schema is unavailable.
	RefUnresolved *RefUnresolvedInfo

	helpModal bool // true when the [?] help overlay is visible

	// tfState is the parsed terraform.tfstate loaded at TUI startup.
	// Used to show state context in the plan view. May be nil.
	tfState *state.State
}

// planState enumerates the four states the plan flow can be in: idle (no
// plan ever requested, or the user closed the plan view), loading (a plan
// is in flight), ready (a fresh plan is rendered and interactive), and
// error (the last plan attempt failed; rendered in the status bar, not the
// plan view).
type planState int

const (
	planIdle planState = iota
	planLoading
	planReady
)

// applyState tracks the terraform apply lifecycle.
type applyState int

const (
	applyIdle applyState = iota
	applyLoading
	applyDone
)

// spinnerFrames is the visible animation for the in-flight plan indicator.
// The Braille-octant set is widely supported and reads well on dark and
// light backgrounds without colour.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// maxInt is a large sentinel for "scroll to end" (clamped at render time).
const maxInt = int(^uint(0) >> 1)

type focusPane int

const (
	focusLeft focusPane = iota
	focusRight
)

type statusLevel int

const (
	statusInfo statusLevel = iota
	statusWarn
	statusError
)

// rowEntry is one row in the left pane: either a variable or a section header.
type rowEntry struct {
	VarName   string
	ModuleIdx int  // index into Model.Modules
	IsHeader  bool // true for non-selectable group headers
}

// ModuleEntry represents one module in a multi-module wrapper session.
type ModuleEntry struct {
	State *wrapper.State
	Name  string // display name (typically the module block name)

	// Ref identity for THIS specific module. Populated for git-sourced
	// modules so each module can be ref-switched independently.
	SourceURL   string
	Ref         string
	ResolvedSHA string
	// Switcher performs the ref switch for this module. Nil for local
	// sources (no remote to switch); the R key is a no-op for those.
	Switcher RefSwitcher
}

// New builds a Model around a wrapper.State.
func New(state *wrapper.State, modName string) *Model {
	m := &Model{
		State:      state,
		ModuleName: modName,
		Modules:    []ModuleEntry{{State: state, Name: modName}},
	}
	m.recomputeRows()
	m.refreshEditor()
	return m
}

// AddModule appends an additional module to the TUI. Variables from all
// modules are shown in the left pane, grouped under section headers when
// there is more than one module.
func (m *Model) AddModule(state *wrapper.State, name string) {
	m.AddModuleEntry(ModuleEntry{State: state, Name: name})
}

// AddModuleEntry appends a fully-populated module entry (including its ref
// identity and per-module switcher) to the session.
func (m *Model) AddModuleEntry(e ModuleEntry) {
	m.Modules = append(m.Modules, e)
	m.recomputeRows()
}

// SetPresets installs resolved presets from the manifest. When non-empty,
// the user can press F from the left pane to open the preset picker.
func (m *Model) SetPresets(p []ResolvedPreset) {
	m.presets = p
}

// SetTFState sets the parsed terraform state for display in the plan view.
func (m *Model) SetTFState(s *state.State) {
	m.tfState = s
}

func (m *Model) recomputeRows() {
	var rows []rowEntry
	if len(m.Modules) > 1 {
		// Multi-module: group variables under section headers.
		for idx, mod := range m.Modules {
			rows = append(rows, rowEntry{
				VarName:   mod.Name,
				ModuleIdx: idx,
				IsHeader:  true,
			})
			for _, v := range mod.State.Vars {
				rows = append(rows, rowEntry{
					VarName:   v.Name,
					ModuleIdx: idx,
				})
			}
		}
	} else {
		// Single module: flat list, no headers (backward compat).
		for _, v := range m.State.Vars {
			rows = append(rows, rowEntry{VarName: v.Name})
		}
	}
	m.rows = rows
	if m.cursor >= len(rows) {
		m.cursor = len(rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	// If cursor landed on a header, advance to the first variable.
	if m.cursor < len(m.rows) && m.rows[m.cursor].IsHeader {
		m.skipHeader(+1)
	}
}

// SelectedVariable returns the variable currently under the cursor, or nil
// if out of range.
func (m *Model) SelectedVariable() *tfvars.Variable {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	r := m.rows[m.cursor]
	if r.IsHeader {
		return nil
	}
	st := m.moduleStateForRow(r)
	return st.FindVar(r.VarName)
}

// ActiveModuleState returns the wrapper.State for the variable currently
// under the cursor. Falls back to the primary State.
func (m *Model) ActiveModuleState() *wrapper.State {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return m.State
	}
	return m.moduleStateForRow(m.rows[m.cursor])
}

// moduleStateForRow returns the wrapper.State that owns the given row entry.
func (m *Model) moduleStateForRow(r rowEntry) *wrapper.State {
	if len(m.Modules) > 1 && r.ModuleIdx < len(m.Modules) {
		return m.Modules[r.ModuleIdx].State
	}
	return m.State
}

// activeModuleIdx returns the index into m.Modules of the module owning the
// variable under the cursor. Falls back to the primary module (0).
func (m *Model) activeModuleIdx() int {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		idx := m.rows[m.cursor].ModuleIdx
		if idx >= 0 && idx < len(m.Modules) {
			return idx
		}
	}
	return 0
}

// activeModule returns the ModuleEntry owning the variable under the cursor,
// or nil when there are no modules.
func (m *Model) activeModule() *ModuleEntry {
	if len(m.Modules) == 0 {
		return nil
	}
	return &m.Modules[m.activeModuleIdx()]
}

// activeSwitcher returns the RefSwitcher for the active module. Per-module
// switchers take precedence; the global m.RefSwitcher is a fallback so that
// single-module sessions (and tests) that only set m.RefSwitcher keep working.
func (m *Model) activeSwitcher() RefSwitcher {
	if e := m.activeModule(); e != nil && e.Switcher != nil {
		return e.Switcher
	}
	return m.RefSwitcher
}

// switcherForIdx returns the RefSwitcher for a specific module index, with the
// same global fallback as activeSwitcher.
func (m *Model) switcherForIdx(idx int) RefSwitcher {
	if idx >= 0 && idx < len(m.Modules) && m.Modules[idx].Switcher != nil {
		return m.Modules[idx].Switcher
	}
	return m.RefSwitcher
}

// activeRefInfo returns display info (name, source, ref, sha) for the active
// module, falling back to the legacy model-level fields for single-module
// sessions that don't populate per-entry ref identity.
func (m *Model) activeRefInfo() (name, source, ref, sha string) {
	if e := m.activeModule(); e != nil {
		name, source, ref, sha = e.Name, e.SourceURL, e.Ref, e.ResolvedSHA
	}
	if name == "" {
		name = m.ModuleName
	}
	if source == "" {
		source = m.SourceURL
	}
	if ref == "" {
		ref = m.LiteralRef
	}
	if sha == "" {
		sha = m.ResolvedSHA
	}
	return name, source, ref, sha
}

func (m *Model) refreshEditor() {
	v := m.SelectedVariable()
	if v == nil {
		m.editor = nil
		return
	}
	st := m.ActiveModuleState()
	current, _ := st.VariableValue(v.Name)
	m.editor = newEditor(v, current)
	m.editorScroll = 0 // reset scroll when switching variables
	// The caret belongs only to the editor pane: when the list pane is
	// active, blur the freshly built editor so it shows no cursor.
	if m.focus != focusRight {
		if f, ok := m.editor.(focusable); ok {
			f.Blur()
		}
	}
}

// setFocus moves pane focus and reconciles the editor's caret: the cursor
// shows only while the editor pane is the active context.
func (m *Model) setFocus(pane focusPane) {
	m.focus = pane
	f, ok := m.editor.(focusable)
	if !ok {
		return
	}
	if pane == focusRight {
		f.Focus()
	} else {
		f.Blur()
	}
}

// planResultMsg carries a successful plan back to the UI thread.
type planResultMsg struct {
	plan *tfjson.Plan
}

// planErrorMsg carries a plan failure back to the UI thread for display
// in the status bar.
type planErrorMsg struct {
	err error
}

// spinnerTickMsg drives the in-flight spinner animation.
type spinnerTickMsg time.Time

// refSwitchResultMsg carries a successful ref switch back to the UI thread.
type refSwitchResultMsg struct {
	result *RefSwitchResult
}

// refSwitchErrorMsg carries a ref-switch failure back to the UI thread.
type refSwitchErrorMsg struct {
	err error
}

// refsLoadedMsg carries the result of an async ListRefs fetch for the
// ref-switch modal hint. refs is nil when the remote couldn't be reached
// (non-fatal — the input stays free-text).
type refsLoadedMsg struct {
	refs []string
}

// RefUnresolvedInfo mirrors bootstrap.RefUnresolved for the TUI layer: it
// records why the wrapper opened without a resolvable ref so the model can
// auto-open the switch modal, seed the hint, and render an explanatory banner.
type RefUnresolvedInfo struct {
	Ref       string
	Reason    string
	Available []string
	Offline   bool
}

// applyResultMsg signals a successful terraform apply.
type applyResultMsg struct{}

// applyErrorMsg carries an apply failure back to the UI thread.
type applyErrorMsg struct {
	err error
}

// validateDebounceMsg fires after the debounce delay. The gen field is
// compared against Model.validateGen to detect stale ticks.
type validateDebounceMsg struct {
	gen uint64
}

// validateResultMsg carries a successful validate result back to the UI.
type validateResultMsg struct {
	output *tfjson.ValidateOutput
}

// validateErrorMsg carries a validate failure back to the UI.
type validateErrorMsg struct {
	err error
}

// startPlan composes the async pipeline behind the P key: save state, run
// `terraform init` if needed, run `terraform plan`, return the parsed plan.
// Errors are funnelled through planErrorMsg so the UI can surface them
// without dropping the editor session.
func (m *Model) startPlan() tea.Cmd {
	if m.Planner == nil {
		return func() tea.Msg {
			return planErrorMsg{err: fmt.Errorf("plan unavailable: planner not configured")}
		}
	}
	// Create a fresh progress tracker and attach it to the planner.
	m.progress = NewProgressTracker()
	if tp, ok := m.Planner.(*TfexecPlanner); ok {
		tp.Progress = m.progress
	}
	modules := m.Modules
	planner := m.Planner
	return func() tea.Msg {
		if err := writeAllModules(modules); err != nil {
			return planErrorMsg{err: fmt.Errorf("save wrapper: %w", err)}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := planner.EnsureInit(ctx); err != nil {
			return planErrorMsg{err: err}
		}
		plan, err := planner.Plan(ctx)
		if err != nil {
			return planErrorMsg{err: err}
		}
		return planResultMsg{plan: plan}
	}
}

// spinnerTick schedules the next animation frame. Cheap: a single timer.
func spinnerTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg(t)
	})
}

// startApply runs `terraform apply` using the cached plan file.
func (m *Model) startApply() tea.Cmd {
	if m.Applier == nil {
		return func() tea.Msg {
			return applyErrorMsg{err: fmt.Errorf("apply unavailable: applier not configured")}
		}
	}
	// Create a fresh progress tracker and attach it to the applier.
	m.progress = NewProgressTracker()
	if tp, ok := m.Applier.(*TfexecPlanner); ok {
		tp.Progress = m.progress
	}
	applier := m.Applier
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := applier.Apply(ctx); err != nil {
			return applyErrorMsg{err: err}
		}
		return applyResultMsg{}
	}
}

// scheduleValidate bumps the generation counter and returns a debounce
// tick command. Called after every edit.
func (m *Model) scheduleValidate() tea.Cmd {
	if m.Validator == nil {
		return nil
	}
	m.validateGen++
	gen := m.validateGen
	return tea.Tick(500*time.Millisecond, func(_ time.Time) tea.Msg {
		return validateDebounceMsg{gen: gen}
	})
}

// startValidate saves state and runs `terraform validate` asynchronously.
func (m *Model) startValidate() tea.Cmd {
	modules := m.Modules
	validator := m.Validator
	return func() tea.Msg {
		if err := writeAllModules(modules); err != nil {
			return validateErrorMsg{err: fmt.Errorf("save wrapper: %w", err)}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := validator.Validate(ctx)
		if err != nil {
			return validateErrorMsg{err: err}
		}
		return validateResultMsg{output: out}
	}
}

// startRefSwitch runs the ref switch in a goroutine and returns result/error
// messages to the TUI.
func (m *Model) startRefSwitch(newRef string) tea.Cmd {
	m.progress = NewProgressTracker()
	switcher := m.switcherForIdx(m.refModuleIdx)
	if pa, ok := switcher.(ProgressAware); ok {
		pa.SetProgress(m.progress)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		result, err := switcher.SwitchRef(ctx, newRef)
		if err != nil {
			return refSwitchErrorMsg{err: err}
		}
		return refSwitchResultMsg{result: result}
	}
}

// startListRefs fetches the remote's available ref names for the module the
// modal targets, off the UI thread. Failures are swallowed to a nil list —
// the hint simply won't render, and the input stays free-text.
func (m *Model) startListRefs() tea.Cmd {
	switcher := m.switcherForIdx(m.refModuleIdx)
	if switcher == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		refs, err := switcher.ListRefs(ctx)
		if err != nil {
			return refsLoadedMsg{refs: nil}
		}
		return refsLoadedMsg{refs: refs}
	}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	// When the wrapper opened with an unresolvable ref, drop the user straight
	// into the ref-switch modal so the first thing they see is the fix, and
	// begin fetching the remote's available refs for the hint. The current
	// (broken) ref is pre-seeded for easy editing. When the failure was a
	// deleted ref, the bootstrap layer already handed us the available list;
	// seed it so the hint shows immediately even before the async refresh.
	if m.RefUnresolved != nil {
		m.refModuleIdx = 0
		_, _, ref, _ := m.activeRefInfo()
		if ref == "" {
			ref = m.RefUnresolved.Ref
		}
		m.refModal = true
		m.refInput = ref
		m.availableRefs = m.RefUnresolved.Available
		if !m.RefUnresolved.Offline {
			m.refsLoading = true
			return m.startListRefs()
		}
	}
	return nil
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case planResultMsg:
		m.plan = msg.plan
		m.planTree = BuildPlanTree(msg.plan)
		// Build state tree for the [S] toggle (always available if state exists).
		if m.tfState != nil && m.tfState.Summary.Total > 0 {
			m.stateTree = BuildStateTree(m.tfState)
		}
		// Auto-show state when plan has no changes.
		m.planShowState = len(flattenedRows(m.planTree)) == 0 && m.stateTree != nil
		m.planCursor = 0
		m.planScroll = 0
		m.planDiffScroll = 0
		m.planState = planReady
		m.planErr = ""
		m.status = ""
		m.progress = nil
		return m, nil
	case planErrorMsg:
		m.planState = planIdle
		m.planErr = msg.err.Error()
		m.status = "plan failed: " + msg.err.Error()
		m.statusDetail = msg.err.Error()
		m.statusLvl = statusError
		m.statusAt = time.Now()
		m.progress = nil
		return m, nil
	case spinnerTickMsg:
		if m.planState == planLoading || m.refSwitching || m.applyState == applyLoading {
			m.planSpinnerFrame = (m.planSpinnerFrame + 1) % len(spinnerFrames)
			return m, spinnerTick()
		}
		return m, nil
	case applyResultMsg:
		m.applyState = applyDone
		m.applyErr = ""
		m.status = "apply succeeded"
		m.statusLvl = statusInfo
		m.statusAt = time.Now()
		m.progress = nil
		// Invalidate the plan — it has been consumed.
		m.planState = planIdle
		m.plan = nil
		m.planTree = nil
		// Reload state after successful apply.
		if m.WrapperDir != "" {
			if s, _ := state.Read(m.WrapperDir); s != nil {
				m.tfState = s
			}
		}
		return m, nil
	case applyErrorMsg:
		m.applyState = applyIdle
		m.applyErr = msg.err.Error()
		m.status = "apply failed: " + msg.err.Error()
		m.statusDetail = msg.err.Error()
		m.statusLvl = statusError
		m.statusAt = time.Now()
		m.progress = nil
		return m, nil
	case validateDebounceMsg:
		// Only fire if no newer edit has occurred since this tick was scheduled.
		if msg.gen == m.validateGen {
			return m, m.startValidate()
		}
		return m, nil
	case validateResultMsg:
		m.validateOutput = msg.output
		m.dirty = false // state was written by startValidate
		if msg.output != nil && !msg.output.Valid {
			m.statusDetail = formatValidateDiagnostics(msg.output)
			m.statusLvl = statusError
			m.status = fmt.Sprintf("validate: %d error(s)", msg.output.ErrorCount)
			m.statusAt = time.Now()
		} else {
			// Clear any previous validate error.
			if m.statusLvl == statusError && m.validateOutput != nil {
				m.statusDetail = ""
				m.statusLvl = statusInfo
				m.status = ""
			}
		}
		return m, nil
	case validateErrorMsg:
		// Validation errors are non-fatal; just clear output so the status
		// bar stops showing stale results.
		m.validateOutput = nil
		return m, nil
	case refSwitchResultMsg:
		m.progress = nil
		m.applyRefSwitch(msg.result)
		return m, nil
	case refSwitchErrorMsg:
		m.refSwitching = false
		m.progress = nil
		// A failed switch must not tear down the current state — the user may
		// have simply typed a ref that doesn't exist. Re-open the modal so
		// they can correct it, phrase a precise message when the remote told
		// us the ref is gone, and refresh the available-refs hint. The wrapper
		// on disk is untouched: SwitchRef resolves the ref before any write.
		m.refModal = true
		var refErr *gitops.RefNotFoundError
		if errors.As(msg.err, &refErr) {
			m.availableRefs = refErr.Available
			m.refErr = fmt.Sprintf("ref %q not found on the remote", refErr.Ref)
		} else {
			m.refErr = msg.err.Error()
		}
		m.status = "ref switch failed: " + msg.err.Error()
		m.statusDetail = msg.err.Error()
		m.statusLvl = statusError
		m.statusAt = time.Now()
		// Kick off a fresh ref list so the hint reflects the current remote.
		m.refsLoading = true
		return m, m.startListRefs()
	case refsLoadedMsg:
		m.refsLoading = false
		// Prefer a freshly-fetched list; keep any seeded list if the fetch
		// came back empty (e.g. offline) so we don't blank a useful hint.
		if len(msg.refs) > 0 {
			m.availableRefs = msg.refs
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	if m.editor != nil {
		var cmd tea.Cmd
		m.editor, cmd = m.editor.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Universal shortcuts.
	switch msg.String() {
	case "ctrl+c":
		m.quit = true
		return m, tea.Quit
	case "?":
		m.helpModal = !m.helpModal
		return m, nil
	}

	// Help modal: absorbs all other keys until dismissed.
	if m.helpModal {
		if msg.String() == "esc" || msg.String() == "q" {
			m.helpModal = false
		}
		return m, nil
	}

	// Error detail modal: takes priority over all other views.
	if m.errorDetail {
		if msg.String() == "esc" || msg.String() == "q" || msg.String() == "e" || msg.String() == "E" {
			m.errorDetail = false
		}
		return m, nil
	}

	// Plan-mode interception: when a plan is on screen, the tree owns most
	// keys. Editor / list keys are unreachable until Esc returns the user
	// to the normal layout.
	if m.planState == planReady {
		return m.handlePlanKey(msg)
	}
	if m.planState == planLoading {
		// While plan is in flight only Ctrl+C and Esc do anything.
		if msg.String() == "esc" {
			m.planState = planIdle
			m.status = "plan cancelled (best effort)"
			m.statusLvl = statusInfo
		}
		return m, nil
	}

	// Preset picker interception: the overlay owns all keys until
	// the user applies (Enter) or cancels (Esc).
	if m.presetPicker {
		return m.handlePresetKey(msg)
	}

	// Ref modal interception: text input + confirm/cancel.
	if m.refModal {
		return m.handleRefModalKey(msg)
	}
	// While ref switch is in flight only Ctrl+C does anything.
	if m.refSwitching {
		return m, nil
	}

	switch msg.String() {
	case "q":
		if m.focus == focusLeft {
			m.quit = true
			return m, tea.Quit
		}
	case "tab":
		if m.focus == focusLeft {
			m.setFocus(focusRight)
		} else {
			m.setFocus(focusLeft)
		}
		return m, nil
	case "esc":
		// Depth-aware Esc (ADR-0023 §3): while the editor is drilled into a
		// nested structure it owns Esc to pop one level. Only at the editor's
		// top level does Esc return focus to the variable list.
		if m.focus == focusRight && m.editor != nil {
			if dp, ok := m.editor.(depthProvider); ok && !dp.AtTopLevel() {
				m.editor, _ = m.editor.Update(msg)
				return m, nil
			}
		}
		m.setFocus(focusLeft)
		return m, nil
	case "p", "P":
		// Trigger plan only from the left pane. In the right pane the key
		// belongs to the editor (the user might be typing "p" into a value).
		if m.focus == focusLeft {
			m.planState = planLoading
			m.planErr = ""
			m.status = ""
			return m, tea.Batch(m.startPlan(), spinnerTick())
		}
	case "f", "F":
		// Open preset picker from the left pane (if presets available).
		if m.focus == focusLeft && len(m.presets) > 0 {
			m.presetPicker = true
			m.presetCursor = 0
			return m, nil
		}
	case "r", "R":
		// Open the ref switch modal for the module under the cursor. Gated on
		// the active module having a switcher (git source). Local sources have
		// no remote to switch, so R is a no-op with a hint.
		if m.focus == focusLeft {
			if m.activeSwitcher() == nil {
				if len(m.Modules) > 1 {
					name, _, _, _ := m.activeRefInfo()
					m.status = fmt.Sprintf("%s has a local source — no ref to switch", name)
					m.statusLvl = statusInfo
					m.statusAt = time.Now()
				}
				return m, nil
			}
			m.refModuleIdx = m.activeModuleIdx()
			_, _, ref, _ := m.activeRefInfo()
			m.refModal = true
			m.refInput = ref
			m.refErr = ""
			m.refOrphaned = nil
			// Fetch the remote's current refs for the hint (async, non-fatal).
			m.availableRefs = nil
			m.refsLoading = true
			return m, m.startListRefs()
		}
	case "e", "E":
		// Open error detail modal when an error is present.
		if m.focus == focusLeft && m.statusLvl == statusError && m.statusDetail != "" {
			m.errorDetail = true
			return m, nil
		}
	case "ctrl+r":
		m.resetCurrent()
		return m, m.scheduleValidate()
	}

	if m.focus == focusLeft {
		return m.handleListKey(msg)
	}
	if m.editor != nil {
		ed, cmd := m.editor.Update(msg)
		m.editor = ed
		if e2, ok := m.editor.(EditorWithValue); ok {
			// Push edited value back into state on each tick. Auto-save.
			if v := m.SelectedVariable(); v != nil {
				if m.shouldApplyEditorValue(v) {
					m.applyEditorValue(v, e2.CurrentValue())
					// Schedule debounced validate after each edit.
					if valCmd := m.scheduleValidate(); valCmd != nil {
						cmd = tea.Batch(cmd, valCmd)
					}
				}
			}
		}
		return m, cmd
	}
	return m, nil
}

// shouldApplyEditorValue decides whether the live editor value should be
// pushed into state on this tick. A variable wired to a preserved expression
// (a reference Atelier can't model) is treated as read-only until the user
// actually edits the field, so merely focusing or navigating into it doesn't
// clobber the expression with an empty/placeholder value. Once the user types,
// the editor reports Touched() and the concrete value takes over (which also
// drops the preserved expression via applyEditorValue).
func (m *Model) shouldApplyEditorValue(v *tfvars.Variable) bool {
	if _, wired := m.ActiveModuleState().WiredExpression(v.Name); !wired {
		return true
	}
	if tr, ok := m.editor.(interface{ Touched() bool }); ok {
		return tr.Touched()
	}
	// No way to tell whether a complex wired editor was edited; stay
	// read-only to avoid clobbering the expression.
	return false
}

// handlePlanKey routes keys while the plan view is active. The tree owns
// navigation and collapse/expand; Esc returns to the editor (state is kept
// so re-pressing P refreshes rather than starts cold). Tab toggles focus
// between the tree and the diff pane.
func (m *Model) handlePlanKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// When the diff pane is focused, handle its keys first.
	if m.planDiffFocus {
		return m.handlePlanDiffKey(msg)
	}

	switch msg.String() {
	case "esc", "q":
		m.planState = planIdle
		m.planDiffFocus = false
		return m, nil
	case "tab":
		// Switch focus to the diff pane.
		m.planDiffFocus = true
		return m, nil
	case "s", "S":
		// Toggle between diff view and state values view.
		if m.tfState != nil && m.stateTree != nil {
			m.planShowState = !m.planShowState
			m.planCursor = 0
			m.planScroll = 0
			m.planDiffScroll = 0
		}
		return m, nil
	case "p", "P":
		// Re-run plan.
		m.planState = planLoading
		m.planErr = ""
		return m, tea.Batch(m.startPlan(), spinnerTick())
	case "a", "A":
		// Apply the current plan.
		if m.Applier != nil && m.applyState != applyLoading {
			m.applyState = applyLoading
			m.applyErr = ""
			return m, tea.Batch(m.startApply(), spinnerTick())
		}
	case "e", "E":
		// Open error detail modal when an error is present.
		if m.statusLvl == statusError && m.statusDetail != "" {
			m.errorDetail = true
			return m, nil
		}
	case "up", "k":
		m.movePlanCursor(-1)
		m.planDiffScroll = 0 // reset diff scroll on cursor move
	case "down", "j":
		m.movePlanCursor(+1)
		m.planDiffScroll = 0 // reset diff scroll on cursor move
	case "pgup", "ctrl+u":
		m.movePlanCursor(-m.planPanelHeight() / 2)
		m.planDiffScroll = 0
	case "pgdown", "ctrl+d":
		m.movePlanCursor(m.planPanelHeight() / 2)
		m.planDiffScroll = 0
	case "g":
		m.movePlanCursor(-maxInt)
		m.planDiffScroll = 0
	case "G":
		m.movePlanCursor(maxInt)
		m.planDiffScroll = 0
	case "enter", " ", "space", "right", "left", "l", "h":
		// Toggle collapse on the focused row when it has children.
		rows := flattenedRows(m.activeTree())
		if m.planCursor >= 0 && m.planCursor < len(rows) {
			n := rows[m.planCursor].Node
			if len(n.Children) > 0 {
				n.Collapsed = !n.Collapsed
			}
		}
	}
	return m, nil
}

// handlePlanDiffKey handles keys when the diff pane is focused.
// ↑↓/j/k scroll the diff; Tab/Esc return focus to the tree.
func (m *Model) handlePlanDiffKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "esc":
		m.planDiffFocus = false
		return m, nil
	case "q":
		// q exits the plan view entirely.
		m.planState = planIdle
		m.planDiffFocus = false
		return m, nil
	case "up", "k":
		if m.planDiffScroll > 0 {
			m.planDiffScroll--
		}
	case "down", "j":
		m.planDiffScroll++
	case "pgup", "ctrl+u":
		m.planDiffScroll -= m.planPanelHeight() / 2
		if m.planDiffScroll < 0 {
			m.planDiffScroll = 0
		}
	case "pgdown", "ctrl+d":
		m.planDiffScroll += m.planPanelHeight() / 2
	case "g":
		m.planDiffScroll = 0
	case "G":
		m.planDiffScroll = maxInt // clamped at render time
	}
	return m, nil
}

func (m *Model) movePlanCursor(delta int) {
	rows := flattenedRows(m.activeTree())
	if len(rows) == 0 {
		return
	}
	m.planCursor = clampCursor(rows, m.planCursor+delta)
}

// SelectedPlanChange returns the resource_change under the plan cursor, or
// nil when the cursor is on a non-leaf row (module/type) or no plan exists.
func (m *Model) SelectedPlanChange() *tfjson.ResourceChange {
	if m.planState != planReady {
		return nil
	}
	rows := flattenedRows(m.activeTree())
	if m.planCursor < 0 || m.planCursor >= len(rows) {
		return nil
	}
	n := rows[m.planCursor].Node
	if n.Kind != nodeResource {
		return nil
	}
	return n.Change
}

// activeTree returns the tree currently displayed in the left pane.
func (m *Model) activeTree() *planNode {
	if m.planShowState && m.stateTree != nil {
		return m.stateTree
	}
	return m.planTree
}

// handlePresetKey routes keys while the preset picker overlay is visible.
func (m *Model) handlePresetKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.presetPicker = false
		return m, nil
	case "up", "k":
		if m.presetCursor > 0 {
			m.presetCursor--
		}
		return m, nil
	case "down", "j":
		if m.presetCursor < len(m.presets)-1 {
			m.presetCursor++
		}
		return m, nil
	case "enter":
		cmd := m.applyPresetCmd(m.presetCursor)
		m.presetPicker = false
		return m, cmd
	}
	return m, nil
}

// applyPreset applies the preset at index i: merges its values into
// state.Values, refreshes the editor, and flashes a status message.
func (m *Model) applyPreset(i int) {
	if i < 0 || i >= len(m.presets) {
		return
	}
	p := m.presets[i]
	for name, val := range p.Values {
		m.State.Values[name] = val
		// The preset value supersedes any reference expression on this var,
		// but we keep the preserved raw form so a later reset can restore it.
	}
	m.refreshEditor()
	m.status = fmt.Sprintf("Applied preset: %s", p.Name)
	m.statusLvl = statusInfo
	m.statusAt = time.Now()
	m.dirty = true
}

// applyPresetCmd wraps applyPreset and returns a validate debounce command.
func (m *Model) applyPresetCmd(i int) tea.Cmd {
	m.applyPreset(i)
	return m.scheduleValidate()
}

// handleRefModalKey routes keys while the ref input prompt is visible.
func (m *Model) handleRefModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.refModal = false
		return m, nil
	case tea.KeyEnter:
		newRef := strings.TrimSpace(m.refInput)
		_, _, curRef, _ := m.activeRefInfo()
		if newRef == "" || newRef == curRef {
			m.refModal = false
			return m, nil
		}
		m.refModal = false
		m.refSwitching = true
		m.refErr = ""
		m.status = ""
		return m, tea.Batch(m.startRefSwitch(newRef), spinnerTick())
	case tea.KeyBackspace:
		if len(m.refInput) > 0 {
			m.refInput = m.refInput[:len(m.refInput)-1]
		}
		return m, nil
	case tea.KeyCtrlU:
		m.refInput = ""
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.refInput += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

// applyRefSwitch merges a successful ref switch result into the model,
// preserving user overrides and recording orphaned variables. The switch is
// applied to the module captured in refModuleIdx (the one the user invoked R
// on), which may be the primary or any secondary module.
func (m *Model) applyRefSwitch(result *RefSwitchResult) {
	m.refSwitching = false
	m.refOrphaned = result.OrphanedVars

	idx := m.refModuleIdx
	if idx < 0 || idx >= len(m.Modules) {
		idx = 0
	}
	entry := &m.Modules[idx]

	// Carry over user overrides for variables that still exist in the new ref.
	// Two kinds of override must survive a ref switch:
	//   1. concrete values   -> entry.State.Values
	//   2. wired expressions  -> entry.State.UnknownAttrs (e.g.
	//      model_uuid = data.juju_model.x.uuid). These do NOT live in Values,
	//      so failing to carry them over silently deletes the reference from
	//      the rendered module block on the next write.
	// Overrides for variables that no longer exist in the new ref are
	// intentionally dropped (reported as orphaned).
	oldState := entry.State
	newState := result.State
	if newState.Values == nil {
		newState.Values = make(map[string]cty.Value)
	}
	newVarNames := make(map[string]bool, len(newState.Vars))
	for _, v := range newState.Vars {
		newVarNames[v.Name] = true
	}
	for name, val := range oldState.Values {
		if newVarNames[name] {
			newState.Values[name] = val
		}
	}
	if len(oldState.UnknownAttrs) > 0 {
		carried := make([]wrapper.RawAttr, 0, len(oldState.UnknownAttrs))
		for _, ra := range oldState.UnknownAttrs {
			if newVarNames[ra.Name] {
				carried = append(carried, ra)
			}
		}
		newState.UnknownAttrs = carried
	}

	// Write the new state back into the owning entry. This is the single
	// source of truth used by writeAllModules — failing to update it here is
	// what previously caused a switched ref to silently revert on the next
	// save.
	entry.State = newState
	entry.Ref = result.LiteralRef
	entry.ResolvedSHA = result.ResolvedSHA

	// Keep the primary alias and legacy header fields coherent.
	if idx == 0 {
		m.State = newState
		m.LiteralRef = result.LiteralRef
		m.ResolvedSHA = result.ResolvedSHA
	}

	// Rebuild the UI.
	m.recomputeRows()

	// When the new ref introduced variables, land the cursor on the first
	// one the user must act on — a new *required* var (no default) takes
	// priority, falling back to the first new var. This turns a breaking
	// API change (e.g. model_uuid -> model) into a guided edit instead of a
	// cryptic "Missing required argument" the user has to hunt for.
	if focus := firstActionableNewVar(result.NewVars); focus != "" {
		m.focusVar(idx, focus)
	}
	m.refreshEditor()
	m.dirty = true

	// Reset planner init state so the next plan re-checks modules.
	if p, ok := m.Planner.(*TfexecPlanner); ok {
		p.ResetInit()
	}

	// Status message.
	msg := fmt.Sprintf("Switched %s to ref: %s (%s)", entry.Name, result.LiteralRef, shortSHA(result.ResolvedSHA))
	if len(result.OrphanedVars) > 0 {
		names := strings.Join(result.OrphanedVars, ", ")
		msg += fmt.Sprintf(" · %d orphaned: %s", len(result.OrphanedVars), names)
	}
	if len(result.NewVars) > 0 {
		names := make([]string, len(result.NewVars))
		for i, v := range result.NewVars {
			names[i] = v.Name
		}
		msg += fmt.Sprintf(" · %d new: %s", len(result.NewVars), strings.Join(names, ", "))
	}
	// Report the actionable condition as a neutral fact, not a procedure: the
	// [!] markers and the auto-jumped cursor already show the user where to
	// act. A required-unset count explains a non-fatal init failure; if init
	// failed for some other reason, say only that it is incomplete.
	lvl := statusInfo
	if n := requiredUnsetCount(entry.State); n > 0 {
		msg += fmt.Sprintf(" · %d required unset", n)
		lvl = statusWarn
	} else if result.InitIncomplete {
		msg += " · init incomplete"
		lvl = statusWarn
	}
	m.status = msg
	m.statusLvl = lvl
	m.statusAt = time.Now()
}

// requiredUnsetCount returns how many of the module's variables are required
// (no default) but have neither a concrete value nor a wired expression — the
// same condition the [!] marker reports per row.
func requiredUnsetCount(st *wrapper.State) int {
	if st == nil {
		return 0
	}
	n := 0
	for _, v := range st.Vars {
		if v.HasDefault {
			continue
		}
		if _, wired := st.WiredExpression(v.Name); wired {
			continue
		}
		cur, present := st.Values[v.Name]
		if !present || cur == cty.NilVal {
			n++
		}
	}
	return n
}

// firstActionableNewVar returns the name of the variable the user should be
// taken to after a ref switch: the first new required variable (no default),
// or the first new variable if all have defaults, or "" if there are none.
func firstActionableNewVar(newVars []tfvars.Variable) string {
	if len(newVars) == 0 {
		return ""
	}
	for _, v := range newVars {
		if !v.HasDefault {
			return v.Name
		}
	}
	return newVars[0].Name
}

// focusVar moves the cursor to the row owning variable `name` in module
// `moduleIdx`, adjusting scroll so it is visible. No-op if not found.
func (m *Model) focusVar(moduleIdx int, name string) {
	for i, r := range m.rows {
		if r.IsHeader || r.VarName != name {
			continue
		}
		if len(m.Modules) > 1 && r.ModuleIdx != moduleIdx {
			continue
		}
		m.cursor = i
		m.scrollToCursor()
		return
	}
}

func (m *Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(+1)
	case "pgup", "ctrl+u":
		m.moveCursor(-m.leftPaneVisibleRows() / 2)
	case "pgdown", "ctrl+d":
		m.moveCursor(m.leftPaneVisibleRows() / 2)
	case "home", "g":
		m.moveCursor(-len(m.rows))
	case "end", "G":
		m.moveCursor(len(m.rows))
	case "enter", "right", "l":
		m.setFocus(focusRight)
		return m, nil
	}
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	if len(m.rows) == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	c := m.cursor
	for i := 0; i < abs(delta); i++ {
		c += step
		// Skip over header rows.
		for c >= 0 && c < len(m.rows) && m.rows[c].IsHeader {
			c += step
		}
		if c < 0 || c >= len(m.rows) {
			return
		}
	}
	m.cursor = c
	m.scrollToCursor()
	m.refreshEditor()
}

// skipHeader advances the cursor past a header in the given direction.
func (m *Model) skipHeader(dir int) {
	for m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].IsHeader {
		m.cursor += dir
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
}

// scrollToCursor adjusts leftScroll so the cursor is visible within the
// left pane's height.
func (m *Model) scrollToCursor() {
	visible := m.leftPaneVisibleRows()
	if visible <= 0 {
		return
	}
	if m.cursor < m.leftScroll {
		m.leftScroll = m.cursor
	} else if m.cursor >= m.leftScroll+visible {
		m.leftScroll = m.cursor - visible + 1
	}
}

// leftPaneVisibleRows returns how many variable rows fit in the left pane.
func (m *Model) leftPaneVisibleRows() int {
	return m.panelHeight()
}

func (m *Model) applyEditorValue(v *tfvars.Variable, val cty.Value) {
	st := m.ActiveModuleState()
	if st.Values == nil {
		st.Values = map[string]cty.Value{}
	}
	if val == cty.NilVal {
		delete(st.Values, v.Name)
	} else {
		st.Values[v.Name] = val
		// A concrete value supersedes any reference expression the variable
		// was originally wired to (both the [→] display and the writer prefer
		// Values when present), but we deliberately KEEP the preserved raw
		// form so a later Ctrl+R can restore the original reference instead of
		// leaving the variable empty.
	}
	m.dirty = true
}

// resetCurrent restores the user's current focus point to its declared
// default. The behaviour is contextual:
//
//   - Focus on the right pane with an object editor open: reset the single
//     focused field of that object. The other fields keep their current
//     values.
//   - Anywhere else: reset the whole selected variable. The entry is
//     removed from state.Values so the sparse-write rule treats it as
//     at-default (and the variable's [ ] marker re-appears in the left
//     pane).
//
// In both cases the editor is rebuilt so what the user sees on screen
// matches state.
func (m *Model) resetCurrent() {
	v := m.SelectedVariable()
	if v == nil {
		return
	}

	if m.focus == focusRight {
		if oe, ok := m.editor.(*objectEditor); ok {
			oe.ResetFocused()
			m.applyEditorValue(v, oe.CurrentValue())
			m.status = "field reset to default"
			m.statusLvl = statusInfo
			return
		}
	}

	// Whole-variable reset.
	st := m.ActiveModuleState()
	delete(st.Values, v.Name)
	// We do NOT drop any preserved reference expression here. Removing the
	// concrete override lets the original wiring resurface, so a variable that
	// was read as e.g. `data.vault_generic_secret.s3.data["..."]` returns to
	// its [→] view instead of becoming an empty/required field.
	m.dirty = true
	m.refreshEditor()
	if _, wired := st.WiredExpression(v.Name); wired {
		m.status = "variable reset to original reference"
	} else {
		m.status = "variable reset to default"
	}
	m.statusLvl = statusInfo
}

// SaveIfDirty writes the state to disk if there were pending edits. Returns
// the error from the write, or nil. Exposed so the runtime layer (cmd) can
// run a final flush before exit.
func (m *Model) SaveIfDirty() error {
	if !m.dirty {
		return nil
	}
	if err := writeAllModules(m.Modules); err != nil {
		return err
	}
	m.dirty = false
	return nil
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		// First render before WindowSize arrives.
		return "Loading…"
	}

	if m.helpModal {
		return m.renderHelpModal()
	}
	if m.errorDetail {
		return m.renderErrorDetail()
	}
	if m.planState == planReady {
		return m.renderPlanScreen()
	}
	if m.presetPicker {
		return m.renderPresetPicker()
	}
	if m.refModal || m.refSwitching {
		return m.renderRefModal()
	}
	left := m.renderLeftPane()
	right := m.renderRightPane()
	header := m.renderHeader()
	footer := m.renderFooter()

	body := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// writeAllModules writes the state for every module in the session.
// Each module's State.Write() is idempotent on its own block in main.tf.
func writeAllModules(modules []ModuleEntry) error {
	for _, mod := range modules {
		if mod.State != nil {
			if err := mod.State.Write(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Format helper: short SHA.
func shortSHA(sha string) string {
	if len(sha) >= 7 {
		return sha[:7]
	}
	return sha
}

// moduleLabel renders a module's display name with its git-ref pin, if any.
// Unpinned modules render as the bare name (no SHA, no synthesized branch).
func moduleLabel(name, ref string) string {
	if ref == "" {
		return name
	}
	return name + "@" + ref
}

// unpinnedMarker is the dim, non-ref affordance shown beside a remote module
// that carries no git pin. It is a word (never a sigil or sha-like token) so
// it cannot be mistaken for a ref, and it is only ever shown for pinnable
// (remote) modules — local sources have nothing to pin (ADR-0019 amendment).
const unpinnedMarker = "·unpinned"

// stub used by view: format kind label briefly.
func kindLabel(t *tftypes.Type) string {
	if t == nil {
		return "any"
	}
	return t.Kind.String()
}

// Module-info banner used by the status line. Single-module sessions render
// "Module: <token>"; multi-module sessions add the active module's position
// ("Module 2/3: …") so the banner carries information the per-group section
// headers cannot. Unpinned remote modules get a dim "unpinned" affordance;
// local (unpinnable) modules render as a bare name (ADR-0019).
func (m *Model) moduleBanner() string {
	name, source, ref, _ := m.activeRefInfo()
	if name == "" {
		return ""
	}
	prefix := "Module: "
	if n := len(m.Modules); n > 1 {
		prefix = fmt.Sprintf("Module %d/%d: ", m.activeModuleIdx()+1, n)
	}
	label := moduleLabel(name, ref)
	if ref == "" && source != "" {
		label += " " + styleUnpinnedTag.Render(unpinnedMarker)
	}
	return prefix + label
}

// formatValidateDiagnostics renders validate diagnostics into a multi-line
// string suitable for the error detail modal.
func formatValidateDiagnostics(vo *tfjson.ValidateOutput) string {
	if vo == nil || len(vo.Diagnostics) == 0 {
		return ""
	}
	var b strings.Builder
	for i, d := range vo.Diagnostics {
		if i > 0 {
			fmt.Fprintln(&b)
		}
		sev := "Error"
		if d.Severity == "warning" {
			sev = "Warning"
		}
		fmt.Fprintf(&b, "%s: %s", sev, d.Summary)
		if d.Detail != "" {
			fmt.Fprintf(&b, "\n  %s", d.Detail)
		}
	}
	return b.String()
}
