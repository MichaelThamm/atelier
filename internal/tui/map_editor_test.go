package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

func mapStringVar(t *testing.T) *tfvars.Variable {
	t.Helper()
	tp, err := tftypes.ParseTypeExpr("map(string)")
	if err != nil {
		t.Fatal(err)
	}
	return &tfvars.Variable{
		Name:       "labels",
		Type:       tp,
		HasDefault: true,
		Default:    cty.MapVal(map[string]cty.Value{"env": cty.StringVal("prod")}),
	}
}

func mapObjectVar(t *testing.T) *tfvars.Variable {
	t.Helper()
	tp, err := tftypes.ParseTypeExpr(`map(object({
		app_name = optional(string, "default")
		units    = optional(number, 1)
	}))`)
	if err != nil {
		t.Fatal(err)
	}
	return &tfvars.Variable{
		Name:       "apps",
		Type:       tp,
		HasDefault: true,
		Default:    cty.EmptyObjectVal,
	}
}

// altDelete returns Alt+Delete, used to remove the focused row in map
// editors (see ADR-0020 §2).
func altDelete() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyDelete, Alt: true}
}

// --- mapEditor (map(string)) tests ---

func TestMapEditor_initFromDefault(t *testing.T) {
	v := mapStringVar(t)
	me := newMapEditor(v, cty.NilVal)
	if len(me.rows) != 1 {
		t.Fatalf("expected 1 row from default, got %d", len(me.rows))
	}
	if me.rows[0].Key.Value() != "env" || me.rows[0].Val.Value() != "prod" {
		t.Errorf("row[0] = %q=%q; want env=prod", me.rows[0].Key.Value(), me.rows[0].Val.Value())
	}
}

func TestMapEditor_initFromCurrentValue(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{
		"team": cty.StringVal("obs"),
		"app":  cty.StringVal("atelier"),
	})
	me := newMapEditor(v, current)
	if len(me.rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(me.rows))
	}
	// Sorted by key.
	if me.rows[0].Key.Value() != "app" || me.rows[1].Key.Value() != "team" {
		t.Errorf("rows not sorted: %q, %q", me.rows[0].Key.Value(), me.rows[1].Key.Value())
	}
}

func TestMapEditor_initEmpty_cursorOnAddRow(t *testing.T) {
	v := mapStringVar(t)
	v.Default = cty.MapValEmpty(cty.String)
	me := newMapEditor(v, cty.MapValEmpty(cty.String))
	if !me.onAddRow() {
		t.Errorf("cursor should be on add-row when map is empty")
	}
}

func TestMapEditor_navigation(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{
		"a": cty.StringVal("1"),
		"b": cty.StringVal("2"),
	})
	me := newMapEditor(v, current)

	// Start at row 0, col 0 (key).
	if me.rowCursor != 0 || me.colCursor != 0 {
		t.Fatalf("initial cursor: row=%d col=%d", me.rowCursor, me.colCursor)
	}

	// Tab cycles to value column.
	var ed Editor = me
	ed, _ = ed.Update(key("tab"))
	me = ed.(*mapEditor)
	if me.colCursor != 1 {
		t.Errorf("after tab, colCursor = %d", me.colCursor)
	}

	// Tab again cycles to next row's key.
	ed, _ = ed.Update(key("tab"))
	me = ed.(*mapEditor)
	if me.rowCursor != 1 || me.colCursor != 0 {
		t.Errorf("after second tab, row=%d col=%d; want 1,0", me.rowCursor, me.colCursor)
	}

	// Shift+Tab cycles back.
	ed, _ = ed.Update(key("shift+tab"))
	me = ed.(*mapEditor)
	if me.rowCursor != 0 || me.colCursor != 1 {
		t.Errorf("after shift+tab, row=%d col=%d; want 0,1", me.rowCursor, me.colCursor)
	}

	// Down arrow still moves between rows.
	ed, _ = ed.Update(key("down"))
	me = ed.(*mapEditor)
	if me.rowCursor != 1 {
		t.Errorf("after down, rowCursor = %d", me.rowCursor)
	}

	// Down again to add-row.
	ed, _ = ed.Update(key("down"))
	me = ed.(*mapEditor)
	if !me.onAddRow() {
		t.Errorf("expected add-row after 2 downs from row 0")
	}

	// Tab on add-row wraps back to the first cell (key of row 0).
	ed, _ = ed.Update(key("tab"))
	me = ed.(*mapEditor)
	if me.rowCursor != 0 || me.colCursor != 0 {
		t.Errorf("tab from add-row should wrap to row 0 key; got row=%d col=%d", me.rowCursor, me.colCursor)
	}
}

