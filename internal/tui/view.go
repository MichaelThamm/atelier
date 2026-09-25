package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	// Inner height = terminal height minus border (2), padding (2), title (1),
	// blank×2 (2), footer (1).
	innerH := m.height - 8
	if innerH < 3 {
		innerH = 3
	}

	// Word-wrap body to the available inner width.
	body = wrapContent(body, innerW)

	// Trim trailing newlines so that strings.Split doesn't produce a phantom
	// empty element that inflates the line count by one (causing the frame to
	// overflow the terminal height and produce a stray line above the modal).
	body = strings.TrimRight(body, "\n")

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

// contentHeight returns the vertical space available between the bordered
// header (3 lines) and bordered footer (3 lines). All screens must use
// this as the single source of truth for their body budget.
func (m *Model) contentHeight() int {
	if m.height < 7 {
		return 1
	}
	return m.height - 6
}

// bodyHeight is an alias for contentHeight (used by the editor screen).
func (m *Model) bodyHeight() int {
	return m.contentHeight()
}

// padLines writes n blank lines, used to keep the ref modal's height (and thus
// its border) constant across its loading/empty/populated states.
func padLines(b *strings.Builder, n int) {
	for i := 0; i < n; i++ {
		fmt.Fprintln(b)
	}
}
