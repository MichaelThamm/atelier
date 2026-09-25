package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// --- map(string) ---

// mapEditor is the widget for `map(string)` variables. The user navigates a
// 2-column grid (key, value) plus an "Add row" affordance at the bottom.
//
//	[some-key]   = [some-value]
//	[other-key]  = [other-value]
//	+ Add row
//
// Key bindings inside the editor (see ADR-0023):
//
//	↑/↓                  move between rows; add-row slot is one past the last
//	Enter                advance forward: key→value, value→next row's key,
//	                     value(last row)→new row's key; on the add-row slot,
//	                     append a new row and focus its key. Blocked on an
//	                     empty key ("key required").
//	Alt+Delete           delete the current row; a populated row asks for a
//	                     second Alt+Delete to confirm. No-op on add-row.
//	(any readline edit)  routed to the focused cell — see ADR-0020
//
// All caret-aware editing (←/→, Home/End, Ctrl+←/→, Ctrl+W, Backspace,
// Delete, Ctrl+U, Ctrl+K, …) is owned by the focused cell's cellInput.
// Tab is *not* handled here — it is the app-wide pane-switch key. Non-string
// element types fall back to a read-only message — the dispatching in
// newEditor handles that branch.
type mapEditor struct {
	v         *tfvars.Variable
	rows      []mapRow
	rowCursor int // 0..len(rows); len(rows) means the add-row slot
	colCursor int // 0 = key, 1 = value

	// fresh marks rows appended in this session (as opposed to loaded from
	// main.tf). A fresh, untouched row is silently abandoned when the user
	// moves away from it — see ADR-0023 §4.
	// A row is "fresh" while its index is in this set.
	fresh map[int]bool

	// confirmDelete is the row index awaiting a second Alt+Delete to
	// confirm removal of a populated row (ADR-0023 §5); -1 when none.
	confirmDelete int
	// nudge is a transient one-line message rendered in the hint area
	// (e.g. "key required"); cleared on the next successful move/edit.
	nudge string
}

type mapRow struct {
	Key cellInput
	Val cellInput
}

func newMapEditor(v *tfvars.Variable, current cty.Value) *mapEditor {
	me := &mapEditor{v: v, fresh: map[int]bool{}, confirmDelete: -1}
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
	idx := len(e.rows) - 1
	e.fresh[idx] = true
	e.rowCursor = idx
	e.colCursor = 0
	e.confirmDelete = -1
	e.applyFocus()
}

// rowAbandonable reports whether row i is a fresh, untouched row — appended
// in this session with an empty key and no value content. Such a row is
// silently removed when the user moves away from it (ADR-0023 §4).
func (e *mapEditor) rowAbandonable(i int) bool {
	if i < 0 || i >= len(e.rows) || !e.fresh[i] {
		return false
	}
	return e.rows[i].Key.Value() == "" && e.rows[i].Val.Value() == ""
}

