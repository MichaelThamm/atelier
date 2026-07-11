package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// Ctrl+R from the left pane should drop the selected variable's entry
// from state.Values entirely. The variable then reads as "at default" —
// its left-pane marker returns to "[ ]" and the sparse-write rule will
// omit it from main.tf.
func TestReset_fromLeftPane_clearsValuesEntry(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Set internal_tls (index 1) to a non-default value.
	m = feed(m, key("down"), key("tab"), key(" "), key("esc"))
	if v, ok := m.State.Values["internal_tls"]; !ok || v.True() {
		t.Fatalf("setup: internal_tls should be set to false; got ok=%v v=%v", ok, v.GoString())
	}
	if m.SelectedVariable().Name != "internal_tls" {
		t.Fatalf("setup: SelectedVariable = %v", m.SelectedVariable())
	}

	// Ctrl+R from the left pane.
	m = feed(m, key("ctrl+r"))
	if _, ok := m.State.Values["internal_tls"]; ok {
		t.Errorf("after Ctrl+R, internal_tls should be removed from state.Values")
	}
	if !m.dirty {
		t.Errorf("reset should mark state dirty so SaveIfDirty rewrites main.tf")
	}
	// Marker should now be at-default.
	marker := stripANSI(varMarker(m.State, "internal_tls"))
	if marker != "[ ]" {
		t.Errorf("post-reset marker = %q; want [ ]", marker)
	}
}

// Editor mid-edit + Ctrl+R should rebuild the editor with the default. The
// user's in-progress buffer should disappear.
func TestReset_fromRightPane_rebuildsEditor(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Focus model_uuid (a required string) and type some chars.
	m = feed(m, key("tab"), key("a"), key("b"), key("c"))
	if v := m.State.Values["model_uuid"]; v.AsString() != "abc" {
		t.Fatalf("setup: model_uuid = %v", v.GoString())
	}

	// Ctrl+R — required variable, no default to fall back to, so state
	// entry is removed and the editor is reset to empty.
	m = feed(m, key("ctrl+r"))
	if _, ok := m.State.Values["model_uuid"]; ok {
		t.Errorf("after Ctrl+R, required variable should be unset")
	}
	// The marker should be [!] (required, unset) again.
	if stripANSI(varMarker(m.State, "model_uuid")) != "[!]" {
		t.Errorf("required-after-reset marker should be [!]")
	}
}

