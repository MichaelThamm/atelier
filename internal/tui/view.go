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

// arrowUpDown is the up/down navigation glyph used in the single-line status
// bar hints. The bare arrows U+2191/U+2193 are East-Asian *ambiguous* width:
// lipgloss (and the layout math) count them as one column each, but many
// terminals render them with emoji (width-2) presentation. That two-column
// undercount is enough to push the footer past its box and drop the right
// border. Appending VARIATION SELECTOR-15 (U+FE0E) requests text (narrow)
// presentation, so a compliant terminal draws them at the width lipgloss
// assumes. The renderers additionally hard-clamp their content as a
// belt-and-suspenders guard for terminals that ignore the selector.
const arrowUpDown = "\u2191\ufe0e\u2193\ufe0e"

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

// truncateMiddle shortens s to at most max display columns, keeping the head
// and tail and inserting an ellipsis in the middle. Used for long refs whose
// distinguishing characters often sit at the end (e.g. dated release branches
// like "release/2026-06-07-hotfix"), where plain right-truncation would
// discard exactly the part that disambiguates them. Assumes ASCII-ish input
// (git refs), so rune count tracks display width.
func truncateMiddle(s string, max int) string {
	if max < 1 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	keep := max - 1 // reserve one column for the ellipsis
	head := (keep + 1) / 2
	tail := keep - head
	r := []rune(s)
	if tail == 0 {
		return string(r[:head]) + "…"
	}
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
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
			// Render section header: "── module@ref". The bordered panel pads
			// each line to the pane width, so no trailing decorative dashes
			// are needed (ADR-0019). Truncate the name but preserve the
			// actionable "@ref" suffix whole; in the pathological case where
			// "@ref" alone overflows we middle-truncate so the ref's
			// distinguishing head and tail both survive. Remote modules with
			// no pin get a dim "unpinned" affordance; local (unpinnable)
			// modules render as a bare name.
			name := r.VarName
			ref, source := "", ""
			if r.ModuleIdx < len(m.Modules) {
				ref = m.Modules[r.ModuleIdx].Ref
				source = m.Modules[r.ModuleIdx].SourceURL
			}
			const headerPrefix = 3 // "── "
			budget := maxVisualWidth - headerPrefix
			var label string
			switch {
			case ref != "":
				suffix := "@" + ref
				nameBudget := budget - lipgloss.Width(suffix)
				if nameBudget < 1 {
					// Ref alone overflows: middle-truncate the whole label.
					label = truncateMiddle(name+suffix, budget)
				} else {
					label = ansi.Truncate(name, nameBudget, "…") + suffix
				}
			case source != "":
				// Unpinned remote module: bare name + dim affordance.
				markerW := lipgloss.Width(unpinnedMarker) + 1 // + separating space
				nameBudget := budget - markerW
				if nameBudget < 1 {
					// No room for both: keep the name, drop the affordance.
					label = ansi.Truncate(name, budget, "…")
				} else {
					label = ansi.Truncate(name, nameBudget, "…") +
						" " + styleUnpinnedTag.Render(unpinnedMarker)
				}
			default:
				// Local (unpinnable) module: bare name only.
				label = ansi.Truncate(name, budget, "…")
			}
			line := styleSectionHeader.Render("── " + label)
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
	// Force exactly panelHeight lines so both panels' bottom borders align.
	// lipgloss's .Height() only pads short content, never trims tall content,
	// so a pane whose content exceeds the height would render a row taller
	// than its neighbour — hence the explicit clamp here.
	content = clampToLines(strings.TrimSuffix(content, "\n"), m.panelHeight())
	panel := m.panelStyle(focusLeft)
	return panel.Width(leftWidth).Height(m.panelHeight()).Render(content)
}

