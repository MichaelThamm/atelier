package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestHelpModal_pointsAtLogs keeps the `?` modal honest about where terraform's
// output goes: it must name .atelier/logs/ and must not promise that the plan
// view names the file, which it does not.
func TestHelpModal_pointsAtLogs(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 80})
	m.helpModal = true
	plain := stripANSI(m.renderHelpModal())

	if !strings.Contains(plain, ".atelier/logs/") {
		t.Errorf("help modal does not name .atelier/logs/; got:\n%s", plain)
	}
	if strings.Contains(plain, "the plan view names the file") {
		t.Errorf("help modal still promises the plan view names the file; got:\n%s", plain)
	}
}
