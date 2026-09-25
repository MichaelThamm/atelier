package tui

import (
	"fmt"
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

// ViewInline renders the focused cell without the surrounding `[…]` bracket.
// It exists for the ref-switch modal, which composes its own label around the
// field ("New ref: …") rather than presenting it as a bracketed value cell.
// Like View, a blurred cell is drawn without a caret.
func (c *cellInput) ViewInline() string {
	if !c.ti.Focused() {
		return c.echoedValue()
	}
	return c.ti.View()
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

// atEnd reports whether the caret sits at the end of the buffer, and atStart
// whether it sits at the beginning. Used by the map editor to promote a
// horizontal arrow key at the cell boundary into a cell move (→ off the end
// of the key advances to the value; ← off the start of the value returns to
// the key).
func (c *cellInput) atEnd() bool   { return c.ti.Position() >= len([]rune(c.ti.Value())) }
func (c *cellInput) atStart() bool { return c.ti.Position() <= 0 }

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
	v         *tfvars.Variable
	cell      cellInput
	touched   bool
	lastValid cty.Value // last successfully parsed value; preserved while editing
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
		ne.lastValid = current
	} else if v.HasDefault && !v.Default.IsNull() {
		initial = v.Default.AsBigFloat().Text('f', -1)
		ne.lastValid = v.Default
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
		val := cty.NumberFloatVal(n)
		e.lastValid = val
		return val
	}
	// Input is non-empty but unparseable — preserve the last valid value
	// instead of returning NilVal (which would delete the variable from state).
	if e.lastValid != cty.NilVal {
		return e.lastValid
	}
	return cty.NilVal
}
func (e *numberEditor) Touched() bool { return e.touched }

func (e *numberEditor) Focus() { e.cell.Focus() }
func (e *numberEditor) Blur()  { e.cell.Blur() }
