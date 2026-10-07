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
		fv := curMap[name]
		row.editor = newFieldEditor(name, attr.Type, attr.HasDefault, attr.Default, fv)
		oe.fields = append(oe.fields, row)
	}
	oe.applyFieldFocus() // only the field under the cursor shows a caret
	return oe
}

// newFieldEditor picks the widget for one object field. A nested `map(string)`
// gets the line form rather than the two-column grid: as one field among
// several the grid hides the keys behind a count and spends two columns on
// what are usually single words. See editor_maplines.go.
func newFieldEditor(name string, typ *tftypes.Type, hasDefault bool, def, current cty.Value) Editor {
	v := &tfvars.Variable{
		Name:       name,
		Type:       typ,
		HasDefault: hasDefault,
		Default:    def,
	}
	if typ != nil && typ.Kind == tftypes.KindMap && isScalarKind(typ.Element) {
		return newLineMapEditor(v, current)
	}
	return newEditor(v, current)
}

// isScalarKind reports whether a collection's elements are plain scalars, which
// is the case the line form can render faithfully.
func isScalarKind(t *tftypes.Type) bool {
	return t != nil && (t.Kind == tftypes.KindString || t.Kind == tftypes.KindNumber || t.Kind == tftypes.KindBool)
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
	f.editor = newFieldEditor(f.Name, f.Type, f.HasDefault, f.Default, cty.NilVal)
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

	// --- Drilled-in mode: delegate everything except Esc, which pops one
	// level (ADR-0023 §3). If the sub-editor is itself drilled deeper, let
	// it absorb the Esc; otherwise close this drill level.
	if e.drilledIn != nil {
		if k.Type == tea.KeyEscape {
			if tp, ok := e.drilledIn.(depthProvider); ok && !tp.AtTopLevel() {
				ed, cmd := e.drilledIn.Update(msg)
				e.drilledIn = ed
				e.fields[e.drilledInField].editor = ed
				return e, cmd
			}
			e.drilledIn = nil
			return e, nil
		}
		ed, cmd := e.drilledIn.Update(msg)
		e.drilledIn = ed
		e.fields[e.drilledInField].editor = ed
		return e, cmd
	}

	// --- Field-list jumps (Ctrl+Home/Ctrl+End). No bare-letter aliases: they
	// would shadow characters typed into a scalar field (ADR-0020 §3, as with
	// plain Home/End below).
	keyStr := k.String()
	switch {
	case keyStr == "ctrl+home":
		e.cursor = 0
		return e, nil
	case keyStr == "ctrl+end":
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

// AtTopLevel reports whether this object editor is at its root (not drilled
// into a nested collection field), so Esc/Tab may be owned by the caller
// (ADR-0023 §3).
func (e *objectEditor) AtTopLevel() bool { return e.drilledIn == nil }

// focusable is implemented by scalar sub-editors whose caret visibility
// must track the owning objectEditor's field cursor, so a cursor is only
// ever shown on the field the user is actually on.
type focusable interface {
	Focus()
	Blur()
}

// depthProvider is implemented by editors that can be drilled into. It lets
// an owner (a parent editor, or the top-level model) decide who owns Esc/Tab:
// the editor pops one drill level while depth > 0, and only yields to the
// model's pane-switch when AtTopLevel reports true (ADR-0023 §3).
type depthProvider interface {
	AtTopLevel() bool
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
	// Pad short names to the column width; truncate long ones to it so the
	// value column stays aligned and the row can't blow past the pane width
	// (e.g. "backend_storage_directives" is wider than nameWidth). %-*s only
	// pads, never truncates, so clamp explicitly.
	displayName := f.Name
	if len(displayName) > nameWidth {
		displayName = displayName[:nameWidth-1] + "…"
	}
	name := fmt.Sprintf("%-*s", nameWidth, displayName)
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
		return styleDescription.Render(mapKeyPreview(f.editor))
	case tftypes.KindMap:
		return styleDescription.Render(mapKeyPreview(f.editor))
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

// mapKeyPreview lists a collection field's contents on the parent row, so the
// user can tell what it holds without drilling into it: `config
// (retention_time,replicas)`. It used to report a count, which named no keys
// and made every inspection of a map a drill-in round trip.
//
// The listing contains no spaces. The pane word-wraps on spaces and then
// hard-truncates whatever is left, so a preview that can be split gains a
// physical row and shoves the pane's bottom border down; an unbreakable token
// is truncated cleanly instead (see the wrap-then-truncate note in
// renderRightPane). Keys are Terraform identifiers, so a byte count is a
// display width.
func mapKeyPreview(ed Editor) string {
	keys := editorKeys(ed)
	if len(keys) == 0 {
		return "(empty)"
	}
	const (
		budget  = 34
		maxKeys = 3
	)
	var parts []string
	used := 2 // the surrounding parens
	for i, k := range keys {
		if i == maxKeys {
			if rest := len(keys) - maxKeys; used+4 <= budget {
				parts = append(parts, fmt.Sprintf("+%d", rest))
			}
			break
		}
		name := truncateMiddle(k, budget/2)
		need := len(name)
		if len(parts) > 0 {
			need++ // the comma
		}
		if used+need > budget {
			break
		}
		parts = append(parts, name)
		used += need
	}
	return "(" + strings.Join(parts, ",") + ")"
}

// editorKeys reports a collection editor's keys, sorted, or nil for anything
// that is not key-addressed.
func editorKeys(ed Editor) []string {
	switch e := ed.(type) {
	case *lineMapEditor:
		return e.lineMapKeys()
	case *mapEditor:
		keys := make([]string, 0, len(e.rows))
		for _, r := range e.rows {
			if k := r.Key.Value(); k != "" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		return keys
	case *objectEditor:
		keys := make([]string, 0, len(e.fields))
		for _, f := range e.fields {
			keys = append(keys, f.Name)
		}
		return keys
	}
	return nil
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
