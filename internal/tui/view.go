package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// wrapContent word-wraps each line of s to fit within limit columns,
// preserving ANSI escape sequences and handling wide characters.
func wrapContent(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	return ansi.Wordwrap(s, limit, " ")
}

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

	// Word-wrap body to the available inner width.
	body = wrapContent(body, innerW)

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

// renderLeftPane draws the variable list inside a bordered panel.
func (m *Model) renderLeftPane() string {
	const leftWidth = 32
	// Max visual chars for line content inside the panel border.
	// Border takes 2 chars (left+right), leaving leftWidth-2 for content.
	const maxVisualWidth = leftWidth - 2
	// The marker "[ ]" or "[✓]" is 3 display chars, plus 2 spaces = 5.
	const prefixWidth = 5
	const maxNameWidth = maxVisualWidth - prefixWidth
	var b strings.Builder

	visible := m.leftPaneVisibleRows()
	start := m.leftScroll
	end := start + visible
	if end > len(m.rows) {
		end = len(m.rows)
	}

	for i := start; i < end; i++ {
		r := m.rows[i]
		if r.IsHeader {
			// Render section header: "── module_name @ref ──"
			name := r.VarName
			if r.ModuleIdx < len(m.Modules) {
				if ref := m.Modules[r.ModuleIdx].Ref; ref != "" {
					name = fmt.Sprintf("%s @%s", name, ref)
				}
			}
			// Layout: "── " (3 cols) + name + " " (1 col) + trailing dashes,
			// totalling maxVisualWidth. Reserve 5 cols (prefix + space + at
			// least one trailing dash) for the name. Use display width, not
			// byte length: the ellipsis and box-drawing chars are multi-byte
			// but single-column, so len() overshoots and wraps the line.
			name = ansi.Truncate(name, maxVisualWidth-5, "…")
			pad := maxVisualWidth - lipgloss.Width(name) - 4 // 4 = "── " + " "
			if pad < 1 {
				pad = 1
			}
			line := fmt.Sprintf("── %s %s", name, strings.Repeat("─", pad))
			line = styleSectionHeader.Render(line)
			fmt.Fprintln(&b, line)
			continue
		}
		marker := varMarker(m.moduleStateForRow(r), r.VarName)
		name := r.VarName
		// Truncate the variable name to prevent wrapping.
		if len(name) > maxNameWidth {
			name = name[:maxNameWidth-1] + "…"
		}
		line := fmt.Sprintf("%s  %s", marker, name)
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
	// Pad content to exactly panelHeight lines so both panels align.
	lines := strings.Count(content, "\n")
	if lines < m.panelHeight() {
		content += strings.Repeat("\n", m.panelHeight()-lines)
	}
	panel := m.panelStyle(focusLeft)
	return panel.Width(leftWidth).Height(m.panelHeight()).Render(content)
}

// renderRightPane shows the editor inside a bordered panel.
func (m *Model) renderRightPane() string {
	var content string
	v := m.SelectedVariable()
	if v == nil {
		content = styleDescription.Render("Select a variable on the left.")
	} else {
		var b strings.Builder
		// Variable name as section title.
		name := stylePlanModule.Render(v.Name)
		typeTag := styleDescription.Render(" " + kindLabel(v.Type))
		header := name + typeTag
		if v.Sensitive {
			header += "  " + styleSensitiveTag.Render("⚿ sensitive")
		}
		if !v.HasDefault {
			header += "  " + styleRequiredTag.Render("● required")
		}
		fmt.Fprintln(&b, header)

		if desc := strings.TrimSpace(v.Description); desc != "" {
			fmt.Fprintln(&b, styleDescription.Render(desc))
		}
		fmt.Fprintln(&b)
		// If this variable is wired to an expression Atelier can't model as a
		// value (a reference like data.x.y["k"]), surface it read-only so the
		// user can see the current wiring instead of an empty field.
		if expr, wired := m.ActiveModuleState().WiredExpression(v.Name); wired {
			fmt.Fprintln(&b, styleWiredTag.Render("→ wired to expression"))
			fmt.Fprintln(&b, styleWiredExpr.Render(expr))
			fmt.Fprintln(&b)
			fmt.Fprintln(&b, styleHelp.Render("Type a value to override this reference; Ctrl+R keeps it cleared."))
			fmt.Fprintln(&b)
		}
		if m.editor != nil {
			fmt.Fprintln(&b, m.editor.View())
		}
		content = b.String()
	}
	// Account for left panel width (32) + border (2) + padding (2) + gap (1).
	rightWidth := m.width - 38
	if rightWidth < 20 {
		rightWidth = 20
	}
	// Inner content width = rightWidth minus padding (1 left + 1 right).
	innerW := rightWidth - 2
	// Word-wrap content to fit the panel's inner width, preventing
	// terminal-level wrapping that would break the height budget.
	content = wrapContent(content, innerW)
	// Scroll the right pane content if it exceeds the panel height.
	ph := m.panelHeight()
	lines := strings.Split(content, "\n")
	if len(lines) > ph {
		// Auto-scroll to keep editor cursor visible.
		if ec, ok := m.editor.(EditorWithCursor); ok {
			// Cursor line in the full content (offset by header lines: name + desc + blank).
			cursorLine := ec.CursorLine() + 3
			if cursorLine >= m.editorScroll+ph {
				m.editorScroll = cursorLine - ph + 1
			}
			if cursorLine < m.editorScroll {
				m.editorScroll = cursorLine
			}
		}
		// Clamp scroll.
		maxScroll := len(lines) - ph
		if m.editorScroll > maxScroll {
			m.editorScroll = maxScroll
		}
		if m.editorScroll < 0 {
			m.editorScroll = 0
		}
		end := m.editorScroll + ph
		if end > len(lines) {
			end = len(lines)
		}
		visible := lines[m.editorScroll:end]
		// Append scroll indicator.
		if ph > 1 && len(visible) > 0 {
			pct := 0
			if maxScroll > 0 {
				pct = m.editorScroll * 100 / maxScroll
			}
			visible[len(visible)-1] = styleHelp.Render(fmt.Sprintf("  ↕ scroll (%d%%)", pct))
		}
		content = strings.Join(visible, "\n")
	} else {
		// Pad content to exactly panelHeight lines so both panels align.
		lineCount := strings.Count(content, "\n")
		if lineCount < ph {
			content += strings.Repeat("\n", ph-lineCount)
		}
	}
	panel := m.panelStyle(focusRight)
	return panel.Width(rightWidth).Height(m.panelHeight()).Render(content)
}

// panelStyle returns the panel border style, highlighting the focused pane.
func (m *Model) panelStyle(pane focusPane) lipgloss.Style {
	if m.focus == pane {
		return stylePanelFocused
	}
	return stylePanel
}

// panelHeight returns the inner height for panels (total body minus border rows).
func (m *Model) panelHeight() int {
	h := m.bodyHeight() - 2 // subtract top + bottom border lines
	if h < 1 {
		h = 1
	}
	return h
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
	// Inner width = m.width - 2 (border); padding takes 2 more.
	contentW := m.width - 4
	gap := contentW - leftW
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap)
	return styleHeaderBar.Width(m.width - 2).Render(bar)
}