// clampToLines forces s to exactly n physical lines: it pads with blank lines
// when short and drops trailing lines when tall. Used by the two body panes so
// their heights (and therefore bottom borders) always match, independent of
// how lipgloss's .Height() pads.
func clampToLines(s string, n int) string {
	if n < 1 {
		n = 1
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	} else {
		for len(lines) < n {
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n")
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
	// Size the right pane to fill the remaining width so its right border
	// lines up with the full-width header/footer banners. The left panel
	// occupies leftWidth(32) content + 2 border = 34 columns (its Padding is
	// already inside Width), the gap is 1, and this panel adds its own 2
	// border columns — so body width = 34 + 1 + (rightWidth + 2). Setting
	// rightWidth = m.width - 37 makes that total exactly m.width.
	rightWidth := m.width - 37
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
		// Hard-truncate each visible line to the panel's inner width. word-wrap
		// (above) breaks on spaces only, so an over-long unbreakable token
		// (e.g. a long object field name + value) can still exceed innerW;
		// left un-truncated, lipgloss re-wraps it at Render time, adding a
		// physical row that overflows the fixed Height and shoves the bottom
		// border down — the pane then renders one row shorter than the left
		// pane. Truncating guarantees exactly ph rows.
		for i, ln := range visible {
			visible[i] = ansi.Truncate(ln, innerW, "…")
		}
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
		// Guard against the same over-long-token wrap in the unscrolled path.
		lines = strings.Split(content, "\n")
		for i, ln := range lines {
			lines[i] = ansi.Truncate(ln, innerW, "…")
		}
		content = strings.Join(lines, "\n")
	}
	// Force exactly panelHeight lines so this pane's bottom border always
	// lines up with the left pane's (see clampToLines).
	content = clampToLines(content, ph)
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
	// Append a check-warnings chip. These come from the most recent plan's
	// `check` blocks (advisory, non-blocking), so they sit alongside — not
	// inside — the validate summary and use the peach warning tint to read as
	// distinct from red errors.
	if n := len(m.checkWarnings); n > 0 {
		left += "  " + styleStatusWarning.Render(fmt.Sprintf("⚠ %d check warning(s)", n))
	}
	leftW := lipgloss.Width(left)
	// Inner width = m.width - 2 (border); padding takes 2 more.
	contentW := m.width - 4
	// Keep the header to a single line for the same reason as the footer: a
	// wrapped header grows its height and the layout (sized for one line)
	// clips the top.
	if leftW > contentW {
		left = ansi.Truncate(left, contentW, "…")
		leftW = lipgloss.Width(left)
	}
	gap := contentW - leftW
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap)
	// Belt-and-suspenders: never let the content exceed the box width, even if
	// a terminal disagrees with lipgloss on a glyph's width (see arrowUpDown).
	bar = ansi.Truncate(bar, contentW, "…")
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
	// Keep the footer to a single line. lipgloss wraps content wider than the
	// fixed Width, which would grow the footer's height and push the top of
	// the layout off-screen (the panes are sized assuming a one-line footer).
	// The hints are navigation and must stay visible, so truncate the status
	// text to whatever room is left after reserving the hints and a gap.
	availLeft := contentW - hintsW - 1
	switch {
	case availLeft <= 0:
		left, leftW = "", 0
	case leftW > availLeft:
		left = ansi.Truncate(left, availLeft, "…")
		leftW = lipgloss.Width(left)
	}
	gap := contentW - leftW - hintsW
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + hints
	// Belt-and-suspenders: never let the content exceed the box width, even if
	// a terminal disagrees with lipgloss on a glyph's width (see arrowUpDown).
	bar = ansi.Truncate(bar, contentW, "…")
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
			return "[" + arrowUpDown + "] scroll diff  [Tab/Esc] back to tree  [?] help"
		}
		hints := "[" + arrowUpDown + "] navigate  [Enter] toggle  [Tab] focus diff  [P] re-plan"
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
		if len(m.checkWarnings) > 0 {
			hints += "  [W] warnings"
		}
		if m.statusLvl == statusError && m.statusDetail != "" {
			hints += "  [E] error"
		}
		hints += "  [Esc] back  [?] help"
		return hints
	}
	hints := "[Tab] pane  [" + arrowUpDown + "] navigate  [P] plan"
	if len(m.presets) > 0 {
		hints += "  [F] preset"
	}
	hints += "  [S] save"
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
		if len(m.checkWarnings) > 0 {
			fmt.Fprintln(&b, "  W              Show check warnings")
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
		fmt.Fprintln(&b, "  S              Save current config as a preset")
		if m.activeSwitcher() != nil {
			fmt.Fprintln(&b, "  R              Switch module ref")
		}
		if m.statusLvl == statusError && m.statusDetail != "" {
			fmt.Fprintln(&b, "  E              Show error details")
		}
		fmt.Fprintln(&b, "  Q              Quit (auto-saves)")

		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "Editing a value (string, number, map cell):")
		fmt.Fprintln(&b, "  ← →            Move caret one char")
		fmt.Fprintln(&b, "  Ctrl+← Ctrl+→  Move caret one word")
		fmt.Fprintln(&b, "  Alt+B  Alt+F   Move caret one word (Emacs)")
		fmt.Fprintln(&b, "  Home/Ctrl+A    Caret to start")
		fmt.Fprintln(&b, "  End/Ctrl+E     Caret to end")
		fmt.Fprintln(&b, "  Backspace      Delete char before caret")
		fmt.Fprintln(&b, "  Delete         Delete char under caret")
		fmt.Fprintln(&b, "  Ctrl+W         Delete word before caret")
		fmt.Fprintln(&b, "  Alt+Backspace  Delete word before caret")
		fmt.Fprintln(&b, "  Alt+D          Delete word after caret")
		fmt.Fprintln(&b, "  Ctrl+U         Delete to start of line")
		fmt.Fprintln(&b, "  Ctrl+K         Delete to end of line")

		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "Map / map(object) editors:")
		fmt.Fprintln(&b, "  ↑ ↓            Move between rows")
		fmt.Fprintln(&b, "  Enter          Advance: key → value → next row;")
		fmt.Fprintln(&b, "                 on the last value, add a new row;")
		fmt.Fprintln(&b, "                 on a map(object) key, drill into the object")
		fmt.Fprintln(&b, "  Esc            Back one level (then to the variable list)")
		fmt.Fprintln(&b, "  Alt+Delete     Remove current row (twice to confirm if set)")
		fmt.Fprintln(&b, "  Tab            Switch panes (variable list ⇄ editor)")
		fmt.Fprintln(&b, "  Ctrl+Home/End  Jump to first/last field (in an object)")

		if m.activeSwitcher() != nil {
			fmt.Fprintln(&b)
			fmt.Fprintln(&b, "Ref switch modal (R):")
			fmt.Fprintln(&b, "  type           Filter the ref list (substring match)")
			fmt.Fprintln(&b, "  ↑ ↓            Move the highlight in the ref list")
			fmt.Fprintln(&b, "  Tab            Fill the field with the highlighted ref")
			fmt.Fprintln(&b, "  Enter          Switch to the typed ref (free-text ok)")
			fmt.Fprintln(&b, "  Esc            Cancel")
			fmt.Fprintln(&b, "                 (editing keys above apply in the field)")
		}
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

