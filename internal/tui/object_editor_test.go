package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// alertmanagerLikeVar builds a variable that mirrors the COS Lite
// `alertmanager` shape: a mix of optional string/number/object/map fields
// with their own defaults. Used as the primary test fixture for the object
// editor.
func alertmanagerLikeVar(t *testing.T) tfvars.Variable {
	t.Helper()
	tp, err := tftypes.ParseTypeExpr(`object({
		app_name           = optional(string, "alertmanager")
		constraints        = optional(string, "arch=amd64")
		units              = optional(number, 1)
		trust              = optional(bool, true)
		storage_directives = optional(map(string), {})
		config             = optional(object({
			templates_file = optional(string, "")
		}), {})
	})`)
	if err != nil {
		t.Fatal(err)
	}
	return tfvars.Variable{
		Name:       "alertmanager",
		Type:       tp,
		HasDefault: true,
		Default:    cty.EmptyObjectVal,
	}
}

func objectEditorOf(t *testing.T, v tfvars.Variable) *objectEditor {
	t.Helper()
	ed := newEditor(&v, cty.NilVal)
	oe, ok := ed.(*objectEditor)
	if !ok {
		t.Fatalf("expected *objectEditor, got %T", ed)
	}
	return oe
}

// drive runs a sequence of key strings through the object editor's Update.
func drive(t *testing.T, oe *objectEditor, keys ...string) *objectEditor {
	t.Helper()
	var ed Editor = oe
	for _, k := range keys {
		ed, _ = ed.Update(key(k))
	}
	return ed.(*objectEditor)
}

func TestObjectEditor_initialCursorOnFirstField(t *testing.T) {
	oe := objectEditorOf(t, alertmanagerLikeVar(t))
	if oe.cursor != 0 {
		t.Errorf("cursor = %d, want 0", oe.cursor)
	}
	if oe.fields[oe.cursor].Name != "app_name" {
		t.Errorf("first field = %q", oe.fields[oe.cursor].Name)
	}
}

func TestObjectEditor_arrowKeys_navigate(t *testing.T) {
	oe := objectEditorOf(t, alertmanagerLikeVar(t))
	oe = drive(t, oe, "down")
	if oe.fields[oe.cursor].Name != "constraints" {
		t.Errorf("after down, focused = %q", oe.fields[oe.cursor].Name)
	}
	oe = drive(t, oe, "down", "down")
	if oe.fields[oe.cursor].Name != "trust" {
		t.Errorf("after three downs, focused = %q (want trust)", oe.fields[oe.cursor].Name)
	}
	oe = drive(t, oe, "up")
	if oe.fields[oe.cursor].Name != "units" {
		t.Errorf("after up, focused = %q (want units)", oe.fields[oe.cursor].Name)
	}
}

func TestObjectEditor_clampsAtBoundaries(t *testing.T) {
	oe := objectEditorOf(t, alertmanagerLikeVar(t))
	// Up at top: stays at 0.
	oe = drive(t, oe, "up", "up", "up")
	if oe.cursor != 0 {
		t.Errorf("up from top: cursor = %d", oe.cursor)
	}
	// Down past end: clamps to last.
	for i := 0; i < 100; i++ {
		oe = drive(t, oe, "down")
	}
	if oe.cursor != len(oe.fields)-1 {
		t.Errorf("down past end: cursor = %d, want %d", oe.cursor, len(oe.fields)-1)
	}
}

func TestObjectEditor_typingIntoStringField(t *testing.T) {
	oe := objectEditorOf(t, alertmanagerLikeVar(t))
	// Focus is on app_name (string). Type "x".
	oe = drive(t, oe, "x", "y", "z")
	val := oe.CurrentValue().AsValueMap()["app_name"]
	if val.AsString() != "alertmanagerxyz" {
		t.Errorf("string field after typing: %v", val.GoString())
	}
}

func TestObjectEditor_toggleBoolField(t *testing.T) {
	oe := objectEditorOf(t, alertmanagerLikeVar(t))
	// Navigate to trust (4th field, index 3).
	oe = drive(t, oe, "down", "down", "down")
	if oe.fields[oe.cursor].Name != "trust" {
		t.Fatalf("setup: focused = %q", oe.fields[oe.cursor].Name)
	}
	oe = drive(t, oe, "space")
	val := oe.CurrentValue().AsValueMap()["trust"]
	if val.True() {
		t.Errorf("trust should be false after toggle; got %v", val.GoString())
	}
	oe = drive(t, oe, "space")
	val = oe.CurrentValue().AsValueMap()["trust"]
	if !val.True() {
		t.Errorf("trust should be true after second toggle")
	}
}

