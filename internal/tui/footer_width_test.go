package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TestFooter_neverOverflowsBox guards against the footer's content exceeding
// its bordered box — the bug where the ambiguous-width ↑↓ glyph was
// undercounted by lipgloss, pushing the hints past the right border so it
// vanished. Every rendered line must fit within m.width across a range of
// widths, including ones where the full hint string nearly fills the bar.
func TestFooter_neverOverflowsBox(t *testing.T) {
	for _, width := range []int{60, 78, 80, 82, 100, 120} {
		m := New(sampleState(t), "cos_lite")
		m = feed(m, tea.WindowSizeMsg{Width: width, Height: 24})
		// Make the widest hint set active: presets + ref switcher present.
		m.SetPresets([]ResolvedPreset{{Name: "production"}})

		for _, part := range []struct {
			name string
			s    string
		}{
			{"footer", m.renderFooter()},
			{"header", m.renderHeader()},
		} {
			for i, line := range strings.Split(part.s, "\n") {
				if w := lipgloss.Width(line); w > width {
					t.Errorf("width=%d %s line %d has width %d > %d:\n%q",
						width, part.name, i, w, width, line)
				}
			}
		}
	}
}

// TestFooter_hasRightBorderWithArrowHints asserts the footer keeps its right
// border glyph on the content line when the arrow-bearing hints are shown.
func TestFooter_hasRightBorderWithArrowHints(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 82, Height: 24})

	footer := m.renderFooter()
	// The rounded border's right edge is "│"; the content line must end with it.
	lines := strings.Split(footer, "\n")
	if len(lines) < 3 {
		t.Fatalf("footer should render as a 3-line bordered box; got %d lines:\n%s", len(lines), footer)
	}
	content := lines[1] // middle line carries the hints
	if !strings.Contains(stripANSI(content), "navigate") {
		t.Fatalf("content line missing hints:\n%q", content)
	}
	if !strings.HasSuffix(stripANSI(content), "│") {
		t.Errorf("footer content line lost its right border:\n%q", stripANSI(content))
	}
}