func TestMapEditor_addRow(t *testing.T) {
	v := mapStringVar(t)
	v.Default = cty.MapValEmpty(cty.String)
	me := newMapEditor(v, cty.MapValEmpty(cty.String))

	// On add-row, Enter adds a new row.
	var ed Editor = me
	ed, _ = ed.Update(key("enter"))
	me = ed.(*mapEditor)
	if len(me.rows) != 1 {
		t.Fatalf("expected 1 row after enter, got %d", len(me.rows))
	}
	if me.rowCursor != 0 || me.colCursor != 0 {
		t.Errorf("after add, cursor should be on new row key: row=%d col=%d", me.rowCursor, me.colCursor)
	}
}

func TestMapEditor_typeIntoKey(t *testing.T) {
	v := mapStringVar(t)
	v.Default = cty.MapValEmpty(cty.String)
	me := newMapEditor(v, cty.MapValEmpty(cty.String))

	var ed Editor = me
	// Add a row then type into the key.
	ed, _ = ed.Update(key("enter"))
	ed, _ = ed.Update(key("h"))
	ed, _ = ed.Update(key("i"))
	me = ed.(*mapEditor)
	if me.rows[0].Key.Value() != "hi" {
		t.Errorf("key = %q; want 'hi'", me.rows[0].Key.Value())
	}
}

func TestMapEditor_typeIntoValue(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{"k": cty.StringVal("")})
	me := newMapEditor(v, current)

	var ed Editor = me
	// Tab to value column.
	ed, _ = ed.Update(key("tab"))
	ed, _ = ed.Update(key("w"))
	ed, _ = ed.Update(key("o"))
	ed, _ = ed.Update(key("w"))
	me = ed.(*mapEditor)
	if me.rows[0].Val.Value() != "wow" {
		t.Errorf("val = %q; want 'wow'", me.rows[0].Val.Value())
	}
}

func TestMapEditor_backspace(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{"abc": cty.StringVal("xyz")})
	me := newMapEditor(v, current)

	var ed Editor = me
	ed, _ = ed.Update(key("backspace"))
	me = ed.(*mapEditor)
	if me.rows[0].Key.Value() != "ab" {
		t.Errorf("key after backspace = %q", me.rows[0].Key.Value())
	}
}

func TestMapEditor_ctrlU_clearsCell(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{"abc": cty.StringVal("xyz")})
	me := newMapEditor(v, current)

	var ed Editor = me
	ed, _ = ed.Update(key("ctrl+u"))
	me = ed.(*mapEditor)
	if me.rows[0].Key.Value() != "" {
		t.Errorf("key after ctrl+u = %q; want empty", me.rows[0].Key.Value())
	}
}

func TestMapEditor_deleteRow(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{
		"a": cty.StringVal("1"),
		"b": cty.StringVal("2"),
	})
	me := newMapEditor(v, current)

	var ed Editor = me
	ed, _ = ed.Update(altDelete())
	me = ed.(*mapEditor)
	if len(me.rows) != 1 {
		t.Fatalf("expected 1 row after delete, got %d", len(me.rows))
	}
	if me.rows[0].Key.Value() != "b" {
		t.Errorf("remaining row key = %q; want 'b'", me.rows[0].Key.Value())
	}
}

