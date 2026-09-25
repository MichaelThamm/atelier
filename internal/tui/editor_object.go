package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

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
