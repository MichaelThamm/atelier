package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/zclconf/go-cty/cty"

	"github.com/canonical/atelier/internal/tftypes"
	"github.com/canonical/atelier/internal/wrapper"
)

// styleModalFrame is the bordered box used by all overlay modals.
var styleModalFrame = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(colorFaint).
	Padding(1, 2)

// renderModalFrame renders a consistent overlay with a bordered frame, title,
// body content, and a footer hint line. All modals should use this for visual
// cohesion.
func (m *Model) renderModalFrame(title, body, footer string) string {
	titleLine := styleVarHeader.Render(title)
	footerLine := styleHelp.Render(footer)

	// Inner width = terminal width minus border (2) and padding (4).
	innerW := m.width - 8
	if innerW < 30 {
		innerW = 30
	}
	// Inner height = terminal height minus border (2), padding (2), title (1), blank (1), footer (1).
	innerH := m.height - 7
	if innerH < 3 {
		innerH = 3
	}

	// Truncate body to fit the available height.
	lines := strings.Split(body, "\n")
	if len(lines) > innerH {
		lines = lines[:innerH]
	}
	visibleBody := strings.Join(lines, "\n")

	content := titleLine + "\n\n" + visibleBody + "\n\n" + footerLine

	frame := styleModalFrame.
		Width(innerW).
		Render(content)

	// Center the frame in the terminal.
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, frame)
}

