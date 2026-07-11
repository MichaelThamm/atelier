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

	// Enter advances key → value (the key is non-empty).
	var ed Editor = me
	ed, _ = ed.Update(key("enter"))
	me = ed.(*mapEditor)
	if me.colCursor != 1 {
		t.Errorf("after enter, colCursor = %d; want 1 (value)", me.colCursor)
	}

	// Enter on a non-last row's value advances to the next row's key.
	ed, _ = ed.Update(key("enter"))
	me = ed.(*mapEditor)
	if me.rowCursor != 1 || me.colCursor != 0 {
		t.Errorf("after enter on value, row=%d col=%d; want 1,0", me.rowCursor, me.colCursor)
	}

	// Down arrow moves between rows and resets to the key column.
	ed, _ = ed.Update(key("down"))
	me = ed.(*mapEditor)
	if !me.onAddRow() {
		t.Errorf("expected add-row after down from last row")
	}

	// Up returns to the last row's key.
	ed, _ = ed.Update(key("up"))
	me = ed.(*mapEditor)
	if me.rowCursor != 1 || me.colCursor != 0 {
		t.Errorf("after up from add-row, row=%d col=%d; want 1,0", me.rowCursor, me.colCursor)
	}
}

func TestMapEditor_enterOnLastValueAddsRow(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{"a": cty.StringVal("1")})
	me := newMapEditor(v, current)

	var ed Editor = me
	// key → value.
	ed, _ = ed.Update(key("enter"))
	// value on the last row → commit + new row focused on its key.
	ed, _ = ed.Update(key("enter"))
	me = ed.(*mapEditor)
	if len(me.rows) != 2 {
		t.Fatalf("expected 2 rows after enter on last value, got %d", len(me.rows))
	}
	if me.rowCursor != 1 || me.colCursor != 0 {
		t.Errorf("cursor should be on new row key: row=%d col=%d", me.rowCursor, me.colCursor)
	}
}

func TestMapEditor_enterOnEmptyKeyBlocks(t *testing.T) {
	v := mapStringVar(t)
	v.Default = cty.MapValEmpty(cty.String)
	me := newMapEditor(v, cty.MapValEmpty(cty.String))

	var ed Editor = me
	// Add a row (empty key), then Enter must not advance to the value.
	ed, _ = ed.Update(key("enter"))
	ed, _ = ed.Update(key("enter"))
	me = ed.(*mapEditor)
	if me.colCursor != 0 {
		t.Errorf("enter on empty key should stay on key; colCursor=%d", me.colCursor)
	}
	if me.nudge == "" {
		t.Errorf("expected a 'key required' nudge")
	}
}

func TestMapEditor_abandonEmptyRowOnMove(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{"a": cty.StringVal("1")})
	me := newMapEditor(v, current)

	var ed Editor = me
	// Go to add-row and add a fresh empty row, then move away without typing.
	ed, _ = ed.Update(key("down")) // to add-row
	ed, _ = ed.Update(key("enter"))
	me = ed.(*mapEditor)
	if len(me.rows) != 2 {
		t.Fatalf("expected 2 rows after add, got %d", len(me.rows))
	}
	// Up should abandon the fresh empty row.
	ed, _ = ed.Update(key("up"))
	me = ed.(*mapEditor)
	if len(me.rows) != 1 {
		t.Errorf("fresh empty row should be abandoned on move; got %d rows", len(me.rows))
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
	// Enter advances key → value (key "k" is non-empty).
	ed, _ = ed.Update(key("enter"))
	ed, _ = ed.Update(key("w"))
	ed, _ = ed.Update(key("o"))
	ed, _ = ed.Update(key("w"))
	me = ed.(*mapEditor)
	if me.rows[0].Val.Value() != "wow" {
		t.Errorf("val = %q; want 'wow'", me.rows[0].Val.Value())
	}
}

