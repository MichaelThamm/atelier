package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	tfjson "github.com/hashicorp/terraform-json"
)

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
		m.checkWarnings = nil
		return m, tea.Batch(m.startPlan(), spinnerTick())
	case "w", "W":
		// Open the check-warnings detail modal when warnings are present.
		if len(m.checkWarnings) > 0 {
			m.warnDetail = true
			return m, nil
		}
	case "a", "A":
		// Apply the current plan.
		if m.Applier != nil && m.applyState != applyLoading {
			m.applyState = applyLoading
			return m, tea.Batch(m.startApply(), spinnerTick())
		}
	case "d", "D":
		// Open ref-switch detail modal when a ref switch summary is available.
		if m.refDetailText != "" {
			m.refDetail = true
			return m, nil
		}
	case "up":
		m.movePlanCursor(-1)
		m.planDiffScroll = 0 // reset diff scroll on cursor move
	case "down":
		m.movePlanCursor(+1)
		m.planDiffScroll = 0 // reset diff scroll on cursor move
	case "pgup":
		m.movePlanCursor(-m.planPanelHeight() / 2)
		m.planDiffScroll = 0
	case "pgdown":
		m.movePlanCursor(m.planPanelHeight() / 2)
		m.planDiffScroll = 0
	case "home":
		m.movePlanCursor(-len(flattenedRows(m.activeTree())))
		m.planDiffScroll = 0
	case "end":
		m.movePlanCursor(len(flattenedRows(m.activeTree())))
		m.planDiffScroll = 0
	case "enter", " ":
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

// handlePlanFailedKey routes keys for the failed-plan view. Esc returns to the
// editor, where the wrapper can be fixed and re-planned; P re-plans from here.
// ? is handled globally.
func (m *Model) handlePlanFailedKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.planState = planIdle
		m.planErr = ""
		return m, nil
	case "p", "P":
		m.planState = planLoading
		m.planErr = ""
		m.checkWarnings = nil
		return m, tea.Batch(m.startPlan(), spinnerTick())
	}
	return m, nil
}

// handlePlanDiffKey handles keys when the diff pane is focused.
// ↑↓ scroll the diff; Tab/Esc return focus to the tree.
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
	case "up":
		if m.planDiffScroll > 0 {
			m.planDiffScroll--
		}
	case "down":
		m.planDiffScroll++
	case "pgup":
		m.planDiffScroll -= m.planPanelHeight() / 2
		if m.planDiffScroll < 0 {
			m.planDiffScroll = 0
		}
	case "pgdown":
		m.planDiffScroll += m.planPanelHeight() / 2
	case "home":
		m.planDiffScroll = 0
	case "end":
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