func TestMapEditor_currentValue(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{
		"hello": cty.StringVal("world"),
	})
	me := newMapEditor(v, current)
	val := me.CurrentValue()
	m := val.AsValueMap()
	if m["hello"].AsString() != "world" {
		t.Errorf("CurrentValue wrong: %v", val.GoString())
	}
}

func TestMapEditor_currentValue_skipsEmptyKeys(t *testing.T) {
	v := mapStringVar(t)
	v.Default = cty.MapValEmpty(cty.String)
	me := newMapEditor(v, cty.MapValEmpty(cty.String))

	// Add a row but don't type a key — should be skipped.
	var ed Editor = me
	ed, _ = ed.Update(key("enter"))
	me = ed.(*mapEditor)
	val := me.CurrentValue()
	if val.LengthInt() != 0 {
		t.Errorf("empty-key row should be skipped; got %v", val.GoString())
	}
}

func TestMapEditor_view_showsAddRow(t *testing.T) {
	v := mapStringVar(t)
	v.Default = cty.MapValEmpty(cty.String)
	me := newMapEditor(v, cty.MapValEmpty(cty.String))
	out := stripANSI(me.View())
	if !strings.Contains(out, "+ Add row") {
		t.Errorf("view missing add-row; got:\n%s", out)
	}
}

func TestMapEditor_view_showsKeyValuePairs(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{"foo": cty.StringVal("bar")})
	me := newMapEditor(v, current)
	out := stripANSI(me.View())
	if !strings.Contains(out, "foo") || !strings.Contains(out, "bar") {
		t.Errorf("view missing key/value; got:\n%s", out)
	}
}

// --- mapObjectEditor (map(object(...))) tests ---

func TestMapObjectEditor_initEmpty(t *testing.T) {
	v := mapObjectVar(t)
	ed := newEditor(v, cty.NilVal)
	moe, ok := ed.(*mapObjectEditor)
	if !ok {
		t.Fatalf("expected *mapObjectEditor, got %T", ed)
	}
	if len(moe.rows) != 0 {
		t.Errorf("expected 0 rows for empty default, got %d", len(moe.rows))
	}
	if !moe.onAddRow() {
		t.Errorf("cursor should be on add-row when empty")
	}
}

func TestMapObjectEditor_addAndDrillIn(t *testing.T) {
	v := mapObjectVar(t)
	ed := newEditor(v, cty.NilVal)

	// Press Enter on add-row to create a new entry and drill in.
	ed, _ = ed.Update(key("enter"))
	moe := ed.(*mapObjectEditor)
	if len(moe.rows) != 1 {
		t.Fatalf("expected 1 row after enter, got %d", len(moe.rows))
	}
	if moe.drilledIn == nil {
		t.Fatal("expected drill-in after adding row")
	}
}

func TestMapObjectEditor_drillIn_esc_returns(t *testing.T) {
	v := mapObjectVar(t)
	ed := newEditor(v, cty.NilVal)

	// Add and drill in.
	ed, _ = ed.Update(key("enter"))
	moe := ed.(*mapObjectEditor)
	if moe.drilledIn == nil {
		t.Fatal("should be drilled in")
	}

	// Esc returns to key list.
	ed, _ = ed.Update(key("esc"))
	moe = ed.(*mapObjectEditor)
	if moe.drilledIn != nil {
		t.Errorf("esc should exit drill-in")
	}
}

