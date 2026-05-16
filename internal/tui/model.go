// Package tui implements Atelier's Bubble Tea TUI.
//
// The TUI is a two-pane layout (left=variable list, right=editor) with a
// status pane at the bottom (SPEC §7, ADR-0006). The top-level Model owns
// the wrapper.State and routes input to the active editor based on the
// selected variable's type.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"

	"github.com/canonical/atelier/internal/manifest"
	"github.com/canonical/atelier/internal/tftypes"
	"github.com/canonical/atelier/internal/tfvars"
	"github.com/canonical/atelier/internal/wrapper"
)

// Model is the top-level TUI model.
type Model struct {
	State *wrapper.State
	// Module display info shown in the status bar.
	ModuleName   string
	LiteralRef   string
	ResolvedSHA  string
	ManifestPath string

	groups []manifest.ResolvedGroup
	rows   []rowEntry

	cursor       int
	focus        focusPane
	width, height int

	// editor is the active right-pane editor, type-specific per the
	// currently selected variable.
	editor Editor

	// status text shown at the bottom. Cleared when a new edit lands.
	status     string
	statusLvl  statusLevel
	statusAt   time.Time

	// Planner runs `terraform plan` asynchronously when the user presses P.
	// May be nil (e.g. in tests or read-only contexts); the P key just
	// produces a friendly status message in that case.
	Planner Planner

	// plan tree + cursor when planState == planReady.
	planState    planState
	plan         *tfjson.Plan
	planTree     *planNode
	planCursor   int
	planErr      string
	planSpinnerFrame int

	// quitSignal: when set, the runtime tea.Quit will follow.
	quit bool

	// dirty tracks whether we have unsaved in-memory edits. Used by the
	// auto-save path that ends each Update tick.
	dirty bool
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

// spinnerFrames is the visible animation for the in-flight plan indicator.
// The Braille-octant set is widely supported and reads well on dark and
// light backgrounds without colour.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

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

// rowEntry is one row in the left pane: either a group header or a variable.
type rowEntry struct {
	IsGroup  bool
	GroupIdx int
	VarName  string
}

// New builds a Model around a wrapper.State.
func New(state *wrapper.State, modName string) *Model {
	m := &Model{
		State:      state,
		ModuleName: modName,
	}
	m.recomputeRows()
	m.refreshEditor()
	return m
}

// SetGroups installs a manifest-driven group ordering. If empty / nil, the
// model falls back to a single unnamed group of all variables.
func (m *Model) SetGroups(g []manifest.ResolvedGroup) {
	m.groups = g
	m.recomputeRows()
	m.refreshEditor()
}

func (m *Model) recomputeRows() {
	var rows []rowEntry
	if len(m.groups) == 0 {
		// Default: one anonymous group of all declared variables in
		// declaration order.
		for _, v := range m.State.Vars {
			rows = append(rows, rowEntry{VarName: v.Name})
		}
	} else {
		for gi, g := range m.groups {
			rows = append(rows, rowEntry{IsGroup: true, GroupIdx: gi})
			for _, name := range g.Variables {
				rows = append(rows, rowEntry{VarName: name})
			}
		}
	}
	m.rows = rows
	if m.cursor >= len(rows) {
		m.cursor = len(rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	// Cursor should land on a variable, not a header.
	for m.cursor < len(rows) && rows[m.cursor].IsGroup {
		m.cursor++
	}
}

// SelectedVariable returns the variable currently under the cursor, or nil
// if the cursor is on a group header / out of range.
func (m *Model) SelectedVariable() *tfvars.Variable {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	r := m.rows[m.cursor]
	if r.IsGroup {
		return nil
	}
	return m.State.FindVar(r.VarName)
}

func (m *Model) refreshEditor() {
	v := m.SelectedVariable()
	if v == nil {
		m.editor = nil
		return
	}
	current, _ := m.State.VariableValue(v.Name)
	m.editor = newEditor(v, current)
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
	state := m.State
	planner := m.Planner
	return func() tea.Msg {
		if state != nil {
			if err := state.Write(); err != nil {
				return planErrorMsg{err: fmt.Errorf("save wrapper: %w", err)}
			}
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

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
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
		m.planCursor = 0
		m.planState = planReady
		m.planErr = ""
		m.status = ""
		return m, nil
	case planErrorMsg:
		m.planState = planIdle
		m.planErr = msg.err.Error()
		m.status = "plan failed: " + msg.err.Error()
		m.statusLvl = statusError
		m.statusAt = time.Now()
		return m, nil
	case spinnerTickMsg:
		if m.planState == planLoading {
			m.planSpinnerFrame = (m.planSpinnerFrame + 1) % len(spinnerFrames)
			return m, spinnerTick()
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

	switch msg.String() {
	case "q":
		if m.focus == focusLeft {
			m.quit = true
			return m, tea.Quit
		}
	case "tab":
		if m.focus == focusLeft {
			m.focus = focusRight
		} else {
			m.focus = focusLeft
		}
		return m, nil
	case "esc":
		m.focus = focusLeft
		return m, nil
	case "p", "P":
		// Trigger the plan from anywhere in the normal mode.
		m.planState = planLoading
		m.planErr = ""
		m.status = ""
		return m, tea.Batch(m.startPlan(), spinnerTick())
	case "ctrl+r":
		m.resetCurrent()
		return m, nil
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
				m.applyEditorValue(v, e2.CurrentValue())
			}
		}
		return m, cmd
	}
	return m, nil
}

// handlePlanKey routes keys while the plan view is active. The tree owns
// navigation and collapse/expand; Esc returns to the editor (state is kept
// so re-pressing P refreshes rather than starts cold).
func (m *Model) handlePlanKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.planState = planIdle
		return m, nil
	case "p", "P":
		// Re-run plan.
		m.planState = planLoading
		m.planErr = ""
		return m, tea.Batch(m.startPlan(), spinnerTick())
	case "up", "k":
		m.movePlanCursor(-1)
	case "down", "j":
		m.movePlanCursor(+1)
	case "enter", " ", "space", "right", "left", "l", "h":
		// Toggle collapse on the focused row when it has children.
		rows := flattenedRows(m.planTree)
		if m.planCursor >= 0 && m.planCursor < len(rows) {
			n := rows[m.planCursor].Node
			if len(n.Children) > 0 {
				n.Collapsed = !n.Collapsed
			}
		}
	}
	return m, nil
}

func (m *Model) movePlanCursor(delta int) {
	rows := flattenedRows(m.planTree)
	if len(rows) == 0 {
		return
	}
	m.planCursor = clampCursor(rows, m.planCursor+delta)
}

// SelectedPlanChange returns the resource_change under the plan cursor, or
// nil when the cursor is on a non-leaf row (module/type) or no plan exists.
func (m *Model) SelectedPlanChange() *tfjson.ResourceChange {
	if m.planState != planReady || m.planTree == nil {
		return nil
	}
	rows := flattenedRows(m.planTree)
	if m.planCursor < 0 || m.planCursor >= len(rows) {
		return nil
	}
	n := rows[m.planCursor].Node
	if n.Kind != nodeResource {
		return nil
	}
	return n.Change
}

func (m *Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(+1)
	case "enter", "right", "l":
		m.focus = focusRight
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
		if c < 0 || c >= len(m.rows) {
			return
		}
		// Skip group headers.
		for c >= 0 && c < len(m.rows) && m.rows[c].IsGroup {
			c += step
		}
		if c < 0 || c >= len(m.rows) {
			return
		}
	}
	m.cursor = c
	m.refreshEditor()
}

func (m *Model) applyEditorValue(v *tfvars.Variable, val cty.Value) {
	if m.State.Values == nil {
		m.State.Values = map[string]cty.Value{}
	}
	if val == cty.NilVal {
		delete(m.State.Values, v.Name)
	} else {
		m.State.Values[v.Name] = val
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
	delete(m.State.Values, v.Name)
	m.dirty = true
	m.refreshEditor()
	m.status = "variable reset to default"
	m.statusLvl = statusInfo
}

// SaveIfDirty writes the state to disk if there were pending edits. Returns
// the error from the write, or nil. Exposed so the runtime layer (cmd) can
// run a final flush before exit.
func (m *Model) SaveIfDirty() error {
	if !m.dirty || m.State == nil {
		return nil
	}
	if err := m.State.Write(); err != nil {
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

	if m.planState == planReady {
		return m.renderPlanScreen()
	}

	left := m.renderLeftPane()
	right := m.renderRightPane()
	status := m.renderStatus()

	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	return lipgloss.JoinVertical(lipgloss.Left, body, status)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Format helper: short SHA.
func shortSHA(sha string) string {
	if len(sha) >= 7 {
		return sha[:7]
	}
	return sha
}

// stub used by view: format kind label briefly.
func kindLabel(t *tftypes.Type) string {
	if t == nil {
		return "any"
	}
	return t.Kind.String()
}

// Module-info banner used by the status line.
func (m *Model) moduleBanner() string {
	parts := []string{}
	if m.ModuleName != "" {
		parts = append(parts, fmt.Sprintf("Module: %s", m.ModuleName))
	}
	if m.LiteralRef != "" {
		parts = append(parts, fmt.Sprintf("ref %s", m.LiteralRef))
	}
	if m.ResolvedSHA != "" {
		parts = append(parts, fmt.Sprintf("(%s)", shortSHA(m.ResolvedSHA)))
	}
	return strings.Join(parts, " ")
}
