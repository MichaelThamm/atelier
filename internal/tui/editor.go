package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// Editor is the interface every type-specific right-pane editor satisfies.
type Editor interface {
	Update(msg tea.Msg) (Editor, tea.Cmd)
	View() string
}

// EditorWithValue is an editor that can report its current value back to
// the top-level model (so edits flow into state.Values).
type EditorWithValue interface {
	Editor
	CurrentValue() cty.Value
}

// EditorWithCursor is an editor that reports its logical cursor line
// (0-based) so the right-pane scroll can follow the cursor.
type EditorWithCursor interface {
	Editor
	CursorLine() int
}

// newEditor picks a widget for the variable's type.
func newEditor(v *tfvars.Variable, current cty.Value) Editor {
	if v == nil || v.Type == nil {
		return &readOnlyEditor{text: "<no variable selected>"}
	}
	switch v.Type.Kind {
	case tftypes.KindBool:
		return newBoolEditor(v, current)
	case tftypes.KindString:
		return newStringEditor(v, current)
	case tftypes.KindNumber:
		return newNumberEditor(v, current)
	case tftypes.KindList, tftypes.KindSet:
		return newListEditor(v, current)
	case tftypes.KindMap:
		if v.Type.Element != nil && v.Type.Element.Kind == tftypes.KindObject {
			return newMapObjectEditor(v, current)
		}
		return newMapEditor(v, current)
	case tftypes.KindObject:
		return newObjectEditor(v, current)
	case tftypes.KindAny, tftypes.KindTuple:
		return &readOnlyEditor{
			variable: v,
			text:     "Read-only widget (v1 deferred; use $EDITOR on main.tf for now).",
		}
	}
	return &readOnlyEditor{variable: v, text: "Unknown type."}
}

// readOnlyEditor is a fallback for types Atelier doesn't have a widget for.
type readOnlyEditor struct {
	variable *tfvars.Variable
	text     string
}

func (e *readOnlyEditor) Update(msg tea.Msg) (Editor, tea.Cmd) { return e, nil }
func (e *readOnlyEditor) View() string                         { return styleDescription.Render(e.text) }

// --- bool ---

type boolEditor struct {
	v       *tfvars.Variable
	value   cty.Value
	touched bool
}

func newBoolEditor(v *tfvars.Variable, current cty.Value) *boolEditor {
	if current == cty.NilVal {
		if v.HasDefault {
			current = v.Default
		} else {
			current = cty.False
		}
	}
	return &boolEditor{v: v, value: current}
}

func (e *boolEditor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case " ", "space", "enter":
			if e.value.IsNull() || !e.value.True() {
				e.value = cty.True
			} else {
				e.value = cty.False
			}
			e.touched = true
		case "t", "y":
			e.value = cty.True
			e.touched = true
		case "f", "n":
			e.value = cty.False
			e.touched = true
		}
	}
	return e, nil
}
func (e *boolEditor) View() string {
	state := "false"
	if !e.value.IsNull() && e.value.True() {
		state = "true"
	}
	return fmt.Sprintf("[%s]  %s", state, styleHelp.Render("(space to toggle)"))
}
func (e *boolEditor) CurrentValue() cty.Value { return e.value }
func (e *boolEditor) Touched() bool           { return e.touched }

// --- string ---

type stringEditor struct {
	v       *tfvars.Variable
	value   string
	null    bool // user explicitly cleared (null)
	touched bool
}

func newStringEditor(v *tfvars.Variable, current cty.Value) *stringEditor {
	se := &stringEditor{v: v}
	if current == cty.NilVal {
		if v.HasDefault && !v.Default.IsNull() && v.Default.Type() == cty.String {
			se.value = v.Default.AsString()
		}
		return se
	}
	if current.IsNull() {
		se.null = true
		return se
	}
	if current.Type() == cty.String {
		se.value = current.AsString()
	}
	return se
}

