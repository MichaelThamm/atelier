package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// wiredState returns a sampleState whose `model_uuid` variable is wired to a
// data-source reference Atelier can't model as a concrete value.
func wiredState(t *testing.T) *wrapper.State {
	t.Helper()
	st := sampleState(t)
	st.UnknownAttrs = []wrapper.RawAttr{
		{
			Name:    "model_uuid",
			Raw:     []byte(`model_uuid = data.juju_model.m.uuid`),
			RawExpr: []byte(`data.juju_model.m.uuid`),
		},
	}
	return st
}

func TestVarMarker_wiredExpression(t *testing.T) {
	st := wiredState(t)
	got := stripANSI(varMarker(st, "model_uuid"))
	if got != "[→]" {
		t.Errorf("wired var marker = %q, want %q", got, "[→]")
	}
	// A non-wired var with no value still shows the empty marker.
	if m := stripANSI(varMarker(st, "count")); m == "[→]" {
		t.Errorf("non-wired var should not get the wired marker, got %q", m)
	}
}

func TestRenderRightPane_showsWiredExpression(t *testing.T) {
	m := New(wiredState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if m.SelectedVariable().Name != "model_uuid" {
		t.Fatalf("setup: selected %s", m.SelectedVariable().Name)
	}
	out := stripANSI(m.View())
	if !strings.Contains(out, "data.juju_model.m.uuid") {
		t.Errorf("right pane should display the wired expression, got:\n%s", out)
	}
	if !strings.Contains(out, "wired to expression") {
		t.Errorf("right pane should label the wired expression, got:\n%s", out)
	}
}

func TestWired_navigationDoesNotClobberExpression(t *testing.T) {
	m := New(wiredState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	// Focus the editor for the wired var, then press a non-editing key.
	m = feed(m, key("tab"), key("left"))

	if _, ok := m.State.Values["model_uuid"]; ok {
		t.Errorf("focusing/navigating a wired var must not write a value")
	}
	if _, wired := m.State.WiredExpression("model_uuid"); !wired {
		t.Errorf("wired expression must be preserved after navigation")
	}
}

func TestWired_typingOverridesExpression(t *testing.T) {
	m := New(wiredState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = feed(m, key("tab")) // focus editor

	m = feed(m, key("a"), key("b"), key("c"))

	val, ok := m.State.Values["model_uuid"]
	if !ok {
		t.Fatalf("typing should store a concrete value")
	}
	if val.AsString() != "abc" {
		t.Errorf("stored value = %q, want %q", val.AsString(), "abc")
	}
	if _, wired := m.State.WiredExpression("model_uuid"); wired {
		t.Errorf("typing a value should drop the preserved expression")
	}
}
