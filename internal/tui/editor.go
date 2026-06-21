package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// --- cellInput: the one readline cell -----------------------------------
//
// Every scalar text/number buffer in the right-pane editors flows through
// this wrapper around bubbles/textinput. It centralises the readline
// keymap (Home/End, Ctrl+←/→, Ctrl+W, Alt+D, …), sensitive-echo handling,
// width/scroll behaviour, and rendering with our `[…]` bracket style — so
// individual editors never re-implement any of it. See ADR-0020.
type cellInput struct {
	ti        textinput.Model
	sensitive bool
	// allowedRunes, when non-empty, restricts which runes can be inserted
	// via keystrokes. Unmatched runes are silently dropped before the
	// event reaches textinput (textinput's own Validate hook only records
	// an error, it does not refuse the insertion). Used by numberEditor.
	allowedRunes string
}

// newCellInput builds a cell input pre-seeded with value. allowedRunes is
// optional; when non-empty, only those runes can be inserted via typing
// (used by numberEditor to refuse non-numeric runes). sensitive switches
// the cell to password-echo mode.
func newCellInput(value string, sensitive bool, allowedRunes string) cellInput {
	ti := textinput.New()
	ti.Cursor.SetMode(cursor.CursorStatic) // solid caret, never blinks
	ti.Prompt = ""
	ti.CharLimit = 0
	if sensitive {
		ti.EchoMode = textinput.EchoPassword
		ti.EchoCharacter = '•'
	}
	ti.SetValue(value)
	ti.CursorEnd()
	ti.Focus()
	return cellInput{ti: ti, sensitive: sensitive, allowedRunes: allowedRunes}
}

// Update forwards a key event to the underlying textinput. Returns whether
// the buffer or caret actually moved, so callers can avoid spurious
// "touched" flags when the user is e.g. just navigating with arrow keys.
//
// When allowedRunes is set, rune events are filtered before forwarding —
// disallowed runes are dropped silently. textinput's own Validate hook
// only records an error and does not refuse the insertion, so we filter
// here instead.
func (c *cellInput) Update(msg tea.Msg) (textChanged bool, cmd tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.Type == tea.KeyRunes && c.allowedRunes != "" {
		filtered := k.Runes[:0:0]
		for _, r := range k.Runes {
			if strings.ContainsRune(c.allowedRunes, r) {
				filtered = append(filtered, r)
			}
		}
		if len(filtered) == 0 {
			return false, nil
		}
		k.Runes = filtered
		msg = k
	}
	prevValue := c.ti.Value()
	prevPos := c.ti.Position()
	var m textinput.Model
	m, cmd = c.ti.Update(msg)
	c.ti = m
	return c.ti.Value() != prevValue || c.ti.Position() != prevPos, cmd
}

// View renders the cell in a `[…]` bracket. When unfocused, no caret is
// drawn. Sensitive cells echo `•` regardless of focus.
func (c *cellInput) View() string {
	// Render without textinput's own bracket/prompt: we wrap in `[…]` for
	// visual consistency with the rest of the editor surface.
	if !c.ti.Focused() {
		// textinput.View always appends an end-of-line cursor placeholder
		// (a trailing space). A blurred cell shows no caret, so render the
		// value ourselves — otherwise every unfocused cell reads as `[3 ]`.
		return "[" + c.echoedValue() + "]"
	}
	return "[" + c.ti.View() + "]"
}

// echoedValue is the cell's value as displayed: the raw value, or a run of
// `•` for sensitive cells. Used to render blurred cells without a caret.
func (c *cellInput) echoedValue() string {
	v := c.ti.Value()
	if c.sensitive {
		return strings.Repeat("•", len([]rune(v)))
	}
	return v
}

// Value reports the current buffer (decoded, not echoed).
func (c *cellInput) Value() string { return c.ti.Value() }

// SetValue replaces the buffer wholesale and parks the caret at the end.
func (c *cellInput) SetValue(s string) {
	c.ti.SetValue(s)
	c.ti.CursorEnd()
}

// SetWidth configures the visible width for horizontal-scroll behaviour
// inside the textinput. Pass 0 to disable.
func (c *cellInput) SetWidth(w int) { c.ti.Width = w }

// Focus marks the cell as receiving keystrokes; Blur drops focus so a
// passive cell renders without a caret.
func (c *cellInput) Focus() { c.ti.Focus() }
func (c *cellInput) Blur()  { c.ti.Blur() }

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
	cell    cellInput
	null    bool // user explicitly cleared (null)
	touched bool
}