// renderWarnDetail renders a centered modal listing the failed `check` block
// assertions from the most recent plan. These are advisory warnings that do
// not block plan or apply (unlike errors), so they get their own peach-tinted
// surface distinct from the red error modal.
func (m *Model) renderWarnDetail() string {
	title := styleStatusWarning.Render(fmt.Sprintf("⚠ %d check warning(s)", len(m.checkWarnings)))
	return m.renderModalFrame(title, formatCheckWarnings(m.checkWarnings), "[Esc] close")
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

// renderSavePresetModal renders the "save current configuration as a preset"
// overlay: a name field, a description field, and a count of how many
// non-default variables will be captured (ADR-0026).
func (m *Model) renderSavePresetModal() string {
	var b strings.Builder

	if name, _, _, _ := m.activeRefInfo(); name != "" {
		fmt.Fprintf(&b, "Module:  %s\n", styleDescription.Render(name))
	}
	_, n := snapshotPreset(m.State, "", "")
	noun := "variables"
	if n == 1 {
		noun = "variable"
	}
	fmt.Fprintf(&b, "Captures %s from the current configuration.\n",
		styleDescription.Render(fmt.Sprintf("%d non-default %s", n, noun)))
	fmt.Fprintln(&b)

	// Two labelled cells; the focused one draws a caret. Widths track the
	// modal's inner budget so long input scrolls rather than wrapping.
	innerW := m.width - 8
	if innerW < 30 {
		innerW = 30
	}
	fieldW := innerW - len("Description:  ")
	if fieldW < 8 {
		fieldW = 8
	}
	m.savePresetName.SetWidth(fieldW)
	m.savePresetDesc.SetWidth(fieldW)

	fmt.Fprintf(&b, "Name:         %s\n", m.savePresetName.ViewInline())
	fmt.Fprintf(&b, "Description:  %s\n", m.savePresetDesc.ViewInline())
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, styleDescription.Render(fmt.Sprintf("Writes a new %s in this wrapper (secrets excluded).",
		"atelier.local.yaml")))

	return m.renderModalFrame("Save preset", b.String(),
		"[Tab] name/desc   [Enter] save   [Esc] cancel")
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

	// When the wrapper opened on a broken ref, explain why the schema is
	// unavailable right above the input so the modal is self-documenting.
	if m.RefUnresolved != nil && m.RefUnresolved.Reason != "" {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, styleStatusError.Render("⚠ "+m.RefUnresolved.Reason))
		fmt.Fprintln(&b, styleDescription.Render("Switch to a valid ref to load the module's variables."))
	}

	fmt.Fprintln(&b)
	// Inner width matches renderModalFrame's budget; scroll the field and
	// middle-truncate list rows to it so the frame never word-wraps them.
	innerW := m.width - 8
	if innerW < 30 {
		innerW = 30
	}
	inputW := innerW - len("New ref: ")
	if inputW < 8 {
		inputW = 8
	}
	m.refInput.SetWidth(inputW)
	fmt.Fprintf(&b, "New ref: %s\n", m.refInput.ViewInline())

	// Filterable, selectable ref list. Fetched asynchronously; show a loading
	// note while in flight, the substring-filtered matches once available, and
	// a free-text hint when nothing matches (ADR-0025).
	switch {
	case m.refsLoading:
		fmt.Fprintln(&b, styleDescription.Render("  loading refs…"))
		padLines(&b, refMatchWindow) // keep the frame height constant
	case len(m.availableRefs) == 0:
		// Remote unreachable or has no refs: the field stays free-text.
		padLines(&b, refMatchWindow+1)
	case len(m.refMatches) == 0:
		fmt.Fprintln(&b, styleDescription.Render("  no matching refs · [Enter] switches to the typed ref"))
		padLines(&b, refMatchWindow) // keep the frame height constant
	default:
		renderRefMatchList(&b, m.refMatches, m.refMatchCursor, innerW)
	}

	if m.refErr != "" {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, styleStatusError.Render("Error: "+m.refErr))
	}

	return m.renderModalFrame("Switch module ref", b.String(),
		arrowUpDown+" select   [Tab] fill   [Enter] switch   [Esc] cancel")
}