// renderFooter is the bottom bar showing transient status messages and key hints.
func (m *Model) renderFooter() string {
	var left string
	switch {
	case m.applyState == applyLoading:
		frame := spinnerFrames[m.planSpinnerFrame%len(spinnerFrames)]
		label := "Running terraform apply…"
		left = fmt.Sprintf("%s %s",
			styleStatusBusy.Render(frame),
			styleStatusBusy.Render(label+m.progressSuffix()))
	case m.refSwitching:
		frame := spinnerFrames[m.planSpinnerFrame%len(spinnerFrames)]
		label := "Switching module ref…"
		left = fmt.Sprintf("%s %s",
			styleStatusBusy.Render(frame),
			styleStatusBusy.Render(label+m.progressSuffix()))
	case m.planState == planLoading:
		frame := spinnerFrames[m.planSpinnerFrame%len(spinnerFrames)]
		label := "Running terraform plan…"
		left = fmt.Sprintf("%s %s",
			styleStatusBusy.Render(frame),
			styleStatusBusy.Render(label+m.progressSuffix()))
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
	// Inner width = m.width - 2 (border); padding takes 2 more.
	contentW := m.width - 4
	gap := contentW - leftW - hintsW
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + hints
	return styleStatusBar.Width(m.width - 2).Render(bar)
}

// progressSuffix returns a formatted string with elapsed time and current
// phase from the progress tracker, e.g. " (12s) Refreshing state…". Returns
// "" if no progress tracker is active.
func (m *Model) progressSuffix() string {
	if m.progress == nil {
		return ""
	}
	elapsed := m.progress.Elapsed().Truncate(time.Second)
	phase := m.progress.Phase()
	if phase == "" {
		return fmt.Sprintf(" (%s)", formatDuration(elapsed))
	}
	// Truncate phase to keep the footer readable.
	const maxPhaseLen = 50
	if len(phase) > maxPhaseLen {
		phase = phase[:maxPhaseLen-1] + "…"
	}
	return fmt.Sprintf(" (%s) %s", formatDuration(elapsed), phase)
}

// formatDuration renders a duration as a compact human string: "3s", "1m12s".
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) - m*60
	return fmt.Sprintf("%dm%ds", m, s)
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
		if m.planDiffFocus {
			return "[↑↓] scroll diff  [Tab/Esc] back to tree  [?] help"
		}
		hints := "[↑↓] navigate  [Enter] toggle  [Tab] focus diff  [P] re-plan"
		if m.tfState != nil {
			if m.planShowState {
				hints += "  [S] show diff"
			} else {
				hints += "  [S] show state"
			}
		}
		if m.Applier != nil && m.applyState != applyLoading {
			hints += "  [A] apply"
		}
		if m.statusLvl == statusError && m.statusDetail != "" {
			hints += "  [E] error"
		}
		hints += "  [Esc] back  [?] help"
		return hints
	}
	hints := "[Tab] pane  [↑↓] navigate  [P] plan"
	if len(m.presets) > 0 {
		hints += "  [F] preset"
	}
	if m.activeSwitcher() != nil {
		hints += "  [R] ref"
	}
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
		fmt.Fprintln(&b, "  PgUp/Ctrl+U   Half-page up")
		fmt.Fprintln(&b, "  PgDn/Ctrl+D   Half-page down")
		fmt.Fprintln(&b, "  g/G           Jump to top/bottom")
		fmt.Fprintln(&b, "  Enter/Space    Toggle collapse/expand")
		fmt.Fprintln(&b, "  [ / ]          Scroll diff pane up/down")
		fmt.Fprintln(&b, "  P              Re-run terraform plan")
		if m.tfState != nil {
			fmt.Fprintln(&b, "  S              Toggle state/diff view")
		}
		if m.Applier != nil {
			fmt.Fprintln(&b, "  A              Apply the current plan")
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
		if m.activeSwitcher() != nil {
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

// contentHeight returns the vertical space available between the bordered
// header (3 lines) and bordered footer (3 lines), minus 1 safety line for
// terminals that report height inclusive of the cursor row. All screens
// must use this as the single source of truth for their body budget.
func (m *Model) contentHeight() int {
	if m.height < 9 {
		return 1
	}
	return m.height - 7
}

// bodyHeight is an alias for contentHeight (used by the editor screen).
func (m *Model) bodyHeight() int {
	return m.contentHeight()
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
	// Variable wired to an HCL expression Atelier can't model as a value
	// (a data/var/local/module reference, index access, function call, ...).
	if _, wired := state.WiredExpression(name); wired {
		return styleMarkerExpr.Render("[→]")
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
		label := "Switching ref…" + m.progressSuffix()
		fmt.Fprintf(&b, "%s %s\n",
			styleStatusBusy.Render(frame),
			styleStatusBusy.Render(label))
		return m.renderModalFrame("Switching ref", b.String(), "")
	}

	name, source, ref, sha := m.activeRefInfo()
	if name != "" {
		fmt.Fprintf(&b, "Module:  %s\n", styleDescription.Render(name))
	}
	if source != "" {
		fmt.Fprintf(&b, "Source:  %s\n", styleDescription.Render(source))
	}
	fmt.Fprintf(&b, "Current: %s", styleDescription.Render(ref))
	if sha != "" {
		fmt.Fprintf(&b, " (%s)", shortSHA(sha))
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