func (e *stringEditor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}
	switch k.Type {
	case tea.KeyBackspace:
		if len(e.value) > 0 {
			e.value = e.value[:len(e.value)-1]
		}
		e.null = false
		e.touched = true
	case tea.KeyRunes, tea.KeySpace:
		e.value += string(k.Runes)
		e.null = false
		e.touched = true
	case tea.KeyCtrlU:
		e.value = ""
		e.null = false
		e.touched = true
	}
	return e, nil
}
func (e *stringEditor) View() string {
	if e.v != nil && e.v.Sensitive {
		return fmt.Sprintf("[%s] %s",
			strings.Repeat("•", len(e.value)),
			styleSensitiveTag.Render("sensitive"))
	}
	return fmt.Sprintf("[%s]", e.value)
}
func (e *stringEditor) CurrentValue() cty.Value {
	if e.null {
		return cty.NullVal(cty.String)
	}
	return cty.StringVal(e.value)
}
func (e *stringEditor) Touched() bool { return e.touched }

// --- number ---

// numberEditor is a free-text widget: the user types a literal numeric
// string (digits, optional sign, decimal point, scientific notation) and
// CurrentValue parses it on demand. We deliberately don't bind `+`/`-` to
// increment/decrement — those characters need to be typeable as part of
// the number (leading sign, exponent sign).
type numberEditor struct {
	v       *tfvars.Variable
	text    string
	touched bool
}

// numberRunes is the set of characters accepted inside the input. Anything
// else is silently ignored so a stray letter keypress doesn't pollute the
// buffer.
const numberRunes = "0123456789.-+eE"

func newNumberEditor(v *tfvars.Variable, current cty.Value) *numberEditor {
	ne := &numberEditor{v: v}
	if current != cty.NilVal && !current.IsNull() && current.Type() == cty.Number {
		ne.text = current.AsBigFloat().Text('f', -1)
	} else if v.HasDefault && !v.Default.IsNull() {
		ne.text = v.Default.AsBigFloat().Text('f', -1)
	}
	return ne
}

func (e *numberEditor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}
	switch k.Type {
	case tea.KeyBackspace:
		if len(e.text) > 0 {
			e.text = e.text[:len(e.text)-1]
		}
		e.touched = true
		return e, nil
	case tea.KeyCtrlU:
		e.text = ""
		e.touched = true
		return e, nil
	case tea.KeyRunes:
		for _, r := range k.Runes {
			if strings.ContainsRune(numberRunes, r) {
				e.text += string(r)
				e.touched = true
			}
		}
	}
	return e, nil
}

func (e *numberEditor) View() string {
	// Highlight unparseable input in danger so the user sees that what's
	// in the buffer won't make it through to the wrapper.
	body := fmt.Sprintf("[%s]", e.text)
	if e.text != "" {
		if _, err := strconv.ParseFloat(e.text, 64); err != nil {
			return styleRequiredTag.Render(body) + " " + styleHelp.Render("(invalid number)")
		}
	}
	return body
}

func (e *numberEditor) CurrentValue() cty.Value {
	if e.text == "" {
		return cty.NilVal
	}
	if n, err := strconv.ParseFloat(e.text, 64); err == nil {
		return cty.NumberFloatVal(n)
	}
	return cty.NilVal
}
func (e *numberEditor) Touched() bool { return e.touched }

// --- map(string) ---

// mapEditor is the widget for `map(string)` variables. The user navigates a
// 2-column grid (key, value) plus an "Add row" affordance at the bottom.
//
//	[some-key]   = [some-value]
//	[other-key]  = [other-value]
//	+ Add row
//
// Key bindings inside the editor:
//
//	↑/↓      move between rows; the add-row slot is one past the last data row
//	←/→      switch between key and value cells within the current row
//	Enter    on add-row: append a new empty row and focus its key
//	Ctrl+D   delete the current row (no-op on add-row)
//	Ctrl+U   clear the current cell
//	Backspace remove the last char from the current cell
//	(any printable rune) append to the current cell
//
// Non-string element types fall back to a read-only message — the
// dispatching in newEditor handles that branch.
type mapEditor struct {
	v         *tfvars.Variable
	rows      []mapRow
	rowCursor int // 0..len(rows); len(rows) means the add-row slot
	colCursor int // 0 = key, 1 = value
}

