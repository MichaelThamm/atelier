package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	tfjson "github.com/hashicorp/terraform-json"
)

// renderPlanScreen renders the full-screen plan view: a summary header,
// then a tree-on-left + attribute-diff-on-right split, then the status
// bar. Triggered when m.planState == planReady.
func (m *Model) renderPlanScreen() string {
	summary := stylePlanSummary.Render(PlanSummary(m.plan))

	tree := m.renderPlanTree()
	diff := m.renderPlanDiff()

	header := m.renderHeader()
	footer := m.renderFooter()
	body := lipgloss.JoinHorizontal(lipgloss.Top, tree, diff)
	return lipgloss.JoinVertical(lipgloss.Left, header, summary, body, footer)
}

// renderPlanTree draws the collapsible module → type → resource tree on
// the left. The focused row uses the same active-cursor style as the
// editor pane, so navigation is visually consistent across modes.
func (m *Model) renderPlanTree() string {
	const leftWidth = 44
	rows := flattenedRows(m.planTree)
	if len(rows) == 0 {
		content := styleDescription.Render("No changes.")
		return stylePaneDivider.Width(leftWidth).Height(m.planBodyHeight()).Render(content)
	}

	var b strings.Builder
	for i, r := range rows {
		line := renderPlanRow(r)
		if i == m.planCursor {
			line = styleCursorActive.Render(line)
		}
		fmt.Fprintln(&b, line)
	}
	return stylePaneDivider.Width(leftWidth).Height(m.planBodyHeight()).Render(b.String())
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
	rc := m.SelectedPlanChange()
	if rc == nil {
		hint := styleDescription.Render(
			"Select a resource row to see its attribute diff.\n\n" +
				"Use ↑/↓ to navigate, Enter to collapse/expand, Esc to return.")
		return stylePaneRight.Width(rightWidth).Height(m.planBodyHeight()).Render(hint)
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
	return stylePaneRight.Width(rightWidth).Height(m.planBodyHeight()).Render(b.String())
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

// planBodyHeight is the height of the plan-screen body (tree + diff pane),
// reserving one line each for header, summary, and footer.
func (m *Model) planBodyHeight() int {
	if m.height < 6 {
		return 1
	}
	return m.height - 3
}