// TestMapEditor_rightArrowAdvancesToValue verifies that Right at the end of a
// non-empty key cell promotes focus to the value cell (a caret-level mirror
// of Enter's key→value advance), and Left at the start of the value returns.
func TestMapEditor_rightArrowAdvancesToValue(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{"abc": cty.StringVal("xyz")})
	me := newMapEditor(v, current)

	var ed Editor = me
	// Caret starts at end of the key (newCellInput parks it there). Right
	// off the end advances to the value cell.
	ed, _ = ed.Update(key("right"))
	me = ed.(*mapEditor)
	if me.colCursor != 1 {
		t.Fatalf("right at end of key should focus value; colCursor=%d", me.colCursor)
	}

	// Left at the start of the value returns to the key cell. First move the
	// caret to the start of the value with Home.
	ed, _ = ed.Update(key("home"))
	ed, _ = ed.Update(key("left"))
	me = ed.(*mapEditor)
	if me.colCursor != 0 {
		t.Errorf("left at start of value should return to key; colCursor=%d", me.colCursor)
	}
}

// TestMapEditor_rightArrowMovesCaretMidKey verifies Right does NOT jump cells
// when the caret is in the middle of the key — it just moves the caret.
func TestMapEditor_rightArrowMovesCaretMidKey(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{"abc": cty.StringVal("xyz")})
	me := newMapEditor(v, current)

	var ed Editor = me
	ed, _ = ed.Update(key("home")) // caret to start of key
	ed, _ = ed.Update(key("right"))
	me = ed.(*mapEditor)
	if me.colCursor != 0 {
		t.Errorf("right mid-key should stay on key cell; colCursor=%d", me.colCursor)
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
	// Loaded rows are populated, so the first Alt+Delete arms confirmation.
	ed, _ = ed.Update(altDelete())
	me = ed.(*mapEditor)
	if len(me.rows) != 2 {
		t.Fatalf("populated row should require confirmation; got %d rows", len(me.rows))
	}
	if me.confirmDelete != 0 {
		t.Errorf("expected confirmDelete=0, got %d", me.confirmDelete)
	}
	// Second Alt+Delete confirms.
	ed, _ = ed.Update(altDelete())
	me = ed.(*mapEditor)
	if len(me.rows) != 1 {
		t.Fatalf("expected 1 row after confirmed delete, got %d", len(me.rows))
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

func TestMapObjectEditor_addIsKeyFirst(t *testing.T) {
	v := mapObjectVar(t)
	ed := newEditor(v, cty.NilVal)

	// Enter on add-row appends a row and focuses its KEY — it does NOT drill
	// in until the key is named (ADR-0023 §1–2).
	ed, _ = ed.Update(key("enter"))
	moe := ed.(*mapObjectEditor)
	if len(moe.rows) != 1 {
		t.Fatalf("expected 1 row after enter, got %d", len(moe.rows))
	}
	if moe.drilledIn != nil {
		t.Fatal("add-row Enter must not drill in before the key is named")
	}
}

func TestMapObjectEditor_enterOnEmptyKeyBlocks(t *testing.T) {
	v := mapObjectVar(t)
	ed := newEditor(v, cty.NilVal)

	ed, _ = ed.Update(key("enter")) // add row, focus key
	ed, _ = ed.Update(key("enter")) // Enter on empty key must be blocked
	moe := ed.(*mapObjectEditor)
	if moe.drilledIn != nil {
		t.Error("enter on empty key should not drill in")
	}
	if moe.nudge == "" {
		t.Error("expected a 'key required' nudge")
	}
}

func TestMapObjectEditor_nameThenDrillIn(t *testing.T) {
	v := mapObjectVar(t)
	ed := newEditor(v, cty.NilVal)

	ed, _ = ed.Update(key("enter")) // add row, focus key
	ed, _ = ed.Update(key("m"))
	ed, _ = ed.Update(key("y"))
	ed, _ = ed.Update(key("k"))
	moe := ed.(*mapObjectEditor)
	if moe.rows[0].Key.Value() != "myk" {
		t.Errorf("key = %q; want 'myk'", moe.rows[0].Key.Value())
	}
	// Now Enter drills into the object.
	ed, _ = ed.Update(key("enter"))
	moe = ed.(*mapObjectEditor)
	if moe.drilledIn == nil {
		t.Error("enter on a named key should drill into the object")
	}
	// Esc returns to the key list (one level).
	ed, _ = ed.Update(key("esc"))
	moe = ed.(*mapObjectEditor)
	if moe.drilledIn != nil {
		t.Error("esc should exit drill-in")
	}
	if !moe.AtTopLevel() {
		t.Error("editor should report top level after esc")
	}
}

func TestMapObjectEditor_deleteRow(t *testing.T) {
	v := mapObjectVar(t)
	// Two pre-existing, named rows.
	current := cty.ObjectVal(map[string]cty.Value{
		"a": cty.ObjectVal(map[string]cty.Value{"app_name": cty.StringVal("x"), "units": cty.NumberIntVal(1)}),
		"b": cty.ObjectVal(map[string]cty.Value{"app_name": cty.StringVal("y"), "units": cty.NumberIntVal(2)}),
	})
	moe := newMapObjectEditor(v, current)
	if len(moe.rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(moe.rows))
	}

	var ed Editor = moe
	// First Alt+Delete on a named row arms confirmation.
	ed, _ = ed.Update(altDelete())
	moe = ed.(*mapObjectEditor)
	if len(moe.rows) != 2 {
		t.Fatalf("named row should require confirmation; got %d rows", len(moe.rows))
	}
	// Second confirms.
	ed, _ = ed.Update(altDelete())
	moe = ed.(*mapObjectEditor)
	if len(moe.rows) != 1 {
		t.Errorf("expected 1 row after confirmed delete, got %d", len(moe.rows))
	}
}

func TestMapObjectEditor_abandonEmptyRowOnMove(t *testing.T) {
	v := mapObjectVar(t)
	// One pre-existing named row so there is somewhere to move.
	current := cty.ObjectVal(map[string]cty.Value{
		"a": cty.ObjectVal(map[string]cty.Value{"app_name": cty.StringVal("x"), "units": cty.NumberIntVal(1)}),
	})
	moe := newMapObjectEditor(v, current)

	var ed Editor = moe
	ed, _ = ed.Update(key("down"))  // to add-row
	ed, _ = ed.Update(key("enter")) // add a fresh, keyless row
	moe = ed.(*mapObjectEditor)
	if len(moe.rows) != 2 {
		t.Fatalf("expected 2 rows after add, got %d", len(moe.rows))
	}
	// Moving up off the fresh keyless row abandons it.
	ed, _ = ed.Update(key("up"))
	moe = ed.(*mapObjectEditor)
	if len(moe.rows) != 1 {
		t.Errorf("fresh keyless row should be abandoned on move; got %d rows", len(moe.rows))
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
	oe = drive(t, oe, "enter")                 // add a row (focus on key)
	oe = drive(t, oe, "d", "i", "r")           // type key "dir"
	oe = drive(t, oe, "enter")                 // Enter advances key → value
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

// TestMapEditor_TabIsPaneSwitch confirms Tab is no longer a cell-cycle: the
// editor does not consume it (ADR-0023 §2), leaving it to the model as the
// app-wide pane-switch key. It must not move the row/col cursor, and the
// editor always reports AtTopLevel so the model owns Tab/Esc.
func TestMapEditor_TabIsPaneSwitch(t *testing.T) {
	v := mapStringVar(t)
	current := cty.MapVal(map[string]cty.Value{
		"a": cty.StringVal("1"),
		"b": cty.StringVal("2"),
	})
	me := newMapEditor(v, current)

	var ed Editor = me
	ed, _ = ed.Update(key("tab"))
	me = ed.(*mapEditor)
	if me.rowCursor != 0 || me.colCursor != 0 {
		t.Errorf("tab must not move the cursor; got row=%d col=%d", me.rowCursor, me.colCursor)
	}
	if !me.AtTopLevel() {
		t.Errorf("mapEditor should always report AtTopLevel")
	}
}

// TestMapEditor_AltDeleteRemovesRow confirms Alt+Delete removes a row, with a
// second-press confirmation for a populated row (ADR-0023 §5).
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

	// Cursor starts on row 0 ("keep" — sorted first). The row is populated,
	// so the first Alt+Delete arms confirmation and the second removes it.
	var ed Editor = me
	ed, _ = ed.Update(altDelete())
	ed, _ = ed.Update(altDelete())
	me = ed.(*mapEditor)
	if len(me.rows) != 1 {
		t.Fatalf("expected 1 row after confirmed alt+delete, got %d", len(me.rows))
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