func newStringEditor(v *tfvars.Variable, current cty.Value) *stringEditor {
	se := &stringEditor{v: v}
	initial := ""
	if current == cty.NilVal {
		if v.HasDefault && !v.Default.IsNull() && v.Default.Type() == cty.String {
			initial = v.Default.AsString()
		}
	} else if current.IsNull() {
		se.null = true
	} else if current.Type() == cty.String {
		initial = current.AsString()
	}
	se.cell = newCellInput(initial, v != nil && v.Sensitive, "")
	return se
}

func (e *stringEditor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); !ok {
		return e, nil
	}
	prev := e.cell.Value()
	changed, cmd := e.cell.Update(msg)
	if e.cell.Value() != prev {
		e.null = false
	}
	if changed {
		e.touched = true
	}
	return e, cmd
}
func (e *stringEditor) View() string {
	if e.v != nil && e.v.Sensitive {
		return fmt.Sprintf("%s %s",
			e.cell.View(),
			styleSensitiveTag.Render("sensitive"))
	}
	return e.cell.View()
}
func (e *stringEditor) CurrentValue() cty.Value {
	if e.null {
		return cty.NullVal(cty.String)
	}
	return cty.StringVal(e.cell.Value())
}
func (e *stringEditor) Touched() bool { return e.touched }

// Focus/Blur toggle this editor's caret. An owning objectEditor uses them
// to keep a cursor only on the field under its cursor (see focusable).
func (e *stringEditor) Focus() { e.cell.Focus() }
func (e *stringEditor) Blur()  { e.cell.Blur() }

// --- number ---

// numberEditor is a free-text widget: the user types a literal numeric
// string (digits, optional sign, decimal point, scientific notation) and
// CurrentValue parses it on demand. We deliberately don't bind `+`/`-` to
// increment/decrement — those characters need to be typeable as part of
// the number (leading sign, exponent sign).
type numberEditor struct {
	v       *tfvars.Variable
	cell    cellInput
	touched bool
}

// numberRunes is the set of characters accepted inside the input. Anything
// else is silently ignored so a stray letter keypress doesn't pollute the
// buffer. Enforced at the cellInput boundary, not via textinput.Validate
// (textinput's Validate only records an error; it does not refuse the
// insertion).
const numberRunes = "0123456789.-+eE"

func newNumberEditor(v *tfvars.Variable, current cty.Value) *numberEditor {
	ne := &numberEditor{v: v}
	initial := ""
	if current != cty.NilVal && !current.IsNull() && current.Type() == cty.Number {
		initial = current.AsBigFloat().Text('f', -1)
	} else if v.HasDefault && !v.Default.IsNull() {
		initial = v.Default.AsBigFloat().Text('f', -1)
	}
	ne.cell = newCellInput(initial, false, numberRunes)
	return ne
}

func (e *numberEditor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); !ok {
		return e, nil
	}
	changed, cmd := e.cell.Update(msg)
	if changed {
		e.touched = true
	}
	return e, cmd
}

func (e *numberEditor) View() string {
	body := e.cell.View()
	if v := e.cell.Value(); v != "" {
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return styleRequiredTag.Render(body) + " " + styleHelp.Render("(invalid number)")
		}
	}
	return body
}

func (e *numberEditor) CurrentValue() cty.Value {
	v := e.cell.Value()
	if v == "" {
		return cty.NilVal
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		return cty.NumberFloatVal(n)
	}
	return cty.NilVal
}
func (e *numberEditor) Touched() bool { return e.touched }

func (e *numberEditor) Focus() { e.cell.Focus() }
func (e *numberEditor) Blur()  { e.cell.Blur() }

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
//	↑/↓                  move between rows; add-row slot is one past the last
//	Tab / Shift+Tab      cycle cells: key → value → next-row key (and back)
//	Enter                on add-row: append a new empty row and focus its key
//	Alt+Delete           delete the current row (no-op on add-row)
//	(any readline edit)  routed to the focused cell — see ADR-0020
//
// All caret-aware editing (←/→, Home/End, Ctrl+←/→, Ctrl+W, Backspace,
// Delete, Ctrl+U, Ctrl+K, …) is owned by the focused cell's cellInput.
// Non-string element types fall back to a read-only message — the
// dispatching in newEditor handles that branch.
type mapEditor struct {
	v         *tfvars.Variable
	rows      []mapRow
	rowCursor int // 0..len(rows); len(rows) means the add-row slot
	colCursor int // 0 = key, 1 = value
}