func TestMapObjectEditor_typeKeyBeforeDrillIn(t *testing.T) {
	v := mapObjectVar(t)
	ed := newEditor(v, cty.NilVal)

	// Add row and immediately Esc out of drill-in.
	ed, _ = ed.Update(key("enter"))
	ed, _ = ed.Update(key("esc"))

	// Type the key name.
	ed, _ = ed.Update(key("m"))
	ed, _ = ed.Update(key("y"))
	ed, _ = ed.Update(key("k"))
	moe := ed.(*mapObjectEditor)
	if moe.rows[0].Key.Value() != "myk" {
		t.Errorf("key = %q; want 'myk'", moe.rows[0].Key.Value())
	}
}

func TestMapObjectEditor_deleteRow(t *testing.T) {
	v := mapObjectVar(t)
	ed := newEditor(v, cty.NilVal)

	// Add 2 rows.
	ed, _ = ed.Update(key("enter"))
	ed, _ = ed.Update(key("esc"))
	ed, _ = ed.Update(key("down"))
	ed, _ = ed.Update(key("enter"))
	ed, _ = ed.Update(key("esc"))

	moe := ed.(*mapObjectEditor)
	if len(moe.rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(moe.rows))
	}

	// Delete the first row.
	ed, _ = ed.Update(key("up"))
	ed, _ = ed.Update(key("up"))
	ed, _ = ed.Update(altDelete())
	moe = ed.(*mapObjectEditor)
	if len(moe.rows) != 1 {
		t.Errorf("expected 1 row after delete, got %d", len(moe.rows))
	}
}

func TestMapObjectEditor_view_showsEditHint(t *testing.T) {
	v := mapObjectVar(t)
	current := cty.ObjectVal(map[string]cty.Value{
		"myapp": cty.ObjectVal(map[string]cty.Value{
			"app_name": cty.StringVal("hello"),
			"units":    cty.NumberIntVal(3),
		}),
	})
	ed := newMapObjectEditor(v, current)
	out := stripANSI(ed.View())
	if !strings.Contains(out, "edit") {
		t.Errorf("view should show edit hint; got:\n%s", out)
	}
	if !strings.Contains(out, "myapp") {
		t.Errorf("view should show key; got:\n%s", out)
	}
}

// --- objectEditor drill-in tests ---

func TestObjectEditor_drillIntoMap_enterAndEsc(t *testing.T) {
	oe := objectEditorOf(t, alertmanagerLikeVar(t))
	// Navigate to storage_directives (map).
	for oe.fields[oe.cursor].Name != "storage_directives" {
		oe = drive(t, oe, "down")
	}
	// Press Enter to drill in.
	oe = drive(t, oe, "enter")
	if oe.drilledIn == nil {
		t.Fatal("expected drill-in after Enter on map field")
	}
	// Esc returns.
	oe = drive(t, oe, "esc")
	if oe.drilledIn != nil {
		t.Errorf("esc should exit drill-in")
	}
}

func TestObjectEditor_drillIntoMap_editsPropagate(t *testing.T) {
	oe := objectEditorOf(t, alertmanagerLikeVar(t))
	// Navigate to storage_directives (map).
	for oe.fields[oe.cursor].Name != "storage_directives" {
		oe = drive(t, oe, "down")
	}
	// Drill in, add a row, type key and value.
	oe = drive(t, oe, "enter")                 // drill in — map is empty, so cursor on add-row
	oe = drive(t, oe, "enter")                 // add a row
	oe = drive(t, oe, "d", "i", "r")           // type key "dir"
	oe = drive(t, oe, "tab")                   // Tab to value column
	oe = drive(t, oe, "/", "d", "a", "t", "a") // type value "/data"
	oe = drive(t, oe, "esc")                   // exit drill-in

	val := oe.CurrentValue().AsValueMap()["storage_directives"]
	if val.IsNull() || !val.Type().IsMapType() {
		t.Fatalf("storage_directives type: %v", val.GoString())
	}
	m := val.AsValueMap()
	if m["dir"].AsString() != "/data" {
		t.Errorf("expected dir=/data; got %v", val.GoString())
	}
}