type mapRow struct {
	Key string
	Val string
}

func newMapEditor(v *tfvars.Variable, current cty.Value) *mapEditor {
	me := &mapEditor{v: v}
	source := current
	if source == cty.NilVal || source.IsNull() {
		if v != nil && v.HasDefault && !v.Default.IsNull() {
			source = v.Default
		}
	}
	if source != cty.NilVal && !source.IsNull() &&
		(source.Type().IsMapType() || source.Type().IsObjectType()) &&
		source.LengthInt() > 0 {

		m := source.AsValueMap()
		// Sort by key for stable display — map iteration in Go is randomised
		// and we don't want rows shuffling between repaints.
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val := m[k]
			row := mapRow{Key: k}
			if !val.IsNull() && val.Type() == cty.String {
				row.Val = val.AsString()
			}
			me.rows = append(me.rows, row)
		}
	}
	// Start on the add-row when the map is empty, otherwise on the first
	// row's key.
	if len(me.rows) == 0 {
		me.rowCursor = 0 // == len(rows); add-row
	}
	return me
}

func (e *mapEditor) onAddRow() bool { return e.rowCursor == len(e.rows) }

func (e *mapEditor) currentCell() *string {
	if e.onAddRow() || e.rowCursor < 0 || e.rowCursor >= len(e.rows) {
		return nil
	}
	if e.colCursor == 0 {
		return &e.rows[e.rowCursor].Key
	}
	return &e.rows[e.rowCursor].Val
}

func (e *mapEditor) addRow() {
	e.rows = append(e.rows, mapRow{})
	e.rowCursor = len(e.rows) - 1
	e.colCursor = 0
}

func (e *mapEditor) deleteRow() {
	if e.onAddRow() || e.rowCursor < 0 || e.rowCursor >= len(e.rows) {
		return
	}
	e.rows = append(e.rows[:e.rowCursor], e.rows[e.rowCursor+1:]...)
	if e.rowCursor > len(e.rows) {
		e.rowCursor = len(e.rows)
	}
	e.colCursor = 0
}

func (e *mapEditor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}
	switch k.Type {
	case tea.KeyUp:
		if e.rowCursor > 0 {
			e.rowCursor--
		}
		return e, nil
	case tea.KeyDown:
		if e.rowCursor < len(e.rows) {
			e.rowCursor++
		}
		return e, nil
	case tea.KeyLeft:
		e.colCursor = 0
		return e, nil
	case tea.KeyRight:
		if !e.onAddRow() {
			e.colCursor = 1
		}
		return e, nil
	case tea.KeyEnter:
		if e.onAddRow() {
			e.addRow()
		}
		return e, nil
	case tea.KeyBackspace:
		if cell := e.currentCell(); cell != nil && len(*cell) > 0 {
			*cell = (*cell)[:len(*cell)-1]
		}
		return e, nil
	case tea.KeyCtrlU:
		if cell := e.currentCell(); cell != nil {
			*cell = ""
		}
		return e, nil
	case tea.KeyCtrlD:
		e.deleteRow()
		return e, nil
	case tea.KeySpace:
		if cell := e.currentCell(); cell != nil {
			*cell += " "
		}
		return e, nil
	case tea.KeyRunes:
		if cell := e.currentCell(); cell != nil {
			*cell += string(k.Runes)
		}
		return e, nil
	}
	return e, nil
}

func (e *mapEditor) View() string {
	var b strings.Builder
	for i, row := range e.rows {
		keyFocused := i == e.rowCursor && e.colCursor == 0
		valFocused := i == e.rowCursor && e.colCursor == 1
		key := renderMapCell(row.Key, keyFocused, "(key)")
		val := renderMapCell(row.Val, valFocused, "(value)")
		fmt.Fprintf(&b, "  %s = %s\n", key, val)
	}
	addLabel := "+ Add row"
	if e.onAddRow() {
		fmt.Fprintf(&b, "  %s\n", styleCursorActive.Render(addLabel))
	} else {
		fmt.Fprintf(&b, "  %s\n", styleHelp.Render(addLabel))
	}
	fmt.Fprintln(&b)
	fmt.Fprint(&b, styleHelp.Render(
		"[↑↓] row   [←→] cell   [Enter] add row   [Ctrl+D] delete row"))
	return b.String()
}

