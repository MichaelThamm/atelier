// Package tui implements Atelier's Bubble Tea TUI.
//
// The TUI is a two-pane layout (left=variable list, right=editor) with a
// status pane at the bottom (SPEC §7, ADR-0006). The top-level Model owns
// the wrapper.State and routes input to the active editor based on the
// selected variable's type.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	uptfexec "github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// Model is the top-level TUI model.
type Model struct {
	State *wrapper.State
	// Module display info shown in the status bar.
	ModuleName   string
	LiteralRef   string
	ResolvedSHA  string
	SourceURL    string
	ManifestPath string

	rows   []rowEntry

	cursor       int
	focus        focusPane
	width, height int

	// editor is the active right-pane editor, type-specific per the
	// currently selected variable.
	editor Editor

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

	// OutputProvider fetches terraform outputs after apply or on demand.
	// May be nil; the O key is hidden if unset.
	OutputProvider OutputProvider

	// plan tree + cursor when planState == planReady.
	planState    planState
	plan         *tfjson.Plan
	planTree     *planNode
	planCursor   int
	planErr      string
	planSpinnerFrame int

	// applyState tracks the apply flow (idle → loading → done/error).
	applyState applyState
	applyErr   string

	// outputs holds the result of `terraform output -json` fetched after
	// apply or on demand via O key. Displayed until dismissed.
	outputs      map[string]uptfexec.OutputMeta
	outputsReady bool // true when the output view is showing
	outputScroll int  // scroll offset (lines) in output view

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

	helpModal bool // true when the [?] help overlay is visible
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

// rowEntry is one row in the left pane: a variable.
type rowEntry struct {
	VarName string
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

// SetPresets installs resolved presets from the manifest. When non-empty,
// the user can press F from the left pane to open the preset picker.
func (m *Model) SetPresets(p []ResolvedPreset) {
	m.presets = p
}

func (m *Model) recomputeRows() {
	var rows []rowEntry
	for _, v := range m.State.Vars {
		rows = append(rows, rowEntry{VarName: v.Name})
	}
	m.rows = rows
	if m.cursor >= len(rows) {
		m.cursor = len(rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// SelectedVariable returns the variable currently under the cursor, or nil
// if out of range.
func (m *Model) SelectedVariable() *tfvars.Variable {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	r := m.rows[m.cursor]
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

// refSwitchResultMsg carries a successful ref switch back to the UI thread.
type refSwitchResultMsg struct {
	result *RefSwitchResult
}

// refSwitchErrorMsg carries a ref-switch failure back to the UI thread.
type refSwitchErrorMsg struct {
	err error
}

// applyResultMsg signals a successful terraform apply.
type applyResultMsg struct{}

// applyErrorMsg carries an apply failure back to the UI thread.
type applyErrorMsg struct {
	err error
}

// outputResultMsg carries terraform output data back to the UI thread.
type outputResultMsg struct {
	outputs map[string]uptfexec.OutputMeta
}

// outputErrorMsg signals that fetching outputs failed (non-fatal).
type outputErrorMsg struct {
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

// startApply runs `terraform apply` using the cached plan file.
func (m *Model) startApply() tea.Cmd {
	if m.Applier == nil {
		return func() tea.Msg {
			return applyErrorMsg{err: fmt.Errorf("apply unavailable: applier not configured")}
		}
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

// startFetchOutputs runs `terraform output -json` asynchronously.
func (m *Model) startFetchOutputs() tea.Cmd {
	if m.OutputProvider == nil {
		return nil
	}
	provider := m.OutputProvider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := provider.Output(ctx)
		if err != nil {
			return outputErrorMsg{err: err}
		}
		return outputResultMsg{outputs: out}
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
	state := m.State
	validator := m.Validator
	return func() tea.Msg {
		if state != nil {
			if err := state.Write(); err != nil {
				return validateErrorMsg{err: fmt.Errorf("save wrapper: %w", err)}
			}
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
	switcher := m.RefSwitcher
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
		m.statusDetail = msg.err.Error()
		m.statusLvl = statusError
		m.statusAt = time.Now()
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
		// Invalidate the plan — it has been consumed.
		m.planState = planIdle
		m.plan = nil
		m.planTree = nil
		// Fetch outputs after successful apply.
		return m, m.startFetchOutputs()
	case outputResultMsg:
		m.outputs = msg.outputs
		m.outputsReady = true
		return m, nil
	case outputErrorMsg:
		// Non-fatal: just note in status bar.
		m.status = "apply succeeded (outputs unavailable)"
		m.statusLvl = statusInfo
		m.statusAt = time.Now()
		return m, nil
	case applyErrorMsg:
		m.applyState = applyIdle
		m.applyErr = msg.err.Error()
		m.status = "apply failed: " + msg.err.Error()
		m.statusDetail = msg.err.Error()
		m.statusLvl = statusError
		m.statusAt = time.Now()
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
		m.applyRefSwitch(msg.result)
		return m, nil
	case refSwitchErrorMsg:
		m.refSwitching = false
		m.refErr = msg.err.Error()
		m.status = "ref switch failed: " + msg.err.Error()
		m.statusDetail = msg.err.Error()
		m.statusLvl = statusError
		m.statusAt = time.Now()
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

	// Output view: dismiss with Esc or q; scroll with j/k/arrows.
	if m.outputsReady {
		switch msg.String() {
		case "esc", "q":
			m.outputsReady = false
			m.outputs = nil
			m.outputScroll = 0
		case "down", "j":
			m.outputScroll++
		case "up", "k":
			if m.outputScroll > 0 {
				m.outputScroll--
			}
		case "pgdown", "ctrl+d":
			m.outputScroll += m.height / 2
		case "pgup", "ctrl+u":
			m.outputScroll -= m.height / 2
			if m.outputScroll < 0 {
				m.outputScroll = 0
			}
		case "g":
			m.outputScroll = 0
		case "G":
			m.outputScroll = maxInt // clamped at render time
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
			m.focus = focusRight
		} else {
			m.focus = focusLeft
		}
		return m, nil
	case "esc":
		m.focus = focusLeft
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
		// Open ref switch modal from the left pane (if RefSwitcher is configured).
		if m.focus == focusLeft && m.RefSwitcher != nil {
			m.refModal = true
			m.refInput = m.LiteralRef
			m.refErr = ""
			m.refOrphaned = nil
			return m, nil
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
				m.applyEditorValue(v, e2.CurrentValue())
				// Schedule debounced validate after each edit.
				if valCmd := m.scheduleValidate(); valCmd != nil {
					cmd = tea.Batch(cmd, valCmd)
				}
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
	case "o", "O":
		// Prefer planned outputs (available before apply).
		if m.plan != nil && len(m.plan.OutputChanges) > 0 {
			m.outputScroll = 0
			m.showPlanOutputs()
			return m, nil
		}
		// Fall back to terraform output (reads from state, requires prior apply).
		if m.OutputProvider != nil {
			m.outputScroll = 0
			return m, m.startFetchOutputs()
		}
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
	switch msg.String() {
	case "esc":
		m.refModal = false
		return m, nil
	case "enter":
		newRef := strings.TrimSpace(m.refInput)
		if newRef == "" || newRef == m.LiteralRef {
			m.refModal = false
			return m, nil
		}
		m.refModal = false
		m.refSwitching = true
		m.refErr = ""
		m.status = ""
		return m, tea.Batch(m.startRefSwitch(newRef), spinnerTick())
	case "backspace":
		if len(m.refInput) > 0 {
			m.refInput = m.refInput[:len(m.refInput)-1]
		}
		return m, nil
	case "ctrl+u":
		m.refInput = ""
		return m, nil
	default:
		// Accept printable characters for the ref input.
		if len(msg.String()) == 1 && msg.String()[0] >= 32 {
			m.refInput += msg.String()
		}
		return m, nil
	}
}

// applyRefSwitch merges a successful ref switch result into the model,
// preserving user overrides and recording orphaned variables.
func (m *Model) applyRefSwitch(result *RefSwitchResult) {
	m.refSwitching = false
	m.refOrphaned = result.OrphanedVars

	// Preserve existing user values — only drop them from the active var list,
	// not from state.Values. This allows switching back to recover them.
	oldValues := m.State.Values

	// Replace state with the new one from the switched ref.
	m.State = result.State
	m.LiteralRef = result.LiteralRef
	m.ResolvedSHA = result.ResolvedSHA

	// Re-apply user overrides that still have matching variables.
	if m.State.Values == nil {
		m.State.Values = make(map[string]cty.Value)
	}
	for name, val := range oldValues {
		m.State.Values[name] = val
	}

	// Rebuild the UI.
	m.recomputeRows()
	m.refreshEditor()
	m.dirty = true

	// Reset planner init state so the next plan re-checks modules.
	if p, ok := m.Planner.(*TfexecPlanner); ok {
		p.ResetInit()
	}

	// Status message.
	msg := fmt.Sprintf("Switched to ref: %s (%s)", result.LiteralRef, shortSHA(result.ResolvedSHA))
	if len(result.OrphanedVars) > 0 {
		names := strings.Join(result.OrphanedVars, ", ")
		msg += fmt.Sprintf(" · %d orphaned: %s", len(result.OrphanedVars), names)
	}
	m.status = msg
	m.statusLvl = statusInfo
	m.statusAt = time.Now()
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

	if m.helpModal {
		return m.renderHelpModal()
	}
	if m.errorDetail {
		return m.renderErrorDetail()
	}
	if m.outputsReady {
		return m.renderOutputView()
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

// showPlanOutputs converts the plan's OutputChanges into the outputs map
// and activates the output view, allowing the user to see planned outputs
// before apply (when no state exists yet).
func (m *Model) showPlanOutputs() {
	outputs := make(map[string]uptfexec.OutputMeta, len(m.plan.OutputChanges))
	for name, change := range m.plan.OutputChanges {
		var meta uptfexec.OutputMeta
		if b, ok := change.AfterSensitive.(bool); ok && b {
			meta.Sensitive = true
		}
		if change.After != nil {
			if raw, err := json.Marshal(change.After); err == nil {
				meta.Value = raw
			}
		} else {
			meta.Value = json.RawMessage(`"(known after apply)"`)
		}
		outputs[name] = meta
	}
	m.outputs = outputs
	m.outputsReady = true
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
