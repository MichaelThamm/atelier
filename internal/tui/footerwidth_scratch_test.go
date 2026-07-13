package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestScratch_arrowWidth(t *testing.T) {
	for _, s := range []string{"↑↓", "↑\ufe0e↓\ufe0e", "\u2191\u2193", "[↑\ufe0e↓\ufe0e] navigate"} {
		t.Logf("%q lipgloss.Width=%d runecount=%d", s, lipgloss.Width(s), len([]rune(s)))
	}
}