type mapRow struct {
	Key cellInput
	Val cellInput
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
			valStr := ""
			if !val.IsNull() && val.Type() == cty.String {
				valStr = val.AsString()
			}
			me.rows = append(me.rows, newMapRow(k, valStr))
		}
	}
	me.applyFocus()
	// Start on the add-row when the map is empty, otherwise on the first
	// row's key.
	if len(me.rows) == 0 {
		me.rowCursor = 0 // == len(rows); add-row
	}
	return me
}

func newMapRow(key, val string) mapRow {
	return mapRow{
		Key: newCellInput(key, false, ""),
		Val: newCellInput(val, false, ""),
	}
}

func (e *mapEditor) onAddRow() bool { return e.rowCursor == len(e.rows) }

// focusedCell returns a pointer to the focused cellInput, or nil when the
// cursor sits on the add-row.
func (e *mapEditor) focusedCell() *cellInput {
	if e.onAddRow() || e.rowCursor < 0 || e.rowCursor >= len(e.rows) {
		return nil
	}
	if e.colCursor == 0 {
		return &e.rows[e.rowCursor].Key
	}
	return &e.rows[e.rowCursor].Val
}

// applyFocus blurs every cell and re-focuses the one under the cursor.
// Called after any movement so only the active cell renders a caret.
func (e *mapEditor) applyFocus() {
	for i := range e.rows {
		e.rows[i].Key.Blur()
		e.rows[i].Val.Blur()
	}
	if c := e.focusedCell(); c != nil {
		c.Focus()
	}
}

// Focus/Blur let the model gate the caret on pane focus, so the cursor only
// appears while the editor pane is the active context.
func (e *mapEditor) Focus() { e.applyFocus() }
func (e *mapEditor) Blur() {
	for i := range e.rows {
		e.rows[i].Key.Blur()
		e.rows[i].Val.Blur()
	}
}

func (e *mapEditor) addRow() {
	e.rows = append(e.rows, newMapRow("", ""))
	e.rowCursor = len(e.rows) - 1
	e.colCursor = 0
	e.applyFocus()
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
	e.applyFocus()
}

// cycleCell advances the cell-cursor key → value → next-row-key → … When
// direction is -1 (Shift+Tab), it walks the other way. Wrapping past the
// last row's value lands on the add-row.
func (e *mapEditor) cycleCell(direction int) {
	if direction >= 0 {
		// Forward: key → value → next row's key → … → last value → add-row.
		if e.onAddRow() {
			// From add-row, Tab takes the user back to the very first cell.
			if len(e.rows) > 0 {
				e.rowCursor = 0
				e.colCursor = 0
			}
		} else if e.colCursor == 0 {
			e.colCursor = 1
		} else {
			e.rowCursor++
			e.colCursor = 0
		}
	} else {
		// Backward: value → key → previous row's value → …
		if e.onAddRow() {
			if len(e.rows) > 0 {
				e.rowCursor = len(e.rows) - 1
				e.colCursor = 1
			}
		} else if e.colCursor == 1 {
			e.colCursor = 0
		} else if e.rowCursor > 0 {
			e.rowCursor--
			e.colCursor = 1
		}
	}
	e.applyFocus()
}

func (e *mapEditor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}
	switch {
	case k.Type == tea.KeyUp:
		if e.rowCursor > 0 {
			e.rowCursor--
		}
		e.applyFocus()
		return e, nil
	case k.Type == tea.KeyDown:
		if e.rowCursor < len(e.rows) {
			e.rowCursor++
		}
		e.applyFocus()
		return e, nil
	case k.Type == tea.KeyTab:
		e.cycleCell(+1)
		return e, nil
	case k.Type == tea.KeyShiftTab:
		e.cycleCell(-1)
		return e, nil
	case k.Type == tea.KeyEnter:
		if e.onAddRow() {
			e.addRow()
		}
		return e, nil
	case k.Type == tea.KeyDelete && k.Alt:
		// Alt+Delete is the row-delete chord (ADR-0020 §3). Ctrl+D, the
		// previous binding, now belongs to the cell as delete-forward.
		e.deleteRow()
		return e, nil
	}
	// Everything else is readline editing for the focused cell.
	if c := e.focusedCell(); c != nil {
		_, cmd := c.Update(msg)
		return e, cmd
	}
	return e, nil
}

func (e *mapEditor) View() string {
	var b strings.Builder
	for i := range e.rows {
		keyFocused := i == e.rowCursor && e.colCursor == 0
		valFocused := i == e.rowCursor && e.colCursor == 1
		key := renderMapCell(&e.rows[i].Key, keyFocused, "(key)")
		val := renderMapCell(&e.rows[i].Val, valFocused, "(value)")
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
		"[↑↓] row   [Tab] cell   [Enter] add row   [Alt+Del] delete row   [?] keys"))
	return b.String()
}