// removeRow drops row i and fixes up the fresh-set indices.
func (e *mapEditor) removeRow(i int) {
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

// abandonIfEmpty removes the current row if it is a fresh, untouched row,
// leaving the cursor on a sensible neighbour. Returns true if a row was
// dropped. Called before any move that leaves the current row.
func (e *mapEditor) abandonIfEmpty() bool {
	if e.onAddRow() || !e.rowAbandonable(e.rowCursor) {
		return false
	}
	e.removeRow(e.rowCursor)
	if e.rowCursor > len(e.rows) {
		e.rowCursor = len(e.rows)
	}
	return true
}

// deleteRow removes the current row. A populated row requires a second
// Alt+Delete to confirm (ADR-0023 §5); an empty/abandonable row goes at once.
func (e *mapEditor) deleteRow() {
	if e.onAddRow() || e.rowCursor < 0 || e.rowCursor >= len(e.rows) {
		return
	}
	populated := e.rows[e.rowCursor].Key.Value() != "" || e.rows[e.rowCursor].Val.Value() != ""
	if populated && e.confirmDelete != e.rowCursor {
		e.confirmDelete = e.rowCursor
		e.nudge = fmt.Sprintf("press Alt+Del again to remove %q", e.rows[e.rowCursor].Key.Value())
		return
	}
	e.removeRow(e.rowCursor)
	if e.rowCursor > len(e.rows) {
		e.rowCursor = len(e.rows)
	}
	e.colCursor = 0
	e.confirmDelete = -1
	e.nudge = ""
	e.applyFocus()
}

// advance implements the Enter "move forward" verb (ADR-0023 §2):
// key(non-empty)→value; value(non-last)→next row's key; value(last)→new row;
// on the add-row slot, append a row. An empty key blocks with a nudge.
func (e *mapEditor) advance() {
	if e.onAddRow() {
		e.addRow()
		return
	}
	if e.colCursor == 0 { // on the key cell
		if e.rows[e.rowCursor].Key.Value() == "" {
			e.nudge = "key required"
			return
		}
		e.colCursor = 1
		e.applyFocus()
		return
	}
	// On the value cell.
	if e.rowCursor == len(e.rows)-1 {
		// Last row: commit and spawn a fresh row.
		e.addRow()
		return
	}
	// Advance to the next existing row's key.
	e.rowCursor++
	e.colCursor = 0
	e.applyFocus()
}

func (e *mapEditor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}
	// A non-delete keystroke clears any pending delete confirmation.
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
		e.colCursor = 0
		e.applyFocus()
		return e, nil
	case k.Type == tea.KeyDown:
		e.nudge = ""
		abandoned := e.abandonIfEmpty()
		if !abandoned && e.rowCursor < len(e.rows) {
			e.rowCursor++
		}
		e.colCursor = 0
		e.applyFocus()
		return e, nil
	case k.Type == tea.KeyEnter:
		e.nudge = ""
		e.advance()
		return e, nil
	case k.Type == tea.KeyDelete && k.Alt:
		// Alt+Delete is the row-delete chord (ADR-0020 §3, ADR-0023 §5).
		e.deleteRow()
		return e, nil
	case k.Type == tea.KeyRight:
		// Right arrow at the end of the (non-empty) key cell promotes to the
		// value cell — a caret-level mirror of Enter's key→value advance
		// (ADR-0023 §2). Otherwise it is an ordinary caret move.
		if c := e.focusedCell(); c != nil && e.colCursor == 0 && c.atEnd() &&
			e.rows[e.rowCursor].Key.Value() != "" {
			e.nudge = ""
			e.colCursor = 1
			e.applyFocus()
			return e, nil
		}
	case k.Type == tea.KeyLeft:
		// Left arrow at the start of the value cell returns to the key cell,
		// the natural inverse of the Right-arrow promotion above.
		if c := e.focusedCell(); c != nil && e.colCursor == 1 && c.atStart() {
			e.nudge = ""
			e.colCursor = 0
			e.applyFocus()
			return e, nil
		}
	}
	// Everything else is readline editing for the focused cell.
	if c := e.focusedCell(); c != nil {
		changed, cmd := c.Update(msg)
		if changed {
			e.nudge = ""
			// Any edit "touches" the row, so it is no longer abandonable.
			delete(e.fresh, e.rowCursor)
		}
		return e, cmd
	}
	return e, nil
}

// AtTopLevel reports whether Esc/Tab should be owned by the model (pane
// switch) rather than the editor. mapEditor has no drill levels, so it is
// always at top level. Before yielding, it abandons a fresh empty row.
func (e *mapEditor) AtTopLevel() bool { return true }

func (e *mapEditor) View() string {
	var b strings.Builder
	for i := range e.rows {
		keyFocused := i == e.rowCursor && e.colCursor == 0
		valFocused := i == e.rowCursor && e.colCursor == 1
		key := renderMapCell(&e.rows[i].Key, keyFocused, "(key)")
		val := renderMapCell(&e.rows[i].Val, valFocused, "(value)")
		glyph := rowStatusGlyph(e.rows[i].Key.Value() != "", e.fresh[i])
		if e.confirmDelete == i {
			// Highlight the row awaiting delete confirmation.
			fmt.Fprintf(&b, "%s %s = %s\n", glyph,
				styleMarkerModified.Render(e.rows[i].Key.Value()+" ="),
				styleMarkerModified.Render(e.rows[i].Val.Value()))
			continue
		}
		fmt.Fprintf(&b, "%s %s = %s\n", glyph, key, val)
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
		"[↑↓] row   [Enter] next/add   [Alt+Del] delete row   [Tab] pane   [?] keys"))
	return b.String()
}

// rowStatusGlyph returns the left-margin status marker for a map row
// (ADR-0023 §6): a check when the row has a key, "!" when a non-fresh row is
// missing its key (invalid), and a blank for a fresh empty row.
func rowStatusGlyph(hasKey, fresh bool) string {
	switch {
	case hasKey:
		return styleMarkerModified.Render("✓")
	case fresh:
		return " "
	default:
		return styleMarkerRequired.Render("!")
	}
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