// renderLeftPane draws the variable list with per-variable modification
// markers. The active-pane cursor row uses a background highlight; the
// inactive pane uses a quieter accented version.
func (m *Model) renderLeftPane() string {
	const leftWidth = 32
	var b strings.Builder
	for i, r := range m.rows {
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

// renderHeader is the top bar showing module context and validate status.
func (m *Model) renderHeader() string {
	left := m.moduleBanner()
	// Append validate summary.
	if m.validateOutput != nil {
		if m.validateOutput.Valid {
			left += "  " + styleHelp.Render("✓ valid")
		} else {
			summary := fmt.Sprintf("✗ %d error(s)", m.validateOutput.ErrorCount)
			if m.validateOutput.WarningCount > 0 {
				summary += fmt.Sprintf(", %d warning(s)", m.validateOutput.WarningCount)
			}
			left += "  " + styleStatusError.Render(summary)
		}
	}
	leftW := lipgloss.Width(left)
	padW := 2
	gap := m.width - leftW - padW
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap)
	return styleHeaderBar.MaxWidth(m.width).Render(bar)
}

// renderFooter is the bottom bar showing transient status messages and key hints.
func (m *Model) renderFooter() string {
	var left string
	switch {
	case m.applyState == applyLoading:
		frame := spinnerFrames[m.planSpinnerFrame%len(spinnerFrames)]
		left = fmt.Sprintf("%s %s",
			styleStatusBusy.Render(frame),
			styleStatusBusy.Render("Running terraform apply…"))
	case m.planState == planLoading:
		frame := spinnerFrames[m.planSpinnerFrame%len(spinnerFrames)]
		left = fmt.Sprintf("%s %s",
			styleStatusBusy.Render(frame),
			styleStatusBusy.Render("Running terraform plan…"))
	case m.statusLvl == statusError && m.status != "":
		errText := m.status
		if idx := strings.IndexByte(errText, '\n'); idx >= 0 {
			errText = errText[:idx]
		}
		left = styleStatusError.Render("✗ " + errText)
	case m.status != "":
		left = m.status
	}
	hints := styleHelp.Render(m.statusHints())
	hintsW := lipgloss.Width(hints)
	leftW := lipgloss.Width(left)
	padW := 2
	gap := m.width - leftW - hintsW - padW
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + hints
	return styleStatusBar.MaxWidth(m.width).Render(bar)
}

// renderStatus composes the full footer (kept for plan view compatibility).
func (m *Model) renderStatus() string {
	return m.renderFooter()
}

func (m *Model) statusHints() string {
	switch m.planState {
	case planLoading:
		return "[Esc] cancel  [?] help"
	case planReady:
		hints := "[↑↓] navigate  [P] re-plan"
		if m.Applier != nil && m.applyState != applyLoading {
			hints += "  [A] apply"
		}
		if m.OutputProvider != nil {
			hints += "  [O] outputs"
		}
		if m.statusLvl == statusError && m.statusDetail != "" {
			hints += "  [E] error"
		}
		hints += "  [?] help"
		return hints
	}
	hints := "[Tab] pane  [P] plan"
	if m.statusLvl == statusError && m.statusDetail != "" {
		hints += "  [E] error"
	}
	hints += "  [Q] quit  [?] help"
	return hints
}

// renderHelpModal renders a centered overlay listing all keyboard shortcuts.
func (m *Model) renderHelpModal() string {
	var b strings.Builder

	switch {
	case m.planState == planReady:
		fmt.Fprintln(&b, "  ↑/k  ↓/j      Navigate plan tree")
		fmt.Fprintln(&b, "  Enter/Space    Toggle collapse/expand")
		fmt.Fprintln(&b, "  P              Re-run terraform plan")
		if m.Applier != nil {
			fmt.Fprintln(&b, "  A              Apply the current plan")
		}
		if m.OutputProvider != nil || (m.plan != nil && len(m.plan.OutputChanges) > 0) {
			fmt.Fprintln(&b, "  O              Show terraform outputs")
		}
		if m.statusLvl == statusError && m.statusDetail != "" {
			fmt.Fprintln(&b, "  E              Show error details")
		}
		fmt.Fprintln(&b, "  Esc/q          Return to editor")
	default:
		fmt.Fprintln(&b, "  Tab            Switch pane (left ↔ right)")
		fmt.Fprintln(&b, "  ↑/k  ↓/j      Move cursor")
		fmt.Fprintln(&b, "  Enter/→/l      Focus right pane")
		fmt.Fprintln(&b, "  Esc/←          Focus left pane")
		fmt.Fprintln(&b, "  Ctrl+R         Reset variable to default")
		fmt.Fprintln(&b, "  P              Run terraform plan")
		if len(m.presets) > 0 {
			fmt.Fprintln(&b, "  F              Open preset picker")
		}
		if m.RefSwitcher != nil {
			fmt.Fprintln(&b, "  R              Switch module ref")
		}
		if m.statusLvl == statusError && m.statusDetail != "" {
			fmt.Fprintln(&b, "  E              Show error details")
		}
		fmt.Fprintln(&b, "  Q              Quit (auto-saves)")
	}

	fmt.Fprintln(&b, "  Ctrl+C         Quit immediately")
	fmt.Fprintln(&b, "  ?              Toggle this help")

	return m.renderModalFrame("Keyboard shortcuts", b.String(), "[Esc] or [?] to close")
}

func (m *Model) bodyHeight() int {
	if m.height < 5 {
		return 1
	}
	// Reserve 1 line for header + 1 line for footer.
	return m.height - 2
}

// renderErrorDetail renders a centered modal showing the complete error output.
func (m *Model) renderErrorDetail() string {
	return m.renderModalFrame("Error details", m.statusDetail, "[Esc] close")
}

// renderPresetPicker renders a centered modal for preset selection.
func (m *Model) renderPresetPicker() string {
	var b strings.Builder
	for i, p := range m.presets {
		cursor := "  "
		name := p.Name
		if i == m.presetCursor {
			cursor = styleCursorActive.Render("▸ ")
			name = styleCursorActive.Render(p.Name)
		}
		line := cursor + name
		if p.Description != "" {
			line += "  " + styleDescription.Render(p.Description)
		}
		fmt.Fprintln(&b, line)
	}
	return m.renderModalFrame("Select a preset", b.String(), "[↑↓] select   [Enter] apply   [Esc] cancel")
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

// renderRefModal renders the ref switch prompt or the in-flight spinner.
func (m *Model) renderRefModal() string {
	var b strings.Builder

	if m.refSwitching {
		frame := spinnerFrames[m.planSpinnerFrame%len(spinnerFrames)]
		fmt.Fprintf(&b, "%s %s\n",
			styleStatusBusy.Render(frame),
			styleStatusBusy.Render("Cloning and reinitialising…"))
		return m.renderModalFrame("Switching ref", b.String(), "")
	}

	if m.ModuleName != "" {
		fmt.Fprintf(&b, "Module:  %s\n", styleDescription.Render(m.ModuleName))
	}
	if m.SourceURL != "" {
		fmt.Fprintf(&b, "Source:  %s\n", styleDescription.Render(m.SourceURL))
	}
	fmt.Fprintf(&b, "Current: %s", styleDescription.Render(m.LiteralRef))
	if m.ResolvedSHA != "" {
		fmt.Fprintf(&b, " (%s)", shortSHA(m.ResolvedSHA))
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "New ref: %s%s\n", m.refInput, styleCursorActive.Render("▏"))
	if m.refErr != "" {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, styleStatusError.Render("Error: "+m.refErr))
	}

	return m.renderModalFrame("Switch module ref", b.String(), "[Enter] switch   [Esc] cancel")
}