// renderMapCell renders one cell, with a bracket wrapper. Empty unfocused
// cells show a dim placeholder so the user knows what goes there.
func renderMapCell(value string, focused bool, placeholder string) string {
	if focused {
		return styleCursorActive.Render("[" + value + "]")
	}
	if value == "" {
		return "[" + styleHelp.Render(placeholder) + "]"
	}
	return "[" + value + "]"
}

func (e *mapEditor) CurrentValue() cty.Value {
	if len(e.rows) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	m := map[string]cty.Value{}
	for _, row := range e.rows {
		if row.Key == "" {
			continue // skip in-progress rows
		}
		m[row.Key] = cty.StringVal(row.Val)
	}
	if len(m) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	return cty.MapVal(m)
}

// CursorLine reports the 0-based line occupied by the cursor in View().
func (e *mapEditor) CursorLine() int {
	return e.rowCursor
}

// --- map(object(...)) ---

// mapObjectEditor handles `map(object({...}))` variables. The user sees a
// list of key rows; pressing Enter on a row drills into an objectEditor for
// that entry's value. The add-row slot appends a new entry.
//
//	[some-key]   [edit ▸]
//	[other-key]  [edit ▸]
//	+ Add row
//
// Key bindings:
//
//	↑/↓      move between rows
//	Enter    on a data row: drill into the object value editor
//	         on add-row: append a new entry and drill into it
//	Ctrl+D   delete the current row (no-op on add-row or while drilled in)
//	Backspace/runes   edit the key of the focused row
//	Esc      (when drilled in) return to the key list
type mapObjectEditor struct {
	v         *tfvars.Variable
	elemType  *tftypes.Type
	rows      []mapObjectRow
	rowCursor int // 0..len(rows); len(rows) means the add-row slot

	// drilledIn is non-nil when the user has pressed Enter on a row.
	drilledIn    Editor
	drilledInRow int
}

type mapObjectRow struct {
	Key    string
	editor Editor // objectEditor for this entry's value
}

func newMapObjectEditor(v *tfvars.Variable, current cty.Value) *mapObjectEditor {
	me := &mapObjectEditor{v: v}
	if v.Type != nil && v.Type.Element != nil {
		me.elemType = v.Type.Element
	}
	source := current
	if source == cty.NilVal || source.IsNull() {
		if v != nil && v.HasDefault && !v.Default.IsNull() {
			source = v.Default
		}
	}
	if source != cty.NilVal && !source.IsNull() &&
		(source.Type().IsMapType() || source.Type().IsObjectType()) &&
		source.LengthInt() > 0 {

		m := source.AsValueMap()
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val := m[k]
			row := mapObjectRow{Key: k, editor: me.newEntryEditor(val)}
			me.rows = append(me.rows, row)
		}
	}
	if len(me.rows) == 0 {
		me.rowCursor = 0
	}
	return me
}

// newEntryEditor creates an objectEditor for one map entry value.
func (e *mapObjectEditor) newEntryEditor(current cty.Value) Editor {
	if e.elemType == nil {
		return &readOnlyEditor{text: "(unknown element type)"}
	}
	fakeVar := &tfvars.Variable{
		Name:       "entry",
		Type:       e.elemType,
		HasDefault: false,
	}
	return newEditor(fakeVar, current)
}

func (e *mapObjectEditor) onAddRow() bool { return e.rowCursor == len(e.rows) }

func (e *mapObjectEditor) addRow() {
	row := mapObjectRow{editor: e.newEntryEditor(cty.NilVal)}
	e.rows = append(e.rows, row)
	e.rowCursor = len(e.rows) - 1
}