func TestObjectEditor_typeIntoNumberField(t *testing.T) {
	oe := objectEditorOf(t, alertmanagerLikeVar(t))
	// Navigate to units (index 2).
	oe = drive(t, oe, "down", "down")
	if oe.fields[oe.cursor].Name != "units" {
		t.Fatalf("setup: focused = %q", oe.fields[oe.cursor].Name)
	}
	// Pre-populated with "1"; backspace then type "5".
	oe = drive(t, oe, "backspace", "5")
	val := oe.CurrentValue().AsValueMap()["units"]
	if !val.Equals(cty.NumberFloatVal(5)).True() {
		t.Errorf("units after re-typing = %v; want 5", val.GoString())
	}
}

func TestObjectEditor_arrowsDontLeakToSubEditor(t *testing.T) {
	oe := objectEditorOf(t, alertmanagerLikeVar(t))
	// On string field, arrow keys should move the cursor, NOT type "up"
	// or "down" into the string buffer.
	oe = drive(t, oe, "down")
	val := oe.CurrentValue().AsValueMap()["app_name"]
	if val.AsString() != "alertmanager" {
		t.Errorf("string field changed after navigation: %v", val.GoString())
	}
}

func TestObjectEditor_collectionFields_swallowsKeys(t *testing.T) {
	oe := objectEditorOf(t, alertmanagerLikeVar(t))
	// Navigate to storage_directives (map).
	for oe.fields[oe.cursor].Name != "storage_directives" {
		oe = drive(t, oe, "down")
	}
	// Keys forwarded to a map sub-editor would change its content. Verify
	// the map stays empty (current behaviour: collection editing inside an
	// object is not yet wired).
	oe = drive(t, oe, "a", "a", "a")
	val := oe.CurrentValue().AsValueMap()["storage_directives"]
	if val.LengthInt() != 0 {
		t.Errorf("collection field absorbed key presses; got %d entries", val.LengthInt())
	}
}

func TestObjectEditor_currentValue_propagatesAllFields(t *testing.T) {
	oe := objectEditorOf(t, alertmanagerLikeVar(t))
	val := oe.CurrentValue()
	if !val.Type().IsObjectType() {
		t.Fatalf("CurrentValue not object: %v", val.GoString())
	}
	m := val.AsValueMap()
	// Every declared field should be present.
	for _, want := range []string{"app_name", "constraints", "units", "trust", "storage_directives", "config"} {
		if _, ok := m[want]; !ok {
			t.Errorf("field %q missing from CurrentValue", want)
		}
	}
}

func TestObjectEditor_view_marksFocusedRow(t *testing.T) {
	oe := objectEditorOf(t, alertmanagerLikeVar(t))
	out := oe.View()
	plain := stripANSI(out)
	// Cursor chevron should appear before the first row's field name.
	if !strings.Contains(plain, "▸ app_name") {
		t.Errorf("focused chevron missing for app_name; got:\n%s", plain)
	}
	if !strings.Contains(plain, "constraints") {
		t.Errorf("unfocused field constraints missing from view")
	}

	oe = drive(t, oe, "down")
	plain = stripANSI(oe.View())
	if !strings.Contains(plain, "▸ constraints") {
		t.Errorf("after down, chevron didn't move; got:\n%s", plain)
	}
	if strings.Contains(plain, "▸ app_name") {
		t.Errorf("chevron still on app_name after moving down; got:\n%s", plain)
	}
}

func TestObjectEditor_view_collectionFieldsRenderCompact(t *testing.T) {
	oe := objectEditorOf(t, alertmanagerLikeVar(t))
	plain := stripANSI(oe.View())
	// storage_directives (map) and config (object) should appear with
	// compact placeholders, not multi-line nested editor output.
	if !strings.Contains(plain, "(map:") {
		t.Errorf("map field should render compact; got:\n%s", plain)
	}
	if !strings.Contains(plain, "(object:") {
		t.Errorf("nested object should render compact; got:\n%s", plain)
	}
}

func TestObjectEditor_endToEnd_throughTopLevelModel(t *testing.T) {
	// Confirm the field-level cursor and edits land in state.Values via
	// the top-level model — i.e. the auto-save plumbing still works.
	state := sampleState(t)
	state.Vars = append(state.Vars, alertmanagerLikeVar(t))
	m := New(state, "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	// Move down past the 3 scalar vars, into alertmanager.
	m = feed(m, key("down"), key("down"), key("down"))
	if v := m.SelectedVariable(); v == nil || v.Name != "alertmanager" {
		t.Fatalf("setup: SelectedVariable = %v", v)
	}

	// Focus the editor pane.
	m = feed(m, key("tab"))
	if m.focus != focusRight {
		t.Fatalf("focus = %v", m.focus)
	}

	// Move to units (index 2 inside the object). Pre-populated with the
	// declared default of 1; backspace it out and type "3".
	m = feed(m, key("down"), key("down"), key("backspace"), key("3"))

	val, ok := m.State.Values["alertmanager"]
	if !ok {
		t.Fatal("alertmanager value not stored in state")
	}
	units := val.AsValueMap()["units"]
	if !units.Equals(cty.NumberFloatVal(3)).True() {
		t.Errorf("units after edit = %v; want 3", units.GoString())
	}
}