// refMatchWindow is the number of ref rows shown at once in the match list;
// longer match sets scroll to keep the highlighted row visible.
const refMatchWindow = 8

// padLines writes n blank lines, used to keep the ref modal's height (and thus
// its border) constant across its loading/empty/populated states.
func padLines(b *strings.Builder, n int) {
	for i := 0; i < n; i++ {
		fmt.Fprintln(b)
	}
}

// renderRefMatchList renders up to refMatchWindow filtered refs with a
// highlight on cursor, a scroll window that keeps the cursor visible, and a
// position/count footer. Rows are middle-truncated to innerW so the modal
// frame never word-wraps a long branch name.
func renderRefMatchList(b *strings.Builder, matches []string, cursor, innerW int) {
	total := len(matches)
	start := 0
	if total > refMatchWindow {
		start = cursor - refMatchWindow/2
		if start < 0 {
			start = 0
		}
		if start > total-refMatchWindow {
			start = total - refMatchWindow
		}
	}
	end := start + refMatchWindow
	if end > total {
		end = total
	}
	rowW := innerW - 2 // account for the "▸ " / "  " gutter
	if rowW < 4 {
		rowW = 4
	}
	for i := start; i < end; i++ {
		name := truncateMiddle(matches[i], rowW)
		if i == cursor {
			fmt.Fprintln(b, styleCursorActive.Render("▸ "+name))
		} else {
			fmt.Fprintln(b, "  "+name)
		}
	}
	// Pad to a fixed window so the modal frame's height (and thus its border)
	// stays constant regardless of how many refs currently match.
	for i := end - start; i < refMatchWindow; i++ {
		fmt.Fprintln(b)
	}
	fmt.Fprintln(b, styleDescription.Render(fmt.Sprintf("  %d/%d", cursor+1, total)))
}