func (e *mapObjectEditor) deleteRow() {
	if e.onAddRow() || e.rowCursor < 0 || e.rowCursor >= len(e.rows) {
		return
	}
	e.rows = append(e.rows[:e.rowCursor], e.rows[e.rowCursor+1:]...)
	if e.rowCursor > len(e.rows) {
		e.rowCursor = len(e.rows)
	}
}

func (e *mapObjectEditor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}

	// Drilled-in mode: delegate to the entry's editor.
	if e.drilledIn != nil {
		if k.Type == tea.KeyEscape {
			e.drilledIn = nil
			return e, nil
		}
		ed, cmd := e.drilledIn.Update(msg)
		e.drilledIn = ed
		e.rows[e.drilledInRow].editor = ed
		return e, cmd
	}

	switch k.Type {
	case tea.KeyUp:
		if e.rowCursor > 0 {
			e.rowCursor--
		}
		return e, nil
	case tea.KeyDown:
		if e.rowCursor < len(e.rows) {
			e.rowCursor++
		}
		return e, nil
	case tea.KeyEnter:
		if e.onAddRow() {
			e.addRow()
			// Immediately drill into the new row's value editor.
			e.drilledIn = e.rows[e.rowCursor].editor
			e.drilledInRow = e.rowCursor
		} else {
			e.drilledIn = e.rows[e.rowCursor].editor
			e.drilledInRow = e.rowCursor
		}
		return e, nil
	case tea.KeyCtrlD:
		e.deleteRow()
		return e, nil
	case tea.KeyBackspace:
		if !e.onAddRow() && e.rowCursor >= 0 && e.rowCursor < len(e.rows) {
			key := &e.rows[e.rowCursor].Key
			if len(*key) > 0 {
				*key = (*key)[:len(*key)-1]
			}
		}
		return e, nil
	case tea.KeyCtrlU:
		if !e.onAddRow() && e.rowCursor >= 0 && e.rowCursor < len(e.rows) {
			e.rows[e.rowCursor].Key = ""
		}
		return e, nil
	case tea.KeySpace:
		if !e.onAddRow() && e.rowCursor >= 0 && e.rowCursor < len(e.rows) {
			e.rows[e.rowCursor].Key += " "
		}
		return e, nil
	case tea.KeyRunes:
		if !e.onAddRow() && e.rowCursor >= 0 && e.rowCursor < len(e.rows) {
			e.rows[e.rowCursor].Key += string(k.Runes)
		}
		return e, nil
	}
	return e, nil
}

func (e *mapObjectEditor) View() string {
	// Drilled-in: show breadcrumb + entry editor.
	if e.drilledIn != nil {
		var b strings.Builder
		key := e.rows[e.drilledInRow].Key
		if key == "" {
			key = "(unnamed)"
		}
		fmt.Fprintf(&b, "%s\n\n", styleVarHeader.Render(key))
		b.WriteString(e.drilledIn.View())
		fmt.Fprintf(&b, "\n\n%s", styleHelp.Render("[Esc] back to map"))
		return b.String()
	}

	var b strings.Builder
	for i, row := range e.rows {
		focused := i == e.rowCursor
		key := renderMapCell(row.Key, focused, "(key)")
		editHint := styleHelp.Render("[edit ▸]")
		if focused {
			editHint = styleCursorActive.Render("[edit ▸]")
		}
		fmt.Fprintf(&b, "  %s  %s\n", key, editHint)
	}
	addLabel := "+ Add row"
	if e.onAddRow() {
		fmt.Fprintf(&b, "  %s\n", styleCursorActive.Render(addLabel))
	} else {
		fmt.Fprintf(&b, "  %s\n", styleHelp.Render(addLabel))
	}
	fmt.Fprintln(&b)
	fmt.Fprint(&b, styleHelp.Render(
		"[↑↓] row   [Enter] edit value   [Ctrl+D] delete row"))
	return b.String()
}

