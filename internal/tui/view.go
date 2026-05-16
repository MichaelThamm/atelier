package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/zclconf/go-cty/cty"

	"github.com/canonical/atelier/internal/tftypes"
	"github.com/canonical/atelier/internal/wrapper"
)

// renderLeftPane draws the variable list, including group headers and the
// per-variable modification marker. The active-pane cursor row uses a
// background highlight; the inactive pane uses a quieter accented version.
func (m *Model) renderLeftPane() string {
	const leftWidth = 32
	var b strings.Builder
	for i, r := range m.rows {
		if r.IsGroup {
			label := m.groups[r.GroupIdx].Name
			if label == "" {
				continue
			}
			fmt.Fprintln(&b, styleGroupHeader.Render(label))
			continue
		}
		marker := varMarker(m.State, r.VarName)
		line := fmt.Sprintf("%s  %s", marker, r.VarName)
		if i == m.cursor {
			if m.focus == focusLeft {
				line = styleCursorActive.Render(line)
			} else {
				line = styleCursorInactive.Render(line)
			}
		}
		fmt.Fprintln(&b, line)
	}
	content := b.String()
	if content == "" {
		content = styleDescription.Render("(no variables)")
	}
	return stylePaneDivider.Width(leftWidth).Height(m.bodyHeight()).Render(content)
}

// renderRightPane shows the editor for the selected variable, including
// header, description, and the type-specific widget. The right pane has no
// border of its own; the divider lives on the left.
func (m *Model) renderRightPane() string {
	var content string
	v := m.SelectedVariable()
	if v == nil {
		content = styleDescription.Render("Select a variable on the left.")
	} else {
		var b strings.Builder
		header := styleVarHeader.Render(v.Name) + "  " +
			styleDescription.Render("("+kindLabel(v.Type)+")")
		if v.Sensitive {
			header += "  " + styleSensitiveTag.Render("[sensitive]")
		}
		if !v.HasDefault {
			header += "  " + styleRequiredTag.Render("[required]")
		}
		fmt.Fprintln(&b, header)

		if desc := strings.TrimSpace(v.Description); desc != "" {
			fmt.Fprintln(&b, styleDescription.Render(desc))
		}
		fmt.Fprintln(&b)
		if m.editor != nil {
			fmt.Fprintln(&b, m.editor.View())
		}
		content = b.String()
	}
	rightWidth := m.width - 34
	if rightWidth < 20 {
		rightWidth = 20
	}
	return stylePaneRight.Width(rightWidth).Height(m.bodyHeight()).Render(content)
}

// renderStatus is the bottom bar. It surfaces the current mode (idle,
// loading, plan-ready, error) plus module info and contextual key hints.
func (m *Model) renderStatus() string {
	var left string
	switch {
	case m.planState == planLoading:
		frame := spinnerFrames[m.planSpinnerFrame%len(spinnerFrames)]
		left = fmt.Sprintf("%s %s · %s",
			styleStatusBusy.Render(frame),
			styleStatusBusy.Render("Running terraform plan…"),
			m.moduleBanner())
	case m.statusLvl == statusError && m.status != "":
		left = fmt.Sprintf("%s · %s",
			styleStatusError.Render("✗ "+m.status),
			m.moduleBanner())
	case m.status != "":
		left = fmt.Sprintf("%s · %s", m.status, m.moduleBanner())
	default:
		left = fmt.Sprintf("%s · %s",
			styleStatusValid.Render("✓ Valid"),
			m.moduleBanner())
	}
	hints := styleHelp.Render(m.statusHints())
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(hints)
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + hints
	return styleStatusBar.Width(m.width).Render(bar)
}

func (m *Model) statusHints() string {
	switch m.planState {
	case planLoading:
		return "[Esc] cancel"
	case planReady:
		return "[↑↓] navigate  [Enter] toggle  [P] re-plan  [Esc] back"
	}
	return "[Tab] switch pane  [^R] reset  [P] plan  [Q] quit"
}

func (m *Model) bodyHeight() int {
	if m.height < 4 {
		return 1
	}
	return m.height - 1
}

// varMarker returns the modified-vs-default indicator the left pane shows
// for one variable. See SPEC §7.1. Glyphs come pre-coloured by the active
// theme; callers concat the result with the variable name.
func varMarker(state *wrapper.State, name string) string {
	v := state.FindVar(name)
	if v == nil {
		return styleMarkerAtDefault.Render("[ ]")
	}
	current, present := state.Values[name]
	if !v.HasDefault {
		if !present || current == cty.NilVal {
			return styleMarkerRequired.Render("[!]")
		}
		return styleMarkerModified.Render("[✓]")
	}
	if !present {
		return styleMarkerAtDefault.Render("[ ]")
	}
	if v.Type != nil && v.Type.Kind == tftypes.KindObject && !current.IsNull() {
		sparse := wrapper.SparseValue(v, current)
		if sparse.Type().IsObjectType() && sparse.LengthInt() > 0 {
			return styleMarkerModified.Render(fmt.Sprintf("[✓%d]", sparse.LengthInt()))
		}
		return styleMarkerAtDefault.Render("[ ]")
	}
	if !wrapper.ShouldEmit(v, current) {
		return styleMarkerAtDefault.Render("[ ]")
	}
	return styleMarkerModified.Render("[✓]")
}
