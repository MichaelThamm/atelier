package tui

import (
	"fmt"
	"strings"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/wrapper"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/zclconf/go-cty/cty"
)

// renderLeftPane draws the variable list inside a bordered panel.
func (m *Model) renderLeftPane() string {
	const leftWidth = 32
	// Max visual chars for line content inside the panel border.
	// Border takes 2 chars (left+right), leaving leftWidth-2 for content
	// (padding is handled by lipgloss inside this budget).
	const maxVisualWidth = leftWidth - 2
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
		// Truncate the variable name to prevent wrapping.  Compute the
		// budget from the actual marker width so wider markers like [✓10]
		// don't overflow the panel.
		markerW := lipgloss.Width(marker) + 2 // marker + separating spaces
		nameBudget := maxVisualWidth - markerW
		if nameBudget < 1 {
			nameBudget = 1
		}
		if len(name) > nameBudget {
			name = name[:nameBudget-1] + "…"
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
	bar = ansi.TruncateWc(bar, contentW, "…")
	return styleHeaderBar.Width(m.width - 2).Render(bar)
}

// renderFooter is the bottom bar showing transient status messages and key hints.
func (m *Model) renderFooter() string {
	if m.height < 15 {
		hints := styleHelp.Render("[?] help")
		contentW := m.width - 4
		gap := contentW - lipgloss.Width(hints)
		if gap < 1 {
			gap = 1
		}
		bar := strings.Repeat(" ", gap) + hints
		return styleStatusBar.Width(m.width - 2).Render(bar)
	}
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
	// TruncateWc counts ambiguous-width glyphs (like ↑↓) at 2 cells, matching
	// terminals that ignore the variation selector and render them at emoji
	// width. This is the only safe clamp; the rest of the width math uses
	// runewidth (1 cell per arrow) and can undercount.
	bar = ansi.TruncateWc(bar, contentW, "…")
	return styleStatusBar.Width(m.width - 2).Render(bar)
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