func (e *mapObjectEditor) CurrentValue() cty.Value {
	if len(e.rows) == 0 {
		return cty.EmptyObjectVal
	}
	m := map[string]cty.Value{}
	for _, row := range e.rows {
		if row.Key == "" {
			continue
		}
		if wv, ok := row.editor.(EditorWithValue); ok {
			m[row.Key] = wv.CurrentValue()
		}
	}
	if len(m) == 0 {
		return cty.EmptyObjectVal
	}
	return cty.ObjectVal(m)
}

// CursorLine reports the 0-based line occupied by the cursor in View().
func (e *mapObjectEditor) CursorLine() int {
	if e.drilledIn != nil {
		if sub, ok := e.drilledIn.(EditorWithCursor); ok {
			return sub.CursorLine() + 2
		}
		return 2
	}
	return e.rowCursor
}

// --- list(T) / set(T) ---

type listEditor struct {
	v        *tfvars.Variable
	elements []string // string-formatted; OK for v1 since lists are usually scalars
	isSet    bool
}

func newListEditor(v *tfvars.Variable, current cty.Value) *listEditor {
	le := &listEditor{v: v, isSet: v.Type.Kind == tftypes.KindSet}
	if current != cty.NilVal && !current.IsNull() {
		for _, val := range current.AsValueSlice() {
			if val.Type() == cty.String {
				le.elements = append(le.elements, val.AsString())
			} else {
				le.elements = append(le.elements, val.GoString())
			}
		}
	}
	return le
}

func (e *listEditor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}
	switch k.String() {
	case "a", "+":
		e.elements = append(e.elements, "")
	case "d", "-":
		if len(e.elements) > 0 {
			e.elements = e.elements[:len(e.elements)-1]
		}
	}
	return e, nil
}
func (e *listEditor) View() string {
	tag := "List"
	if e.isSet {
		tag = "Set"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%d entries)\n", tag, len(e.elements))
	for i, el := range e.elements {
		fmt.Fprintf(&b, "  [%d] %q\n", i, el)
	}
	fmt.Fprint(&b, "\n", styleHelp.Render("[a] add  [d] del"))
	return b.String()
}
func (e *listEditor) CurrentValue() cty.Value {
	if len(e.elements) == 0 {
		if e.isSet {
			return cty.SetValEmpty(cty.String)
		}
		return cty.ListValEmpty(cty.String)
	}
	vals := make([]cty.Value, len(e.elements))
	for i, s := range e.elements {
		vals[i] = cty.StringVal(s)
	}
	if e.isSet {
		return cty.SetVal(vals)
	}
	return cty.ListVal(vals)
}

// --- object ---

// objectEditor renders the fields of an object variable as a vertical
// scrollable list and forwards keystrokes to the focused field's sub-editor.
// The user navigates fields with ↑/↓ and edits them in place with the
// widget that matches the field's type (space toggles a bool, typing fills
// a string, +/- steps a number, etc.).
//
// For fields whose own type is a collection (object/map/list/set) pressing
// Enter drills into the sub-editor. Esc returns to the parent field list.
type objectEditor struct {
	v      *tfvars.Variable
	fields []objectFieldRow
	cursor int

	// drilledIn is non-nil when the user has pressed Enter on a collection
	// field, delegating all input/view to that field's sub-editor. Esc
	// exits the drill-in and returns to the field list.
	drilledIn      Editor
	drilledInField int // index into fields
}

type objectFieldRow struct {
	Name string
	Type *tftypes.Type
	// HasDefault and Default mirror the declared optional(T, default) on
	// the underlying type, so ResetFocused can rebuild the sub-editor with
	// the right starting value without re-parsing the type expression.
	HasDefault bool
	Default    cty.Value
	editor     Editor
}

