package tui

import (
	"fmt"
	"strings"
)

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
		if m.refDetailText != "" {
			fmt.Fprintln(&b, "  D              Show ref switch summary")
		}
		if m.progress != nil && (len(m.progress.StderrLines()) > 0 || len(m.progress.StdoutLines()) > 0) {
			fmt.Fprintln(&b, "  L              View terraform logs")
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
		if m.refDetailText != "" {
			fmt.Fprintln(&b, "  D              Show ref switch summary")
		}
		if m.progress != nil && (len(m.progress.StderrLines()) > 0 || len(m.progress.StdoutLines()) > 0) {
			fmt.Fprintln(&b, "  L              View terraform logs")
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

		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "Logs view (L):")
		fmt.Fprintln(&b, "  ↑/k  ↓/j      Scroll up/down")
		fmt.Fprintln(&b, "  PgUp/Ctrl+U   Half-page up")
		fmt.Fprintln(&b, "  PgDn/Ctrl+D   Half-page down")
		fmt.Fprintln(&b, "  g/G           Jump to top/bottom")
		fmt.Fprintln(&b, "  L/Tab/Esc     Return to previous view")

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

// renderRefDetail renders a centered modal showing the full ref switch summary
// (orphaned vars, new vars, init status) that doesn't fit in the footer.
func (m *Model) renderRefDetail() string {
	return m.renderModalFrame("Ref switch summary", m.refDetailText, "[Esc] close")
}

// renderWarnDetail renders a centered modal listing the failed `check` block
// assertions from the most recent plan. These are advisory warnings that do
// not block plan or apply (unlike errors), so they get their own peach-tinted
// surface distinct from the red error modal.
func (m *Model) renderWarnDetail() string {
	title := styleStatusWarning.Render(fmt.Sprintf("⚠ %d check warning(s)", len(m.checkWarnings)))
	return m.renderModalFrame(title, formatCheckWarnings(m.checkWarnings), "[Esc] close")
}

// renderPresetPicker renders a centered modal for bundle selection. Each row
// shows the source ([local] personal walk-up, [repo] committed to the module)
// and the description from the file's leading comment.
func (m *Model) renderPresetPicker() string {
	var b strings.Builder
	for i, p := range m.presets {
		cursor := "  "
		name := p.Name
		if p.Source != "" {
			name = "[" + p.Source + "] " + name
		}
		if i == m.presetCursor {
			cursor = styleCursorActive.Render("▸ ")
			name = styleCursorActive.Render(name)
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
	_, n := 0, len(snapshotValues(m.State))
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
	fmt.Fprintln(&b, styleDescription.Render("Writes a new atelier.presets/<name>.tfvars bundle."))

	return m.renderModalFrame("Save preset", b.String(),
		"[Tab] name/desc   [Enter] save   [Esc] cancel")
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
