package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// --- map(object(...)) ---

// mapObjectEditor handles `map(object({...}))` variables. The user sees a
// list of key rows; pressing Enter on a row drills into an objectEditor for
// that entry's value. The add-row slot appends a new entry.
//
//	[some-key]   [edit ▸]
//	[other-key]  [edit ▸]
//	+ Add row
//
// Key bindings (see ADR-0023):
//
//	↑/↓                  move between rows
//	Enter                on a data row with a named key: drill into the
//	                     object value editor. On the add-row slot: append a
//	                     new row and focus its key. Blocked on an empty key.
//	Alt+Delete           delete the current row; a populated row asks for a
//	                     second Alt+Delete to confirm. No-op on add-row.
//	(any readline edit)  routed to the focused row's key cell — see ADR-0020
//	Esc                  (when drilled in) return to the key list — one level
type mapObjectEditor struct {
	v         *tfvars.Variable
	elemType  *tftypes.Type
	rows      []mapObjectRow
	rowCursor int // 0..len(rows); len(rows) means the add-row slot

	// drilledIn is non-nil when the user has pressed Enter on a row.
	drilledIn    Editor
	drilledInRow int

	// fresh marks rows appended this session; a fresh, keyless row is
	// abandoned when the user moves away (ADR-0023 §4).
	fresh map[int]bool
	// confirmDelete is the row awaiting a second Alt+Delete; -1 when none.
	confirmDelete int
	// nudge is a transient one-line hint (e.g. "key required").
	nudge string
}

type mapObjectRow struct {
	Key    cellInput
	editor Editor // objectEditor for this entry's value
}

func newMapObjectEditor(v *tfvars.Variable, current cty.Value) *mapObjectEditor {
	me := &mapObjectEditor{v: v, fresh: map[int]bool{}, confirmDelete: -1}
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
	idx := len(e.rows) - 1
	e.fresh[idx] = true
	e.rowCursor = idx
	e.confirmDelete = -1
	e.applyFocus()
}

// rowAbandonable reports whether row i is a fresh, still-keyless row. Because
// a map(object) row can only be drilled into once its key is named
// (ADR-0023 §4), a fresh keyless row never holds object content, so an empty
// key is a sufficient abandonment test.
func (e *mapObjectEditor) rowAbandonable(i int) bool {
	if i < 0 || i >= len(e.rows) || !e.fresh[i] {
		return false
	}
	return e.rows[i].Key.Value() == ""
}

func (e *mapObjectEditor) removeRow(i int) {
	if i < 0 || i >= len(e.rows) {
		return
	}
	e.rows = append(e.rows[:i], e.rows[i+1:]...)
	newFresh := map[int]bool{}
	for idx := range e.fresh {
		switch {
		case idx < i:
			newFresh[idx] = true
		case idx > i:
			newFresh[idx-1] = true
		}
	}
	e.fresh = newFresh
}

func (e *mapObjectEditor) abandonIfEmpty() bool {
	if e.onAddRow() || !e.rowAbandonable(e.rowCursor) {
		return false
	}
	e.removeRow(e.rowCursor)
	if e.rowCursor > len(e.rows) {
		e.rowCursor = len(e.rows)
	}
	return true
}

// deleteRow removes the current row. A row with a named key requires a second
// Alt+Delete to confirm (ADR-0023 §5); a keyless row goes at once.
func (e *mapObjectEditor) deleteRow() {
	if e.onAddRow() || e.rowCursor < 0 || e.rowCursor >= len(e.rows) {
		return
	}
	populated := e.rows[e.rowCursor].Key.Value() != ""
	if populated && e.confirmDelete != e.rowCursor {
		e.confirmDelete = e.rowCursor
		e.nudge = fmt.Sprintf("press Alt+Del again to remove %q", e.rows[e.rowCursor].Key.Value())
		return
	}
	e.removeRow(e.rowCursor)
	if e.rowCursor > len(e.rows) {
		e.rowCursor = len(e.rows)
	}
	e.confirmDelete = -1
	e.nudge = ""
	e.applyFocus()
}