func newObjectEditor(v *tfvars.Variable, current cty.Value) *objectEditor {
	oe := &objectEditor{v: v}
	if v.Type == nil {
		return oe
	}
	curMap := map[string]cty.Value{}
	if current != cty.NilVal && !current.IsNull() && current.Type().IsObjectType() {
		curMap = current.AsValueMap()
	}
	for _, name := range v.Type.AttrOrder {
		attr := v.Type.Attributes[name]
		row := objectFieldRow{
			Name:       name,
			Type:       attr.Type,
			HasDefault: attr.HasDefault,
			Default:    attr.Default,
		}
		fakeVar := &tfvars.Variable{
			Name:       name,
			Type:       attr.Type,
			HasDefault: attr.HasDefault,
			Default:    attr.Default,
		}
		fv := curMap[name]
		row.editor = newEditor(fakeVar, fv)
		oe.fields = append(oe.fields, row)
	}
	return oe
}

// ResetFocused rebuilds the focused field's sub-editor from the field's
// declared default, throwing away any user edits to it. The aggregated
// CurrentValue() will then report the default; the caller (model layer) is
// responsible for propagating it into state.Values and the sparse-write
// rule takes care of removing the field from main.tf.
func (e *objectEditor) ResetFocused() {
	if e.cursor < 0 || e.cursor >= len(e.fields) {
		return
	}
	f := &e.fields[e.cursor]
	fakeVar := &tfvars.Variable{
		Name:       f.Name,
		Type:       f.Type,
		HasDefault: f.HasDefault,
		Default:    f.Default,
	}
	f.editor = newEditor(fakeVar, cty.NilVal)
}

// Update routes key events. When drilled into a collection field, all input
// is delegated to the sub-editor (Esc exits). Otherwise, arrow keys move the
// field cursor; Enter on a collection field drills in; everything else is
// forwarded to the focused scalar field's sub-editor.
func (e *objectEditor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}

	// --- Drilled-in mode: delegate everything except Esc. ---
	if e.drilledIn != nil {
		if k.Type == tea.KeyEscape {
			e.drilledIn = nil
			return e, nil
		}
		ed, cmd := e.drilledIn.Update(msg)
		e.drilledIn = ed
		e.fields[e.drilledInField].editor = ed
		return e, cmd
	}

	switch k.Type {
	case tea.KeyUp:
		if e.cursor > 0 {
			e.cursor--
		}
		return e, nil
	case tea.KeyDown:
		if e.cursor < len(e.fields)-1 {
			e.cursor++
		}
		return e, nil
	case tea.KeyHome:
		e.cursor = 0
		return e, nil
	case tea.KeyEnd:
		e.cursor = len(e.fields) - 1
		return e, nil
	case tea.KeyPgUp:
		e.cursor -= 5
		if e.cursor < 0 {
			e.cursor = 0
		}
		return e, nil
	case tea.KeyPgDown:
		e.cursor += 5
		if e.cursor >= len(e.fields) {
			e.cursor = len(e.fields) - 1
		}
		return e, nil
	case tea.KeyEnter:
		// Drill into collection fields on Enter.
		if e.cursor >= 0 && e.cursor < len(e.fields) {
			t := e.fields[e.cursor].Type
			if t != nil && (t.Kind == tftypes.KindMap ||
				t.Kind == tftypes.KindList ||
				t.Kind == tftypes.KindSet ||
				t.Kind == tftypes.KindObject) {
				e.drilledIn = e.fields[e.cursor].editor
				e.drilledInField = e.cursor
				return e, nil
			}
		}
	}

	// For collection fields that aren't drilled into, swallow non-Enter
	// keystrokes so the user doesn't get spurious edits.
	if e.cursor >= 0 && e.cursor < len(e.fields) {
		t := e.fields[e.cursor].Type
		if t != nil && (t.Kind == tftypes.KindObject ||
			t.Kind == tftypes.KindMap ||
			t.Kind == tftypes.KindList ||
			t.Kind == tftypes.KindSet) {
			return e, nil
		}
		ed, cmd := e.fields[e.cursor].editor.Update(msg)
		e.fields[e.cursor].editor = ed
		return e, cmd
	}
	return e, nil
}