func TestObjectEditor_drillIn_viewShowsBreadcrumb(t *testing.T) {
	oe := objectEditorOf(t, alertmanagerLikeVar(t))
	for oe.fields[oe.cursor].Name != "storage_directives" {
		oe = drive(t, oe, "down")
	}
	oe = drive(t, oe, "enter")
	out := stripANSI(oe.View())
	if !strings.Contains(out, "alertmanager") || !strings.Contains(out, "storage_directives") {
		t.Errorf("drill-in view should show breadcrumb; got:\n%s", out)
	}
	if !strings.Contains(out, "Esc") {
		t.Errorf("drill-in view should show Esc hint; got:\n%s", out)
	}
}

// --- readline integration tests (ADR-0020) ---

// TestMapEditor_TabCyclesCells walks Tab through key → value → next-row
// key for a 2-row map.
func TestMapEditor_TabCyclesCells(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{
		"a": cty.StringVal("1"),
		"b": cty.StringVal("2"),
	})
	me := newMapEditor(v, current)

	var ed Editor = me
	// row 0 / key → row 0 / value
	ed, _ = ed.Update(key("tab"))
	me = ed.(*mapEditor)
	if me.rowCursor != 0 || me.colCursor != 1 {
		t.Errorf("after 1st tab: row=%d col=%d; want 0,1", me.rowCursor, me.colCursor)
	}
	// row 0 / value → row 1 / key
	ed, _ = ed.Update(key("tab"))
	me = ed.(*mapEditor)
	if me.rowCursor != 1 || me.colCursor != 0 {
		t.Errorf("after 2nd tab: row=%d col=%d; want 1,0", me.rowCursor, me.colCursor)
	}
	// row 1 / key → row 1 / value
	ed, _ = ed.Update(key("tab"))
	me = ed.(*mapEditor)
	if me.rowCursor != 1 || me.colCursor != 1 {
		t.Errorf("after 3rd tab: row=%d col=%d; want 1,1", me.rowCursor, me.colCursor)
	}
}

// TestMapEditor_AltDeleteRemovesRow confirms Alt+Delete is the row-delete
// binding (the old Ctrl+D conflicted with textinput's delete-character-
// forward; see ADR-0020 §2).
func TestMapEditor_AltDeleteRemovesRow(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{
		"keep":   cty.StringVal("yes"),
		"remove": cty.StringVal("no"),
	})
	me := newMapEditor(v, current)
	if len(me.rows) != 2 {
		t.Fatalf("setup: expected 2 rows, got %d", len(me.rows))
	}

	// Cursor starts on row 0 ("keep" — sorted first); Alt+Delete removes
	// it, leaving "remove".
	var ed Editor = me
	ed, _ = ed.Update(altDelete())
	me = ed.(*mapEditor)
	if len(me.rows) != 1 {
		t.Fatalf("expected 1 row after alt+delete, got %d", len(me.rows))
	}
	if me.rows[0].Key.Value() != "remove" {
		t.Errorf("remaining row key = %q; want %q", me.rows[0].Key.Value(), "remove")
	}
}

// TestMapEditor_PlainDeleteForwardErasesChar confirms that plain Delete
// (without Alt) forwards to the focused cell's caret-forward-delete
// rather than removing the row.
func TestMapEditor_PlainDeleteForwardErasesChar(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{"abc": cty.StringVal("xyz")})
	me := newMapEditor(v, current)

	var ed Editor = me
	// Caret starts at end of "abc"; Home then Delete strips the first
	// char, leaving "bc".
	ed, _ = ed.Update(key("home"))
	ed, _ = ed.Update(key("delete"))
	me = ed.(*mapEditor)
	if me.rows[0].Key.Value() != "bc" {
		t.Errorf("after home+delete, key = %q; want %q", me.rows[0].Key.Value(), "bc")
	}
	if len(me.rows) != 1 {
		t.Errorf("delete should not remove the row; got %d rows", len(me.rows))
	}
}
