package tui

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichaelThamm/atelier/internal/gitops"
	"github.com/MichaelThamm/atelier/internal/state"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case planResultMsg:
		m.plan = msg.plan
		m.planTree = BuildPlanTree(msg.plan)
		m.checkWarnings = FailedChecks(msg.plan)
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
		m.checkWarnings = nil
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
		// The field still holds the ref the user just tried (the switch never
		// cleared it), so they can edit it rather than retype. Refresh the
		// filtered list against the possibly-updated available set.
		m.refMatchCursor = 0
		m.refreshRefMatches()
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
		m.refreshRefMatches()
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

	// Check-warnings detail modal: same dismiss pattern as the error modal,
	// toggled by W (or Esc/q).
	if m.warnDetail {
		if msg.String() == "esc" || msg.String() == "q" || msg.String() == "w" || msg.String() == "W" {
			m.warnDetail = false
		}
		return m, nil
	}

	// Ref-switch detail modal: shows the full summary when [D] is pressed.
	if m.refDetail {
		if msg.String() == "esc" || msg.String() == "q" || msg.String() == "d" || msg.String() == "D" {
			m.refDetail = false
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
		// While plan is in flight, the logs view gets priority for scroll
		// and navigation keys. Esc cancels the plan from either view.
		if m.activeView == viewLogs {
			if msg.String() == "esc" {
				m.planState = planIdle
				m.status = "plan cancelled (best effort)"
				m.statusLvl = statusInfo
				m.activeView = viewEditor
				m.logScroll = 0
				return m, nil
			}
			return m.handleLogsKey(msg)
		}
		switch msg.String() {
		case "esc":
			m.planState = planIdle
			m.status = "plan cancelled (best effort)"
			m.statusLvl = statusInfo
		case "l", "L":
			if m.progress != nil {
				m.activeView = viewLogs
				m.logAutoScroll = true
				var lines []LogLine
				switch m.logsTab {
				case logsTabErrors:
					lines = m.progress.StderrLines()
				case logsTabLogs:
					lines = m.progress.StdoutLines()
				}
				h := m.panelHeight() - 1
				if h < 1 {
					h = 1
				}
				m.logScroll = max(0, len(lines)-h)
			}
		}
		return m, nil
	}

	// Logs view interception: when the logs panel is active, it owns
	// scroll keys, L/Tab/Esc to return, and arrow keys.
	if m.activeView == viewLogs {
		return m.handleLogsKey(msg)
	}

	// Preset picker interception: the overlay owns all keys until
	// the user applies (Enter) or cancels (Esc).
	if m.presetPicker {
		return m.handlePresetKey(msg)
	}

	// Save-preset modal interception: text input + confirm/cancel.
	if m.savePresetModal {
		return m.handleSavePresetKey(msg)
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
	case "l", "L":
		if m.progress != nil {
			m.activeView = viewLogs
			m.logAutoScroll = true
			var lines []LogLine
			switch m.logsTab {
			case logsTabErrors:
				lines = m.progress.StderrLines()
			case logsTabLogs:
				lines = m.progress.StdoutLines()
			}
			if len(lines) == 0 {
				m.logScroll = 0
				return m, nil
			}
			h := m.panelHeight() - 1
			if h < 1 {
				h = 1
			}
			m.logScroll = max(0, len(lines)-h)
			return m, nil
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
	case "s", "S":
		// Save the current configuration as a new preset (ADR-0026). Only
		// from the left pane — in the right pane the key belongs to the editor.
		if m.focus == focusLeft {
			return m.openSavePreset()
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
			m.refInput = newCellInput(ref, false, "")
			m.refErr = ""
			m.refOrphaned = nil
			// Fetch the remote's current refs for the list (async, non-fatal).
			m.availableRefs = nil
			m.refMatches = nil
			m.refMatchCursor = 0
			m.refreshRefMatches()
			m.refsLoading = true
			return m, m.startListRefs()
		}
	case "d", "D":
		// Open ref-switch detail modal when a ref switch summary is available.
		if m.focus == focusLeft && m.refDetailText != "" {
			m.refDetail = true
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
