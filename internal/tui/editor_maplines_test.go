package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
)

func driveLines(t *testing.T, le *lineMapEditor, keys ...string) *lineMapEditor {
	t.Helper()
	var ed Editor = le
	for _, k := range keys {
		ed, _ = ed.Update(key(k))
	}
	next, ok := ed.(*lineMapEditor)
	if !ok {
		t.Fatalf("editor became %T", ed)
	}
	return next
}

// An empty map still offers a line to type into, so the first edit needs no
// navigation to reach an add affordance.
func TestLineMapEditor_emptyStartsWithOneEditableLine(t *testing.T) {
	le := newLineMapEditor(&tfvars.Variable{
		Name: "config", Type: mustParseType(t, "map(string)"),
	}, cty.NilVal)
	if len(le.lines) != 1 {
		t.Fatalf("lines = %d, want 1 editable line", len(le.lines))
	}
	le = driveLines(t, le, "r", "e", "t", "e", "n", "t", "i", "o", "n", "_", "t", "i", "m", "e")
	if _, present := le.CurrentValue().AsValueMap()["retention_time"]; present {
		t.Errorf("a key with no '=' is not an entry; got %s", le.CurrentValue().GoString())
	}
	le = driveLines(t, le, "=", "1", "5", "d")
	if got := le.CurrentValue().AsValueMap()["retention_time"].AsString(); got != "15d" {
		t.Errorf("retention_time = %q, want 15d", got)
	}
}

// The whole `key = value` is one editable cell, which is the point of the
// line form: there is no column to advance past before reaching the value.
func TestLineMapEditor_valueEditsInPlace(t *testing.T) {
	le := newLineMapEditor(&tfvars.Variable{
		Name: "config", Type: mustParseType(t, "map(string)"),
	}, cty.MapVal(map[string]cty.Value{"replicas": cty.StringVal("1")}))

	if got := le.lines[0].Value(); got != "replicas = 1" {
		t.Fatalf("line = %q, want %q", got, "replicas = 1")
	}
	// Changing a value needs no navigation at all: the caret starts at the end of
	// the line, so backspace over the old value and type the new one. In the
	// two-column grid the value cell is a separate target to reach first.
	le = driveLines(t, le, "backspace", "5")
	if got := le.CurrentValue().AsValueMap()["replicas"].AsString(); got != "5" {
		t.Errorf("replicas = %q, want 5", got)
	}

	// Rekeying uses the documented readline pair: ctrl+a to the start, ctrl+k
	// to kill to the end, then type the whole entry.
	le = driveLines(t, le, "ctrl+a", "ctrl+k", "b", "a", "c", "k", "e", "n", "d", "=", "2")
	m := le.CurrentValue().AsValueMap()
	if got := m["backend"].AsString(); got != "2" {
		t.Errorf("backend = %q, want 2", got)
	}
	if _, stale := m["replicas"]; stale {
		t.Errorf("rekeying left the old key behind: %s", le.CurrentValue().GoString())
	}
}

// Enter appends; an untouched appended line is abandoned when the user moves
// away, so a stray Enter does not leave a blank row in main.tf.
func TestLineMapEditor_enterAppendsAndFreshLineAbandons(t *testing.T) {
	le := newLineMapEditor(&tfvars.Variable{
		Name: "config", Type: mustParseType(t, "map(string)"),
	}, cty.MapVal(map[string]cty.Value{"a": cty.StringVal("1")}))

	le = driveLines(t, le, "enter")
	if len(le.lines) != 2 {
		t.Fatalf("lines = %d, want 2 after Enter", len(le.lines))
	}
	le = driveLines(t, le, "up") // away from the fresh, empty line
	if len(le.lines) != 1 {
		t.Errorf("lines = %d; an untouched added line should be abandoned", len(le.lines))
	}

	// A line that was typed into is kept.
	le = driveLines(t, le, "enter", "b")
	le = driveLines(t, le, "up")
	if len(le.lines) != 2 {
		t.Errorf("lines = %d; a line the user typed into must survive", len(le.lines))
	}
}

func TestLineMapEditor_deleteConfirmsThenRemoves(t *testing.T) {
	le := newLineMapEditor(&tfvars.Variable{
		Name: "config", Type: mustParseType(t, "map(string)"),
	}, cty.MapVal(map[string]cty.Value{
		"a": cty.StringVal("1"), "b": cty.StringVal("2"),
	}))

	le = driveLines(t, le, "alt+delete")
	if len(le.lines) != 2 {
		t.Fatalf("one Alt+Delete must not remove a populated line; lines = %d", len(le.lines))
	}
	le = driveLines(t, le, "alt+delete")
	if len(le.lines) != 1 {
		t.Errorf("lines = %d after confirming delete, want 1", len(le.lines))
	}
	if _, stillThere := le.CurrentValue().AsValueMap()["a"]; stillThere {
		t.Errorf("a should be gone: %s", le.CurrentValue().GoString())
	}
}

// A blank line is not worth a confirmation prompt; deleting it just removes it.
func TestLineMapEditor_deleteBlankNeedsNoConfirmation(t *testing.T) {
	le := newLineMapEditor(&tfvars.Variable{
		Name: "config", Type: mustParseType(t, "map(string)"),
	}, cty.MapVal(map[string]cty.Value{"a": cty.StringVal("1")}))
	le = driveLines(t, le, "enter", "alt+delete")
	if len(le.lines) != 1 {
		t.Errorf("lines = %d, want the blank line gone", len(le.lines))
	}
}