// renderMapCell renders one cell, with a bracket wrapper. Empty unfocused
// cells show a dim placeholder so the user knows what goes there.
func renderMapCell(c *cellInput, focused bool, placeholder string) string {
	if focused {
		return styleCursorActive.Render(c.View())
	}
	if c.Value() == "" {
		return "[" + styleHelp.Render(placeholder) + "]"
	}
	return c.View()
}

func (e *mapEditor) CurrentValue() cty.Value {
	if len(e.rows) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	m := map[string]cty.Value{}
	for _, row := range e.rows {
		key := row.Key.Value()
		if key == "" {
			continue // skip in-progress rows
		}
		m[key] = cty.StringVal(row.Val.Value())
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
//	↑/↓                  move between rows
//	Enter                on a data row: drill into the object value editor
//	                     on add-row: append a new entry and drill into it
//	Alt+Delete           delete the current row (no-op on add-row or drilled in)
//	(any readline edit)  routed to the focused row's key cell — see ADR-0020
//	Esc                  (when drilled in) return to the key list
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
	Key    cellInput
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
			me.rows = append(me.rows, mapObjectRow{
				Key:    newCellInput(k, false, ""),
				editor: me.newEntryEditor(val),
			})
		}
	}
	if len(me.rows) == 0 {
		me.rowCursor = 0
	}
	me.applyFocus()
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

// applyFocus blurs every row's key cell and re-focuses the one under the
// cursor (no-op on the add-row). Called after any movement so only the
// active cell renders a caret.
func (e *mapObjectEditor) applyFocus() {
	for i := range e.rows {
		e.rows[i].Key.Blur()
	}
	if !e.onAddRow() && e.rowCursor >= 0 && e.rowCursor < len(e.rows) {
		e.rows[e.rowCursor].Key.Focus()
	}
}

// Focus/Blur gate the caret on pane focus (see mapEditor.Focus). When
// drilled into an entry, the active editor is that sub-editor.
func (e *mapObjectEditor) Focus() {
	if e.drilledIn != nil {
		if f, ok := e.drilledIn.(focusable); ok {
			f.Focus()
		}
		return
	}
	e.applyFocus()
}
func (e *mapObjectEditor) Blur() {
	if f, ok := e.drilledIn.(focusable); ok {
		f.Blur()
	}
	for i := range e.rows {
		e.rows[i].Key.Blur()
	}
}

func (e *mapObjectEditor) addRow() {
	e.rows = append(e.rows, mapObjectRow{
		Key:    newCellInput("", false, ""),
		editor: e.newEntryEditor(cty.NilVal),
	})
	e.rowCursor = len(e.rows) - 1
	e.applyFocus()
}

func (e *mapObjectEditor) deleteRow() {
	if e.onAddRow() || e.rowCursor < 0 || e.rowCursor >= len(e.rows) {
		return
	}
	e.rows = append(e.rows[:e.rowCursor], e.rows[e.rowCursor+1:]...)
	if e.rowCursor > len(e.rows) {
		e.rowCursor = len(e.rows)
	}
	e.applyFocus()
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

	switch {
	case k.Type == tea.KeyUp:
		if e.rowCursor > 0 {
			e.rowCursor--
		}
		e.applyFocus()
		return e, nil
	case k.Type == tea.KeyDown:
		if e.rowCursor < len(e.rows) {
			e.rowCursor++
		}
		e.applyFocus()
		return e, nil
	case k.Type == tea.KeyEnter:
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
	case k.Type == tea.KeyDelete && k.Alt:
		// Alt+Delete is the row-delete chord (ADR-0020 §3). Ctrl+D, the
		// previous binding, now belongs to the focused cell.
		e.deleteRow()
		return e, nil
	}
	// Everything else is readline editing for the focused key cell.
	if !e.onAddRow() && e.rowCursor >= 0 && e.rowCursor < len(e.rows) {
		_, cmd := e.rows[e.rowCursor].Key.Update(msg)
		return e, cmd
	}
	return e, nil
}

func (e *mapObjectEditor) View() string {
	// Drilled-in: show breadcrumb + entry editor.
	if e.drilledIn != nil {
		var b strings.Builder
		key := e.rows[e.drilledInRow].Key.Value()
		if key == "" {
			key = "(unnamed)"
		}
		fmt.Fprintf(&b, "%s\n\n", styleVarHeader.Render(key))
		b.WriteString(e.drilledIn.View())
		fmt.Fprintf(&b, "\n\n%s", styleHelp.Render("[Esc] back to map"))
		return b.String()
	}

	var b strings.Builder
	for i := range e.rows {
		focused := i == e.rowCursor
		key := renderMapCell(&e.rows[i].Key, focused, "(key)")
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
		"[↑↓] row   [Enter] edit value   [Alt+Del] delete row   [?] keys"))
	return b.String()
}