func (e *mapObjectEditor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}

	// Drilled-in mode: delegate to the entry's editor. Esc pops exactly one
	// level: if the sub-editor is itself drilled in, let it handle Esc;
	// otherwise Esc returns to this map's row list (ADR-0023 §3).
	if e.drilledIn != nil {
		if k.Type == tea.KeyEscape {
			if tp, ok := e.drilledIn.(depthProvider); ok && !tp.AtTopLevel() {
				ed, cmd := e.drilledIn.Update(msg)
				e.drilledIn = ed
				e.rows[e.drilledInRow].editor = ed
				return e, cmd
			}
			e.drilledIn = nil
			e.applyFocus()
			return e, nil
		}
		ed, cmd := e.drilledIn.Update(msg)
		e.drilledIn = ed
		e.rows[e.drilledInRow].editor = ed
		return e, cmd
	}

	if !(k.Type == tea.KeyDelete && k.Alt) {
		e.confirmDelete = -1
	}

	switch {
	case k.Type == tea.KeyUp:
		e.nudge = ""
		e.abandonIfEmpty()
		if e.rowCursor > 0 {
			e.rowCursor--
		}
		e.applyFocus()
		return e, nil
	case k.Type == tea.KeyDown:
		e.nudge = ""
		abandoned := e.abandonIfEmpty()
		if !abandoned && e.rowCursor < len(e.rows) {
			e.rowCursor++
		}
		e.applyFocus()
		return e, nil
	case k.Type == tea.KeyEnter:
		e.nudge = ""
		if e.onAddRow() {
			// Key-first: append a row and focus its key. The user names the
			// key, then Enter drills in (ADR-0023 §1–2).
			e.addRow()
			return e, nil
		}
		if e.rows[e.rowCursor].Key.Value() == "" {
			e.nudge = "key required"
			return e, nil
		}
		// Named key: drill into the object value editor.
		e.drilledIn = e.rows[e.rowCursor].editor
		e.drilledInRow = e.rowCursor
		return e, nil
	case k.Type == tea.KeyDelete && k.Alt:
		e.deleteRow()
		return e, nil
	}
	// Everything else is readline editing for the focused key cell.
	if !e.onAddRow() && e.rowCursor >= 0 && e.rowCursor < len(e.rows) {
		changed, cmd := e.rows[e.rowCursor].Key.Update(msg)
		if changed {
			e.nudge = ""
			delete(e.fresh, e.rowCursor)
		}
		return e, cmd
	}
	return e, nil
}

// AtTopLevel reports whether the editor is at its root (drill depth 0), so
// the model may treat Esc/Tab as a pane switch (ADR-0023 §3). When drilled
// in, Esc is owned by the editor to pop one level.
func (e *mapObjectEditor) AtTopLevel() bool { return e.drilledIn == nil }

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
		glyph := rowStatusGlyph(e.rows[i].Key.Value() != "", e.fresh[i])
		summary := styleDescription.Render(fmt.Sprintf("(object: %d set)", entrySetCount(e.rows[i].editor)))
		if e.confirmDelete == i {
			fmt.Fprintf(&b, "%s %s  %s\n", glyph,
				styleMarkerModified.Render(e.rows[i].Key.Value()), summary)
			continue
		}
		key := renderMapCell(&e.rows[i].Key, focused, "(key)")
		editHint := styleHelp.Render("[edit ▸]")
		if focused {
			editHint = styleCursorActive.Render("[edit ▸]")
		}
		fmt.Fprintf(&b, "%s %s  %s  %s\n", glyph, key, summary, editHint)
	}
	addLabel := "+ Add row"
	if e.onAddRow() {
		fmt.Fprintf(&b, "  %s\n", styleCursorActive.Render(addLabel))
	} else {
		fmt.Fprintf(&b, "  %s\n", styleHelp.Render(addLabel))
	}
	fmt.Fprintln(&b)
	if e.nudge != "" {
		fmt.Fprintf(&b, "%s\n", styleMarkerRequired.Render(e.nudge))
	}
	fmt.Fprint(&b, styleHelp.Render(
		"[↑↓] row   [Enter] name/edit   [Alt+Del] delete row   [Tab] pane   [?] keys"))
	return b.String()
}

// entrySetCount reports how many fields of a map(object) entry's value
// editor differ from their default / are set — used for the collapsed
// per-row summary (ADR-0023 §6). Falls back to the object's field count.
func entrySetCount(ed Editor) int {
	if o, ok := ed.(*objectEditor); ok {
		if wv, okv := ed.(EditorWithValue); okv {
			if v := wv.CurrentValue(); v != cty.NilVal && !v.IsNull() && v.Type().IsObjectType() {
				return v.LengthInt()
			}
		}
		return len(o.fields)
	}
	return compactObjectCount(ed)
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
