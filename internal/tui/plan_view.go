package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	tfjson "github.com/hashicorp/terraform-json"

	"github.com/MichaelThamm/atelier/internal/state"
)

// renderPlanScreen renders the full-screen plan view: a summary header,
// then a tree-on-left + attribute-diff-on-right split, then the status
// bar. Triggered when m.planState == planReady.
func (m *Model) renderPlanScreen() string {
	summaryText := PlanSummary(m.plan)
	if m.tfState != nil {
		summaryText += "  |  " + m.tfState.SummaryLine()
	}
	summary := stylePlanSummary.Render(summaryText)

	tree := m.renderPlanTree()
	var rightPane string
	if m.planShowState {
		rightPane = m.renderStateValues()
	} else {
		rightPane = m.renderPlanDiff()
	}

	header := m.renderHeader()
	footer := m.renderFooter()
	body := lipgloss.JoinHorizontal(lipgloss.Top, tree, rightPane)
	return lipgloss.JoinVertical(lipgloss.Left, header, summary, body, footer)
}

// renderPlanTree draws the collapsible module → type → resource tree on
// the left. The focused row uses the same active-cursor style as the
// editor pane, so navigation is visually consistent across modes.
func (m *Model) renderPlanTree() string {
	const leftWidth = 44
	// Inner content width = panel width minus padding (1 left + 1 right).
	const innerWidth = leftWidth - 2

	// Pick the active tree: state tree when in state mode, plan tree otherwise.
	activeTree := m.planTree
	if m.planShowState && m.stateTree != nil {
		activeTree = m.stateTree
	}
	rows := flattenedRows(activeTree)

	// Panel style depends on whether the tree or diff pane is focused.
	panelStyle := stylePanelFocused
	if m.planDiffFocus {
		panelStyle = stylePanel
	}

	if len(rows) == 0 {
		content := styleDescription.Render("No changes.")
		return panelStyle.Width(leftWidth).Height(m.planPanelHeight()).Render(content)
	}

	// Scrolling: ensure cursor is visible.
	// Reserve one line for the scroll indicator when content overflows.
	visible := m.planPanelHeight()
	needsIndicator := len(rows) > visible
	if needsIndicator {
		visible-- // reserve last line for the scroll indicator
	}
	if m.planScroll > m.planCursor {
		m.planScroll = m.planCursor
	}
	if m.planCursor >= m.planScroll+visible {
		m.planScroll = m.planCursor - visible + 1
	}
	if m.planScroll < 0 {
		m.planScroll = 0
	}

	end := m.planScroll + visible
	if end > len(rows) {
		end = len(rows)
	}

	var b strings.Builder
	for i := m.planScroll; i < end; i++ {
		line := renderPlanRow(rows[i])
		// Truncate to prevent wrapping that would break the height budget.
		line = ansi.Truncate(line, innerWidth, "…")
		if i == m.planCursor {
			line = styleCursorActive.Render(line)
		}
		fmt.Fprintln(&b, line)
	}

	// Scroll indicator (fits within the height budget).
	if needsIndicator {
		pct := 0
		maxScroll := len(rows) - visible
		if maxScroll > 0 {
			pct = m.planScroll * 100 / maxScroll
		}
		fmt.Fprintf(&b, "%s", styleHelp.Render(fmt.Sprintf("(%d/%d %d%%)", m.planCursor+1, len(rows), pct)))
	}

	return panelStyle.Width(leftWidth).Height(m.planPanelHeight()).Render(b.String())
}

// renderPlanRow renders one tree row. Module and type rows get a caret
// indicator (▾ expanded, ▸ collapsed) and theme-tinted labels; resource
// rows display their coloured action marker and the resource name.
func renderPlanRow(r planRow) string {
	indent := strings.Repeat("  ", r.Depth)
	n := r.Node
	switch n.Kind {
	case nodeModule:
		caret := "▾"
		if n.Collapsed {
			caret = "▸"
		}
		return fmt.Sprintf("%s%s %s", indent, caret, stylePlanModule.Render(n.Label))
	case nodeType:
		caret := "▾"
		if n.Collapsed {
			caret = "▸"
		}
		return fmt.Sprintf("%s%s %s", indent, caret, stylePlanType.Render(n.Label))
	case nodeResource:
		return fmt.Sprintf("%s%s %s",
			indent,
			styledAction(n.Action),
			stylePlanResource.Render(n.Label))
	}
	return indent + n.Label
}

// styledAction returns a coloured +/~/-/↻ marker. Maps to the action
// semantic in theme.go.
func styledAction(a string) string {
	switch a {
	case "+":
		return stylePlanAdd.Render(a)
	case "~":
		return stylePlanChange.Render(a)
	case "-":
		return stylePlanDelete.Render(a)
	case "↻":
		return stylePlanReplace.Render(a)
	}
	return a
}

