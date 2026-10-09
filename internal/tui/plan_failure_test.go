package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestPlanFailure_opensFailureViewWithLogs covers the failed-plan UX: a plan
// error keeps the user in a failure view showing terraform's first line and the
// path to the full output, rather than dropping back to the editor with the
// detail squeezed into (and truncated out of) the one-line footer.
func TestPlanFailure_opensFailureViewWithLogs(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m.WrapperDir = "/tmp/wrap"
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = feed(m, planErrorMsg{err: errors.New("Error: boom\nsecond line")})

	if m.planState != planFailed {
		t.Fatalf("planState = %v; want planFailed", m.planState)
	}
	plain := stripANSI(m.View())
	for _, want := range []string{"Plan failed", "Error: boom", "/tmp/wrap/.atelier/logs/tf-stderr.log"} {
		if !strings.Contains(plain, want) {
			t.Errorf("failure view missing %q; got:\n%s", want, plain)
		}
	}
}

// TestPlanFailure_escReturnsToEditor pins the way out: Esc from the failure view
// returns to the editor, where the wrapper can be fixed and P re-plans.
func TestPlanFailure_escReturnsToEditor(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = feed(m, planErrorMsg{err: errors.New("Error: boom")})

	m = feed(m, key("esc"))
	if m.planState != planIdle {
		t.Errorf("planState after Esc = %v; want planIdle", m.planState)
	}
	if m.planErr != "" {
		t.Errorf("planErr after Esc = %q; want empty", m.planErr)
	}
}

// TestStatusHints_planFailed covers the failure view's footer: it must offer the
// way out (Esc) and the re-plan key, not the normal editor hints.
func TestStatusHints_planFailed(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.planState = planFailed
	m.planErr = "Error: boom"

	got := m.statusHints()
	for _, want := range []string{"[Esc] back", "[P] re-plan"} {
		if !strings.Contains(got, want) {
			t.Errorf("statusHints() = %q; want it to contain %q", got, want)
		}
	}
}
