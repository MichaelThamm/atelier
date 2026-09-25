// Package tui implements Atelier's Bubble Tea TUI.
//
// The TUI is a two-pane layout (left=variable list, right=editor) with a
// status pane at the bottom (SPEC §7, ADR-0006). The top-level Model owns
// the wrapper.State and routes input to the active editor based on the
// selected variable's type.
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	tfjson "github.com/hashicorp/terraform-json"

	"github.com/MichaelThamm/atelier/internal/state"
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

	// TFVarsMode marks the opt-in pass-through wrapper shape (ADR-0031):
	// values live in terraform.tfvars, not in main.tf's module block. The
	// header shows a persistent chip so the shape is never a mystery.
	TFVarsMode bool

	// WrapperDir is the directory containing main.tf and terraform.tfstate.
	// Used for reloading state after apply.
	WrapperDir string

	rows []rowEntry

	cursor        int
	leftScroll    int // scroll offset (first visible row) in the left pane
	focus         focusPane
	width, height int

	// editor is the active right-pane editor, type-specific per the
	// currently selected variable.
	editor       Editor
	editorScroll int // scroll offset for the right pane content

	// activeView switches the body between the editor and live logs.
	activeView viewMode
	logScroll  int // scroll offset for the logs view

	// logAutoScroll is true while the logs view should follow new output.
	// Disabled when the user scrolls up, re-enabled on G/end or re-entering.
	logAutoScroll bool

	// logsTab tracks which tab is active in the unified logs view
	logsTab logsTabMode

	// status text shown at the bottom. Cleared when a new edit lands.
	status       string
	statusLvl    statusLevel
	statusAt     time.Time
	statusDetail string // full multi-line error shown in the logs view

	// checkWarnings holds failed `check` block assertions from the most
	// recent plan. Populated on planResultMsg, cleared on the next plan/apply.
	// These are advisory (they never block plan/apply), so they are surfaced
	// separately from the error path via the [W] warnings detail modal.
	checkWarnings []CheckWarning
	warnDetail    bool // true when the check-warnings detail modal is visible

	// refDetail holds the full multi-line summary from a completed ref
	// switch (orphaned vars, new vars, init status). Shown via [D] from the
	// footer; cleared on the next ref switch.
	refDetail     bool
	refDetailText string

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
	planState        planState
	plan             *tfjson.Plan
	planTree         *planNode
	stateTree        *planNode // state resources as a tree (for browsing when no changes)
	planCursor       int
	planScroll       int  // scroll offset for the plan tree pane
	planDiffScroll   int  // scroll offset for the plan diff pane
	planDiffFocus    bool // true when the diff pane is focused (Tab toggle)
	planShowState    bool // true when left+right panes show state instead of diff
	planErr          string
	planSpinnerFrame int
	progress         *ProgressTracker // live progress from terraform subprocess

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

	// presets holds resolved `.tfvars` bundles (personal walk-up + repo
	// examples). When non-empty, the user can press F to open the picker.
	presets      []ResolvedPreset
	presetPicker bool // true when the picker overlay is visible
	presetCursor int  // cursor within the picker list

	// savePreset modal state: captures the current wrapper configuration into
	// a new atelier.presets/<name>.tfvars bundle (ADR-0032). The snapshot is
	// taken when the modal opens; name and description are collected via two
	// readline cells.
	savePresetModal bool
	savePresetName  cellInput
	savePresetDesc  cellInput
	savePresetFocus int            // 0 = name field, 1 = description field
	savePresetSets  map[string]any // snapshotted non-default values (var -> value)

	// RefSwitcher handles the backend logic of switching module refs.
	// May be nil (e.g., local source wrappers where ref switching is N/A).
	RefSwitcher RefSwitcher

	// refModal state: tracks the ref-switch prompt and in-flight switch.
	refModal     bool      // true when the ref input prompt is visible
	refInput     cellInput // canonical readline cell for the ref input field (ADR-0020/0025)
	refSwitching bool      // true when a ref switch is in flight (spinner)
	refErr       string    // error from last ref switch attempt
	refOrphaned  []string  // vars that no longer exist after a ref switch
	refModuleIdx int       // index of the module being ref-switched

	// availableRefs holds the remote's current ref names for the module the
	// modal targets, populated asynchronously via RefSwitcher.ListRefs and
	// shown as a filterable, selectable list under the input (ADR-0025).
	// refsLoading tracks the in-flight fetch. refMatches is the current
	// substring-filtered, prefix-first view of availableRefs and refMatchCursor
	// is the highlighted row within it.
	availableRefs  []string
	refsLoading    bool
	refMatches     []string
	refMatchCursor int

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

// viewMode controls which body content is displayed in the default layout.
type viewMode int

const (
	viewEditor viewMode = iota
	viewLogs
)

// logsTabMode controls which tab is active in the unified logs view.
type logsTabMode int

const (
	logsTabErrors logsTabMode = iota // default: show stderr
	logsTabLogs                      // show stdout
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

// SetPresets installs resolved `.tfvars` presets. When non-empty, the user can
// press F from the left pane to open the preset picker.
func (m *Model) SetPresets(p []ResolvedPreset) {
	m.presets = p
}

// SetTFVarsMode marks the wrapper as the opt-in pass-through shape
// (ADR-0031). The header then carries a persistent `tfvars` chip and the
// footer opens with a one-line explanation of where values live, so the
// generated forwards in main.tf aren't mistaken for user wiring.
func (m *Model) SetTFVarsMode(on bool) {
	m.TFVarsMode = on
	if on {
		m.flashStatus("tfvars mode — values live in terraform.tfvars", statusInfo)
	}
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
		m.refInput = newCellInput(ref, false, "")
		m.availableRefs = m.RefUnresolved.Available
		m.refMatchCursor = 0
		m.refreshRefMatches()
		if !m.RefUnresolved.Offline {
			m.refsLoading = true
			return m.startListRefs()
		}
	}
	return nil
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
	if m.warnDetail {
		return m.renderWarnDetail()
	}
	if m.refDetail {
		return m.renderRefDetail()
	}
	if m.activeView == viewLogs {
		header := m.renderHeader()
		logs := m.renderLogsView()
		footer := m.renderFooter()
		return lipgloss.JoinVertical(lipgloss.Left, header, logs, footer)
	}
	if m.planState == planReady {
		return m.renderPlanScreen()
	}
	if m.presetPicker {
		return m.renderPresetPicker()
	}
	if m.savePresetModal {
		return m.renderSavePresetModal()
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