// renderPlanDiff draws the attribute-level diff for the currently focused
// resource leaf on the right. Module / type rows display a brief help
// instead. Diff lines themselves are coloured by their marker via the same
// styledAction colour scheme.
func (m *Model) renderPlanDiff() string {
	rightWidth := m.width - 46
	if rightWidth < 24 {
		rightWidth = 24
	}

	// Panel style depends on whether the diff pane is focused.
	panelStyle := stylePanel
	if m.planDiffFocus {
		panelStyle = stylePanelFocused
	}

	rc := m.SelectedPlanChange()
	if rc == nil {
		hint := ansi.Wordwrap(
			"Select a resource row to see its attribute diff.\n\n"+
				"Use ↑/↓ to navigate, Enter to collapse/expand, Tab to focus this pane.",
			rightWidth-2, " ")
		return panelStyle.Width(rightWidth).Height(m.planPanelHeight()).Render(
			styleDescription.Render(hint))
	}

	var b strings.Builder
	fmt.Fprintln(&b, styleVarHeader.Render(rc.Address))
	fmt.Fprintln(&b, styleDescription.Render(
		fmt.Sprintf("%s.%s — %s", rc.Type, rc.Name, joinActions(rc.Change))))
	fmt.Fprintln(&b)

	lines := AttributeDiff(rc)
	if len(lines) == 0 {
		fmt.Fprintln(&b, styleDescription.Render("(no attribute-level changes)"))
	} else {
		for _, l := range lines {
			fmt.Fprintln(&b, colourisedDiffLine(l))
		}
	}
	// Word-wrap diff content to the panel's inner width.
	wrapped := ansi.Wordwrap(b.String(), rightWidth-2, " ")
	// Scroll the diff content if it exceeds the panel height.
	allLines := strings.Split(wrapped, "\n")
	ph := m.planPanelHeight()
	if len(allLines) > ph {
		// Clamp scroll.
		maxScroll := len(allLines) - ph
		if m.planDiffScroll > maxScroll {
			m.planDiffScroll = maxScroll
		}
		if m.planDiffScroll < 0 {
			m.planDiffScroll = 0
		}
		end := m.planDiffScroll + ph
		if end > len(allLines) {
			end = len(allLines)
		}
		allLines = allLines[m.planDiffScroll:end]
	}
	return panelStyle.Width(rightWidth).Height(m.planPanelHeight()).Render(strings.Join(allLines, "\n"))
}

// colourisedDiffLine renders an AttributeDiffLine with its action marker
// tinted. The body text is left at the terminal default so values stay
// readable; sensitive values are already replaced with "<sensitive>"
// upstream (AttributeDiff handles masking).
func colourisedDiffLine(l AttributeDiffLine) string {
	marker := styledAction(l.Marker)
	body := ""
	switch l.Marker {
	case "+":
		body = fmt.Sprintf(" %s = %s", l.Key, l.After)
	case "-":
		body = fmt.Sprintf(" %s = %s", l.Key, l.Before)
	case "~":
		body = fmt.Sprintf(" %s = %s → %s", l.Key, l.Before, l.After)
	default:
		body = fmt.Sprintf("  %s = %s", l.Key, l.After)
	}
	return marker + body
}

// joinActions renders a Change's actions as a human-readable string
// ("create", "delete then create", etc.).
func joinActions(c *tfjson.Change) string {
	if c == nil || len(c.Actions) == 0 {
		return "no change"
	}
	parts := make([]string, len(c.Actions))
	for i, a := range c.Actions {
		parts[i] = string(a)
	}
	return strings.Join(parts, " then ")
}

// renderStateValues renders the right pane in "state mode" — showing
// attribute values for the currently selected resource from terraform state.
func (m *Model) renderStateValues() string {
	rightWidth := m.width - 46
	if rightWidth < 24 {
		rightWidth = 24
	}

	panelStyle := stylePanel
	if m.planDiffFocus {
		panelStyle = stylePanelFocused
	}

	// Find the state resource matching the selected plan resource.
	rc := m.SelectedPlanChange()
	if rc == nil {
		hint := "Select a resource to view its current state values."
		hint = ansi.Wordwrap(hint, rightWidth-2, " ")
		return panelStyle.Width(rightWidth).Height(m.planPanelHeight()).Render(
			styleDescription.Render(hint))
	}

	res := m.findStateResource(rc.Address)
	if res == nil {
		msg := fmt.Sprintf("Resource %s not found in state.\n(It may be newly created by this plan.)", rc.Address)
		msg = ansi.Wordwrap(msg, rightWidth-2, " ")
		return panelStyle.Width(rightWidth).Height(m.planPanelHeight()).Render(
			styleDescription.Render(msg))
	}

	var b strings.Builder
	fmt.Fprintln(&b, styleVarHeader.Render(res.Address))
	fmt.Fprintln(&b, styleDescription.Render(
		fmt.Sprintf("%s.%s — current state", res.Type, res.Name)))
	fmt.Fprintln(&b)

	lines := res.AttributeLines()
	if len(lines) == 0 {
		fmt.Fprintln(&b, styleDescription.Render("(no attributes)"))
	} else {
		for _, l := range lines {
			fmt.Fprintln(&b, "  "+l)
		}
	}

	wrapped := ansi.Wordwrap(b.String(), rightWidth-2, " ")
	allLines := strings.Split(wrapped, "\n")
	ph := m.planPanelHeight()
	if len(allLines) > ph {
		maxScroll := len(allLines) - ph
		if m.planDiffScroll > maxScroll {
			m.planDiffScroll = maxScroll
		}
		if m.planDiffScroll < 0 {
			m.planDiffScroll = 0
		}
		end := m.planDiffScroll + ph
		if end > len(allLines) {
			end = len(allLines)
		}
		allLines = allLines[m.planDiffScroll:end]
	}
	return panelStyle.Width(rightWidth).Height(m.planPanelHeight()).Render(strings.Join(allLines, "\n"))
}

// findStateResource looks up a resource by its full address in the loaded state.
func (m *Model) findStateResource(address string) *state.Resource {
	if m.tfState == nil {
		return nil
	}
	for i := range m.tfState.Resources {
		if m.tfState.Resources[i].Address == address {
			return &m.tfState.Resources[i]
		}
	}
	return nil
}

// planPanelHeight returns the inner height for plan screen panels
// (tree + diff). The plan screen's content budget is contentHeight(), minus
// the summary line (1), minus the panel border lines (2).
func (m *Model) planPanelHeight() int {
	h := m.contentHeight() - 3
	if h < 1 {
		return 1
	}
	return h
}