// Unfinished lines are the user's in-progress work, not errors: they are
// dropped from the value rather than written as a partial entry.
func TestLineMapEditor_unfinishedLinesAreDropped(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  map[string]string
	}{
		{"no equals sign", []string{"retention_time"}, nil},
		{"empty key", []string{" = 15d"}, nil},
		{"only spaces before equals", []string{"   = 15d"}, nil},
		{"one good line one unfinished", []string{"a = 1", "b"}, map[string]string{"a": "1"}},
		{"value keeps inner spaces and equals", []string{"cmd = a=b c"}, map[string]string{"cmd": "a=b c"}},
		{"surrounding whitespace trimmed", []string{"  a  =  1  "}, map[string]string{"a": "1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			le := newLineMapEditor(&tfvars.Variable{
				Name: "config", Type: mustParseType(t, "map(string)"),
			}, cty.NilVal)
			le.lines = nil
			for _, l := range tc.lines {
				le.lines = append(le.lines, newCellInput(l, false, ""))
			}
			got := le.CurrentValue()
			m := got.AsValueMap()
			if len(m) != len(tc.want) {
				t.Fatalf("entries = %d (%s), want %d", len(m), got.GoString(), len(tc.want))
			}
			for k, v := range tc.want {
				if m[k].AsString() != v {
					t.Errorf("%s = %q, want %q", k, m[k].AsString(), v)
				}
			}
		})
	}
}

// The widget renders the HCL it edits, so the parent row and the drilled-in
// view name the same keys.
func TestLineMapEditor_viewShowsKeyEqualsValue(t *testing.T) {
	le := newLineMapEditor(&tfvars.Variable{
		Name: "config", Type: mustParseType(t, "map(string)"),
	}, cty.MapVal(map[string]cty.Value{"retention_time": cty.StringVal("15d")}))
	plain := stripANSI(le.View())
	if !strings.Contains(plain, "retention_time = 15d") {
		t.Errorf("view should show `key = value`; got:\n%s", plain)
	}
}

// The hint must fit the right pane. renderRightPane word-wraps and then
// truncates, so a wrapped hint gains a physical row and shoves the pane's
// bottom border down. The ↑↓ glyphs are double-width, so measure cells.
func TestLineMapEditor_hintFitsOneLine(t *testing.T) {
	le := newLineMapEditor(&tfvars.Variable{
		Name: "config", Type: mustParseType(t, "map(string)"),
	}, cty.NilVal)
	for _, line := range strings.Split(stripANSI(le.View()), "\n") {
		if strings.Contains(line, "[Enter] add") {
			if w := lipgloss.Width(strings.TrimRight(line, " ")); w > 58 {
				t.Errorf("hint line is %d cells wide and would wrap: %q", w, line)
			}
		}
	}
}

// A nested map(string) gets the line form; a top-level map(string) variable
// keeps the two-column grid, which owns the whole right pane.
func TestNewFieldEditor_picksLineFormForNestedMapString(t *testing.T) {
	tp := mustParseType(t, `object({
    config    = optional(map(string), {})
    obj_map   = optional(map(object({ a = optional(string, "") })), {})
    scalars   = optional(map(number), {})
    flag      = optional(bool, true)
  })`)
	attr := tp.Attributes
	tests := []struct {
		field string
		want  string
	}{
		{"config", "*tui.lineMapEditor"},
		{"obj_map", "*tui.mapObjectEditor"},
		{"scalars", "*tui.lineMapEditor"},
		{"flag", "*tui.boolEditor"},
	}
	for _, tc := range tests {
		t.Run(tc.field, func(t *testing.T) {
			got := newFieldEditor(tc.field, attr[tc.field].Type,
				attr[tc.field].HasDefault, attr[tc.field].Default, cty.NilVal)
			if name := typeName(got); name != tc.want {
				t.Errorf("%s editor = %s, want %s", tc.field, name, tc.want)
			}
		})
	}
}

func typeName(e Editor) string {
	switch e.(type) {
	case *lineMapEditor:
		return "*tui.lineMapEditor"
	case *mapEditor:
		return "*tui.mapEditor"
	case *mapObjectEditor:
		return "*tui.mapObjectEditor"
	case *boolEditor:
		return "*tui.boolEditor"
	case *stringEditor:
		return "*tui.stringEditor"
	case *numberEditor:
		return "*tui.numberEditor"
	case *objectEditor:
		return "*tui.objectEditor"
	case *readOnlyEditor:
		return "*tui.readOnlyEditor"
	}
	return "unknown"
}

// Editing a nested map reaches wrapper.State, so the auto-save path and the
// sparse-write rule still see it.
func TestLineMapEditor_editsReachTopLevelModel(t *testing.T) {
	state := sampleState(t)
	state.Vars = append(state.Vars, alertmanagerLikeVar(t))
	m := New(state, "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = feed(m, key("down"), key("down"), key("down")) // to alertmanager
	m = feed(m, key("tab"))                            // into the editor

	// Walk to storage_directives and drill in.
	for i, f := range m.editor.(*objectEditor).fields {
		if f.Name == "storage_directives" {
			for j := 0; j < i; j++ {
				m = feed(m, key("down"))
			}
		}
	}
	m = feed(m, key("enter"))
	for _, k := range []string{"l", "o", "k", "i", "=", "s", "s", "d"} {
		m = feed(m, key(k))
	}
	got := m.State.Values["alertmanager"].AsValueMap()["storage_directives"].AsValueMap()
	if got["loki"].AsString() != "ssd" {
		t.Errorf("storage_directives.loki = %v; want ssd", got["loki"].GoString())
	}
}
