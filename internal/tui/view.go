package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/zclconf/go-cty/cty"

	"github.com/canonical/atelier/internal/tftypes"
	"github.com/canonical/atelier/internal/wrapper"
)

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

// renderStatus is the bottom bar. It surfaces the current mode (idle,
// loading, plan-ready, error) plus module info and contextual key hints.
func (m *Model) renderStatus() string {
	var left string
	switch {
	case m.applyState == applyLoading:
		frame := spinnerFrames[m.planSpinnerFrame%len(spinnerFrames)]
		left = fmt.Sprintf("%s %s · %s",
			styleStatusBusy.Render(frame),
			styleStatusBusy.Render("Running terraform apply…"),
			m.moduleBanner())
	case m.planState == planLoading:
		frame := spinnerFrames[m.planSpinnerFrame%len(spinnerFrames)]
		left = fmt.Sprintf("%s %s · %s",
			styleStatusBusy.Render(frame),
			styleStatusBusy.Render("Running terraform plan…"),
			m.moduleBanner())
	case m.statusLvl == statusError && m.status != "":
		// Truncate to first line to keep the status bar single-height.
		errText := m.status
		if idx := strings.IndexByte(errText, '\n'); idx >= 0 {
			errText = errText[:idx]
		}
		left = fmt.Sprintf("%s · %s",
			styleStatusError.Render("✗ "+errText),
			m.moduleBanner())
	case m.status != "":
		left = fmt.Sprintf("%s · %s", m.status, m.moduleBanner())
	default:
		left = m.moduleBanner()
	}
	// Append validate summary when available and no other error is shown.
	if m.validateOutput != nil && m.statusLvl != statusError {
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
		hints := "[↑↓] navigate  [Enter] toggle  [P] re-plan"
		if m.Applier != nil && m.applyState != applyLoading {
			hints += "  [A] apply"
		}
		if m.statusLvl == statusError && m.statusDetail != "" {
			hints += "  [E] error"
		}
		hints += "  [Esc] back"
		return hints
	}
	hints := "[Tab] switch pane  [^R] reset  [P] plan"
	if len(m.presets) > 0 {
		hints += "  [F] preset"
	}
	if m.RefSwitcher != nil {
		hints += "  [R] ref"
	}
	if m.statusLvl == statusError && m.statusDetail != "" {
		hints += "  [E] error"
	}
	hints += "  [Q] quit"
	return hints
}

func (m *Model) bodyHeight() int {
	if m.height < 4 {
		return 1
	}
	return m.height - 1
}

// renderErrorDetail renders a full-screen modal showing the complete error
// output. Invoked by pressing E when an error is present.
func (m *Model) renderErrorDetail() string {
	var b strings.Builder
	fmt.Fprintln(&b, styleVarHeader.Render("Error details"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, m.statusDetail)
	fmt.Fprintln(&b)
	fmt.Fprint(&b, styleHelp.Render("[Esc] close"))

	content := b.String()
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Render(content)
}

// renderPresetPicker renders the full-screen preset picker overlay.
func (m *Model) renderPresetPicker() string {
	var b strings.Builder
	fmt.Fprintln(&b, styleVarHeader.Render("Select a preset"))
	fmt.Fprintln(&b)

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

	fmt.Fprintln(&b)
	fmt.Fprint(&b, styleHelp.Render("[↑↓] select   [Enter] apply   [Esc] cancel"))

	content := b.String()
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Render(content)
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
		fmt.Fprintln(&b, styleVarHeader.Render("Switching ref"))
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "%s %s\n",
			styleStatusBusy.Render(frame),
			styleStatusBusy.Render("Cloning and reinitialising…"))
	} else {
		fmt.Fprintln(&b, styleVarHeader.Render("Switch module ref"))
		fmt.Fprintln(&b)
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
		fmt.Fprintln(&b)
		if m.refErr != "" {
			fmt.Fprintln(&b, styleStatusError.Render("Error: "+m.refErr))
			fmt.Fprintln(&b)
		}
		fmt.Fprint(&b, styleHelp.Render("[Enter] switch   [Esc] cancel"))
	}

	content := b.String()
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		PaddingLeft(2).
		PaddingTop(1).
		Render(content)
}
