package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/canonical/atelier/internal/tftypes"
	"github.com/canonical/atelier/internal/tfvars"
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
func (e *readOnlyEditor) View() string                          { return styleDescription.Render(e.text) }

// --- bool ---

type boolEditor struct {
	v       *tfvars.Variable
	value   cty.Value
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
		case "t", "y":
			e.value = cty.True
		case "f", "n":
			e.value = cty.False
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

// --- string ---

type stringEditor struct {
	v     *tfvars.Variable
	value string
	null  bool // user explicitly cleared (null)
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
	case tea.KeyRunes, tea.KeySpace:
		e.value += string(k.Runes)
		e.null = false
	case tea.KeyCtrlU:
		e.value = ""
		e.null = false
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

// --- number ---

// numberEditor is a free-text widget: the user types a literal numeric
// string (digits, optional sign, decimal point, scientific notation) and
// CurrentValue parses it on demand. We deliberately don't bind `+`/`-` to
// increment/decrement — those characters need to be typeable as part of
// the number (leading sign, exponent sign).
type numberEditor struct {
	v    *tfvars.Variable
	text string
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
		return e, nil
	case tea.KeyCtrlU:
		e.text = ""
		return e, nil
	case tea.KeyRunes:
		for _, r := range k.Runes {
			if strings.ContainsRune(numberRunes, r) {
				e.text += string(r)
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

// --- map(string) ---

type mapEditor struct {
	v     *tfvars.Variable
	keys  []string
	vals  []string
	focus int    // index into keys; -1 means we're on the key, +N means on value of row N
	add   string // a pending new-key/value buffer
}

func newMapEditor(v *tfvars.Variable, current cty.Value) *mapEditor {
	me := &mapEditor{v: v}
	if current != cty.NilVal && !current.IsNull() && (current.Type().IsMapType() || current.Type().IsObjectType()) {
		m := current.AsValueMap()
		for k, val := range m {
			me.keys = append(me.keys, k)
			if val.IsNull() {
				me.vals = append(me.vals, "")
			} else if val.Type() == cty.String {
				me.vals = append(me.vals, val.AsString())
			} else {
				me.vals = append(me.vals, val.GoString())
			}
		}
	}
	return me
}

func (e *mapEditor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}
	switch k.String() {
	case "a", "+":
		// Add a placeholder entry the user fills in.
		e.keys = append(e.keys, fmt.Sprintf("key%d", len(e.keys)+1))
		e.vals = append(e.vals, "")
	case "d", "-":
		if len(e.keys) > 0 {
			e.keys = e.keys[:len(e.keys)-1]
			e.vals = e.vals[:len(e.vals)-1]
		}
	}
	return e, nil
}
func (e *mapEditor) View() string {
	var b strings.Builder
	fmt.Fprintf(&b, "map(%d entries)\n", len(e.keys))
	for i, k := range e.keys {
		fmt.Fprintf(&b, "  %s = %q\n", k, e.vals[i])
	}
	fmt.Fprint(&b, "\n", styleHelp.Render("[a] add  [d] del"))
	return b.String()
}
func (e *mapEditor) CurrentValue() cty.Value {
	if len(e.keys) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	m := map[string]cty.Value{}
	for i, k := range e.keys {
		m[k] = cty.StringVal(e.vals[i])
	}
	return cty.MapVal(m)
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
// For fields whose own type is a collection (object/map/list/set) we render
// a compact placeholder rather than the sub-editor's full multi-line view —
// drill-in for those is a separate widget pass.
type objectEditor struct {
	v      *tfvars.Variable
	fields []objectFieldRow
	cursor int
}

type objectFieldRow struct {
	Name   string
	Type   *tftypes.Type
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

// Update routes key events. Arrow keys (and Home/End/PgUp/PgDn) move the
// field cursor; everything else is forwarded to the focused field's
// sub-editor so the user can type, toggle, or step in place. Collection
// fields don't yet accept any keys (their compact view is read-only here);
// editing them is a follow-up via drill-in.
func (e *objectEditor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
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
	}

	// Collections inside an object are not yet editable inline (drill-in
	// will land separately); swallow keystrokes so the user doesn't get the
	// false impression that they're typing into something.
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
	var b strings.Builder
	for i, f := range e.fields {
		focused := i == e.cursor
		fmt.Fprintln(&b, renderObjectFieldRow(f, focused))
	}
	fmt.Fprintln(&b)
	fmt.Fprint(&b, styleHelp.Render("[↑↓] field   "+typeSpecificHint(e.fields[e.cursor].Type)))
	return b.String()
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
		return styleDescription.Render("(map)")
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
		return "nested editing — coming soon"
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