func (e *mapObjectEditor) CurrentValue() cty.Value {
	if len(e.rows) == 0 {
		return cty.EmptyObjectVal
	}
	m := map[string]cty.Value{}
	for _, row := range e.rows {
		key := row.Key.Value()
		if key == "" {
			continue
		}
		if wv, ok := row.editor.(EditorWithValue); ok {
			m[key] = wv.CurrentValue()
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
	oe.applyFieldFocus() // only the field under the cursor shows a caret
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
	// Reconcile caret visibility to the field cursor on every update, so
	// only the field the user is on ever shows a cursor (see applyFieldFocus).
	defer e.applyFieldFocus()
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

	// --- Field-list jumps (g/G/Ctrl+Home/Ctrl+End). These take precedence
	// over readline forwarding so the user always has a way to jump the
	// field cursor even while a scalar field has a caret. (Plain
	// Home/End belong to the cell — see below.)
	keyStr := k.String()
	switch {
	case keyStr == "ctrl+home", keyStr == "g":
		e.cursor = 0
		return e, nil
	case keyStr == "ctrl+end", keyStr == "G":
		e.cursor = len(e.fields) - 1
		if e.cursor < 0 {
			e.cursor = 0
		}
		return e, nil
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
	case tea.KeyHome, tea.KeyEnd:
		// ADR-0020 §3: Home/End belong to the focused cell when one is
		// active. Field-list jumps move to g/G or Ctrl+Home/Ctrl+End
		// (handled above). Fall through to the sub-editor forwarding below
		// when the focused field has a caret; for collection fields (no
		// caret), still treat Home/End as field jumps.
		if !objectFieldHasCellInput(e.focusedField()) {
			if k.Type == tea.KeyHome {
				e.cursor = 0
			} else {
				e.cursor = len(e.fields) - 1
			}
			return e, nil
		}
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

// focusable is implemented by scalar sub-editors whose caret visibility
// must track the owning objectEditor's field cursor, so a cursor is only
// ever shown on the field the user is actually on.
type focusable interface {
	Focus()
	Blur()
}

// applyFieldFocus blurs every scalar field's caret except the one under the
// cursor. Without this, every scalar field renders its own caret (each
// cellInput is focused at construction), littering the object view with
// cursors. Collection fields hold no caret and are left untouched.
func (e *objectEditor) applyFieldFocus() {
	for i := range e.fields {
		f, ok := e.fields[i].editor.(focusable)
		if !ok {
			continue
		}
		if i == e.cursor {
			f.Focus()
		} else {
			f.Blur()
		}
	}
}

// Focus/Blur gate the caret on pane focus (see mapEditor.Focus). When
// drilled into a collection field, the active editor is that sub-editor.
func (e *objectEditor) Focus() {
	if e.drilledIn != nil {
		if f, ok := e.drilledIn.(focusable); ok {
			f.Focus()
		}
		return
	}
	e.applyFieldFocus()
}
func (e *objectEditor) Blur() {
	if f, ok := e.drilledIn.(focusable); ok {
		f.Blur()
	}
	for i := range e.fields {
		if f, ok := e.fields[i].editor.(focusable); ok {
			f.Blur()
		}
	}
}

// focusedField returns the editor for the field under the cursor, or nil
// if the cursor is out of range.
func (e *objectEditor) focusedField() Editor {
	if e.cursor < 0 || e.cursor >= len(e.fields) {
		return nil
	}
	return e.fields[e.cursor].editor
}

// objectFieldHasCellInput reports whether the field's editor owns a
// cellInput (i.e. is a scalar string/number editor). Used to decide
// whether Home/End should belong to the cell or to the field list.
func objectFieldHasCellInput(ed Editor) bool {
	switch ed.(type) {
	case *stringEditor, *numberEditor:
		return true
	}
	return false
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
// Detailed readline bindings live in the help modal (ADR-0020 §5).
func typeSpecificHint(t *tftypes.Type) string {
	if t == nil {
		return "[?] keys"
	}
	switch t.Kind {
	case tftypes.KindBool:
		return "[space] toggle   [?] keys"
	case tftypes.KindString, tftypes.KindNumber:
		return "type to edit   [?] keys"
	case tftypes.KindObject, tftypes.KindMap, tftypes.KindList, tftypes.KindSet:
		return "[Enter] drill in   [?] keys"
	}
	return "[?] keys"
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