func (e *objectEditor) View() string {
	if len(e.fields) == 0 {
		return styleDescription.Render("(empty object)")
	}

	// Drilled-in: show breadcrumb + sub-editor.
	if e.drilledIn != nil {
		var b strings.Builder
		fieldName := e.fields[e.drilledInField].Name
		fmt.Fprintf(&b, "%s > %s\n\n",
			styleVarHeader.Render(e.v.Name),
			styleVarHeader.Render(fieldName))
		b.WriteString(e.drilledIn.View())
		fmt.Fprintf(&b, "\n\n%s", styleHelp.Render("[Esc] back"))
		return b.String()
	}

	var b strings.Builder
	for i, f := range e.fields {
		focused := i == e.cursor
		fmt.Fprintln(&b, renderObjectFieldRow(f, focused))
	}
	fmt.Fprintln(&b)
	fmt.Fprint(&b, styleHelp.Render("[↑↓] field   "+typeSpecificHint(e.fields[e.cursor].Type)))
	return b.String()
}

// CursorLine reports the 0-based line that the cursor occupies in View().
// Used by the right-pane scroll logic to keep the cursor visible.
func (e *objectEditor) CursorLine() int {
	if e.drilledIn != nil {
		// Drilled-in: delegate if sub-editor has a cursor, offset by 2 (breadcrumb + blank).
		if sub, ok := e.drilledIn.(EditorWithCursor); ok {
			return sub.CursorLine() + 2
		}
		return 2
	}
	return e.cursor
}

// renderObjectFieldRow draws one field row inside an object editor.
// Focused row: an accented chevron and a colour-tinted name.
func renderObjectFieldRow(f objectFieldRow, focused bool) string {
	const nameWidth = 22

	caret := "  "
	name := fmt.Sprintf("%-*s", nameWidth, f.Name)
	if focused {
		caret = styleCursorInactive.Render("▸ ")
		name = styleVarHeader.Render(name)
	}

	value := compactFieldView(f)
	return caret + name + " " + value
}

// compactFieldView gives a one-line rendering for any field, regardless of
// type — used inside object editors where the multi-line views of nested
// collections would blow out the layout. Scalars use their normal editor
// view; collections summarise.
func compactFieldView(f objectFieldRow) string {
	if f.Type == nil {
		return ""
	}
	switch f.Type.Kind {
	case tftypes.KindObject:
		count := compactObjectCount(f.editor)
		return styleDescription.Render(fmt.Sprintf("(object: %d fields)", count))
	case tftypes.KindMap:
		count := compactMapCount(f.editor)
		return styleDescription.Render(fmt.Sprintf("(map: %d entries)", count))
	case tftypes.KindList:
		return styleDescription.Render("(list)")
	case tftypes.KindSet:
		return styleDescription.Render("(set)")
	}
	return f.editor.View()
}

// compactObjectCount peeks into a nested objectEditor for its field count
// (used purely for the compact placeholder rendering).
func compactObjectCount(ed Editor) int {
	if o, ok := ed.(*objectEditor); ok {
		return len(o.fields)
	}
	return 0
}

// compactMapCount peeks into a nested mapEditor for its entry count.
func compactMapCount(ed Editor) int {
	if m, ok := ed.(*mapEditor); ok {
		return len(m.rows)
	}
	return 0
}

// typeSpecificHint returns a one-line hint matching the focused field's
// editor surface. Keeps the help text honest about what's actually wired.
func typeSpecificHint(t *tftypes.Type) string {
	if t == nil {
		return ""
	}
	switch t.Kind {
	case tftypes.KindBool:
		return "[space] toggle"
	case tftypes.KindString:
		return "type to edit"
	case tftypes.KindNumber:
		return "type digits, '.', '-', 'e'"
	case tftypes.KindObject, tftypes.KindMap, tftypes.KindList, tftypes.KindSet:
		return "[Enter] drill in"
	}
	return ""
}

func (e *objectEditor) CurrentValue() cty.Value {
	m := map[string]cty.Value{}
	for _, f := range e.fields {
		if wv, ok := f.editor.(EditorWithValue); ok {
			m[f.Name] = wv.CurrentValue()
		}
	}
	if len(m) == 0 {
		return cty.EmptyObjectVal
	}
	return cty.ObjectVal(m)
}