// Ctrl+R inside an object editor should reset only the focused field,
// leaving sibling fields untouched.
func TestReset_objectField_leavesSiblingsAlone(t *testing.T) {
	state := sampleState(t)
	state.Vars = append(state.Vars, alertmanagerLikeVar(t))
	m := New(state, "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	// Navigate to alertmanager (3 downs past the 3 scalars).
	m = feed(m, key("down"), key("down"), key("down"))
	if m.SelectedVariable().Name != "alertmanager" {
		t.Fatalf("setup: SelectedVariable = %v", m.SelectedVariable())
	}
	// Enter the object editor.
	m = feed(m, key("tab"))

	// Edit app_name: backspace twice, type "X". Default was "alertmanager".
	m = feed(m, key("backspace"), key("backspace"), key("X"))
	// Move to units and bump it from "1" → "9".
	m = feed(m, key("down"), key("down"), key("backspace"), key("9"))

	// Sanity-check both edits landed: "alertmanager" minus two trailing
	// chars plus an X → "alertmanagX".
	val := m.State.Values["alertmanager"].AsValueMap()
	if got := val["app_name"].AsString(); got != "alertmanagX" {
		t.Fatalf("setup app_name = %q", got)
	}
	if !val["units"].Equals(cty.NumberFloatVal(9)).True() {
		t.Fatalf("setup units = %v", val["units"].GoString())
	}

	// Cursor is on units. Ctrl+R should reset only units.
	m = feed(m, key("ctrl+r"))
	val = m.State.Values["alertmanager"].AsValueMap()
	if !val["units"].Equals(cty.NumberFloatVal(1)).True() {
		t.Errorf("units after reset = %v; want 1 (declared default)", val["units"].GoString())
	}
	if got := val["app_name"].AsString(); got != "alertmanagX" {
		t.Errorf("app_name should be untouched by field-level reset; got %q", got)
	}
}

// After a whole-object reset, the marker collapses back to [ ] and no
// fields appear in the sparse value (so main.tf would emit nothing).
func TestReset_objectFromLeftPane_clearsAllFields(t *testing.T) {
	state := sampleState(t)
	state.Vars = append(state.Vars, alertmanagerLikeVar(t))
	m := New(state, "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	m = feed(m, key("down"), key("down"), key("down"))
	m = feed(m, key("tab"))
	// Touch a couple of fields.
	m = feed(m, key("backspace"), key("X"))
	m = feed(m, key("down"), key("down"), key("backspace"), key("7"))
	// Back to left pane.
	m = feed(m, key("esc"))
	if m.focus != focusLeft {
		t.Fatalf("focus = %v", m.focus)
	}

	// Ctrl+R on the variable: drops the whole entry.
	m = feed(m, key("ctrl+r"))
	if _, ok := m.State.Values["alertmanager"]; ok {
		t.Errorf("after whole-object reset, alertmanager should not appear in state.Values")
	}
	// Sparse-write would emit nothing.
	v := m.State.FindVar("alertmanager")
	cur, _ := m.State.VariableValue("alertmanager")
	if wrapper.ShouldEmit(v, cur) {
		t.Errorf("after reset, ShouldEmit should be false")
	}
	// Marker collapses to [ ].
	if stripANSI(varMarker(m.State, "alertmanager")) != "[ ]" {
		t.Errorf("post-reset marker should be [ ]")
	}
}

// Ctrl+R on a variable wired to a reference expression must RESTORE the
// preserved raw form (the [→] view), not wipe it. The user expects reset to
// undo their concrete override and bring back the original reference.
func TestReset_restoresPreservedExpression(t *testing.T) {
	state := sampleState(t)
	state.UnknownAttrs = []wrapper.RawAttr{
		{
			Name:    "model_uuid",
			Raw:     []byte("model_uuid = data.juju_model.m.uuid"),
			RawExpr: []byte("data.juju_model.m.uuid"),
		},
	}
	m := New(state, "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// model_uuid is the first variable. Override it with a concrete value...
	if m.SelectedVariable().Name != "model_uuid" {
		t.Fatalf("setup: SelectedVariable = %v", m.SelectedVariable())
	}
	m = feed(m, key("tab"), key("a"), key("b"), key("c"))
	if v := m.State.Values["model_uuid"]; v.AsString() != "abc" {
		t.Fatalf("setup: override did not land; got %v", v.GoString())
	}

	// ...then reset. The concrete override should be gone and the original
	// reference should resurface.
	m = feed(m, key("ctrl+r"))
	if _, ok := m.State.Values["model_uuid"]; ok {
		t.Errorf("after reset, the concrete override should be removed from Values")
	}
	expr, wired := m.State.WiredExpression("model_uuid")
	if !wired || expr != "data.juju_model.m.uuid" {
		t.Errorf("after reset, the wired expression should be restored; got %q wired=%v", expr, wired)
	}
	if got := stripANSI(varMarker(m.State, "model_uuid")); got != "[→]" {
		t.Errorf("post-reset marker = %q; want [→]", got)
	}
}

// Status bar should surface a confirmation when a reset happens so the
// user isn't left wondering whether anything occurred.
func TestReset_setsStatusMessage(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = feed(m, key("down"), key("tab"), key(" "), key("esc")) // toggle internal_tls
	m = feed(m, key("ctrl+r"))
	if !strings.Contains(m.status, "reset") {
		t.Errorf("status bar should mention reset; got %q", m.status)
	}
}

// Reset should appear in the status-bar help hints so a first-time user
// can discover it.
func TestReset_appearsInStatusHints(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	// Ctrl+R is shown in the help modal, not the compact status hints.
	m.helpModal = true
	view := m.View()
	if !strings.Contains(view, "Ctrl+R") {
		t.Errorf("help modal should advertise Ctrl+R reset; got %q", view)
	}
}
