package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

func mustParseType(t *testing.T, src string) *tftypes.Type {
	t.Helper()
	tp, err := tftypes.ParseTypeExpr(src)
	if err != nil {
		t.Fatal(err)
	}
	return tp
}

func sampleState(t *testing.T) *wrapper.State {
	t.Helper()
	return &wrapper.State{
		Vars: []tfvars.Variable{
			{Name: "model_uuid", Type: mustParseType(t, "string"), Nullable: true},
			{Name: "internal_tls", Type: mustParseType(t, "bool"), HasDefault: true, Default: cty.True, Nullable: true},
			{Name: "count", Type: mustParseType(t, "number"), HasDefault: true, Default: cty.NumberIntVal(1), Nullable: true},
		},
		Values: map[string]cty.Value{},
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case " ", "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "delete", "del":
		return tea.KeyMsg{Type: tea.KeyDelete}
	case "alt+delete", "alt+del":
		return tea.KeyMsg{Type: tea.KeyDelete, Alt: true}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "alt+backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace, Alt: true}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+k":
		return tea.KeyMsg{Type: tea.KeyCtrlK}
	case "ctrl+w":
		return tea.KeyMsg{Type: tea.KeyCtrlW}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+a":
		return tea.KeyMsg{Type: tea.KeyCtrlA}
	case "ctrl+e":
		return tea.KeyMsg{Type: tea.KeyCtrlE}
	case "ctrl+left":
		return tea.KeyMsg{Type: tea.KeyCtrlLeft}
	case "ctrl+right":
		return tea.KeyMsg{Type: tea.KeyCtrlRight}
	case "ctrl+home":
		return tea.KeyMsg{Type: tea.KeyCtrlHome}
	case "ctrl+end":
		return tea.KeyMsg{Type: tea.KeyCtrlEnd}
	case "alt+b":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true}
	case "alt+f":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}, Alt: true}
	case "alt+d":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}, Alt: true}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func feed(m *Model, msgs ...tea.Msg) *Model {
	var current tea.Model = m
	for _, msg := range msgs {
		current, _ = current.Update(msg)
	}
	return current.(*Model)
}

func TestNew_initialCursorOnFirstVariable(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		t.Fatalf("cursor out of range: %d / %d", m.cursor, len(m.rows))
	}
	v := m.SelectedVariable()
	if v == nil || v.Name != "model_uuid" {
		t.Errorf("expected initial selection model_uuid, got %v", v)
	}
}

func TestArrowKeys_moveBetweenVariables(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = feed(m, key("down"))
	if v := m.SelectedVariable(); v == nil || v.Name != "internal_tls" {
		t.Errorf("after down, got %v", v)
	}
	m = feed(m, key("down"))
	if v := m.SelectedVariable(); v == nil || v.Name != "count" {
		t.Errorf("after second down, got %v", v)
	}
	m = feed(m, key("up"))
	if v := m.SelectedVariable(); v == nil || v.Name != "internal_tls" {
		t.Errorf("after up, got %v", v)
	}
}

func TestBoolEditor_toggle(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24}, key("down")) // select internal_tls

	if m.SelectedVariable().Name != "internal_tls" {
		t.Fatal("setup")
	}

	// Focus the editor.
	m = feed(m, key("tab"))
	if m.focus != focusRight {
		t.Fatalf("focus = %v", m.focus)
	}

	// Default is true; toggling should produce false.
	m = feed(m, key(" "))
	if val, ok := m.State.Values["internal_tls"]; !ok {
		t.Errorf("toggle should store value")
	} else if val.True() {
		t.Errorf("after one toggle expected false, got true")
	}

	// And toggling again gets back to true.
	m = feed(m, key(" "))
	if val := m.State.Values["internal_tls"]; !val.True() {
		t.Errorf("after second toggle expected true")
	}
}

func TestStringEditor_typing(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24}, key("tab"))

	m = feed(m, key("a"), key("b"), key("c"))
	if got := m.State.Values["model_uuid"]; got.AsString() != "abc" {
		t.Errorf("expected 'abc', got %v", got.GoString())
	}

	m = feed(m, key("backspace"))
	if got := m.State.Values["model_uuid"]; got.AsString() != "ab" {
		t.Errorf("expected 'ab' after backspace, got %v", got.GoString())
	}
}

func TestNumberEditor_typingReplacesDefault(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24}, key("down"), key("down"), key("tab"))
	if m.SelectedVariable().Name != "count" {
		t.Fatalf("setup failed; selected = %v", m.SelectedVariable())
	}
	// Default is "1" pre-populated in the buffer. Backspace and re-type.
	m = feed(m, key("backspace"), key("4"), key("2"))
	v := m.State.Values["count"]
	if !v.Equals(cty.NumberFloatVal(42)).True() {
		t.Errorf("expected 42 after typing, got %v", v.GoString())
	}
}

func TestNumberEditor_acceptsSignAndDecimal(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24}, key("down"), key("down"), key("tab"))
	m = feed(m, key("ctrl+u")) // clear pre-populated default
	for _, k := range []string{"-", "3", ".", "1", "4"} {
		m = feed(m, key(k))
	}
	v := m.State.Values["count"]
	if !v.Equals(cty.NumberFloatVal(-3.14)).True() {
		t.Errorf("expected -3.14, got %v", v.GoString())
	}
}

func TestNumberEditor_rejectsLetters(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24}, key("down"), key("down"), key("tab"))
	m = feed(m, key("ctrl+u"))
	// Letters other than 'e'/'E' should be dropped.
	m = feed(m, key("a"), key("b"), key("c"), key("1"))
	v := m.State.Values["count"]
	if !v.Equals(cty.NumberFloatVal(1)).True() {
		t.Errorf("only digits should land; got %v", v.GoString())
	}
}

func TestTab_switchesFocus(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.focus != focusLeft {
		t.Errorf("initial focus = %v", m.focus)
	}
	m = feed(m, key("tab"))
	if m.focus != focusRight {
		t.Errorf("after tab focus = %v", m.focus)
	}
	m = feed(m, key("tab"))
	if m.focus != focusLeft {
		t.Errorf("after second tab focus = %v", m.focus)
	}
}

func TestQ_quitsFromLeftPane(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !out.(*Model).quit {
		t.Errorf("q from left pane should quit")
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit command")
	}
}

func TestVarMarker(t *testing.T) {
	state := &wrapper.State{
		Vars: []tfvars.Variable{
			{Name: "required", Type: mustParseType(t, "string")},
			{Name: "optional", Type: mustParseType(t, "bool"), HasDefault: true, Default: cty.True},
		},
		Values: map[string]cty.Value{},
	}
	if got := stripANSI(varMarker(state, "required")); got != "[!]" {
		t.Errorf("required+unset = %q", got)
	}
	state.Values["required"] = cty.StringVal("x")
	if got := stripANSI(varMarker(state, "required")); got != "[✓]" {
		t.Errorf("required+set = %q", got)
	}

	if got := stripANSI(varMarker(state, "optional")); got != "[ ]" {
		t.Errorf("optional+default = %q", got)
	}
	state.Values["optional"] = cty.False
	if got := stripANSI(varMarker(state, "optional")); got != "[✓]" {
		t.Errorf("optional+changed = %q", got)
	}
}

func TestView_doesNotPanic(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	out := m.View()
	if !strings.Contains(out, "model_uuid") {
		t.Errorf("view should list model_uuid; got:\n%s", out)
	}
}

// TestRefModal_pasteSupport verifies that pasting text into the ref input
// field appends all pasted runes, not just the first character.
func TestRefModal_pasteSupport(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.RefSwitcher = &stubRefSwitcher{}

	// Open the ref modal.
	m = feed(m, key("R"))
	if !m.refModal {
		t.Fatal("expected refModal to be open after pressing R")
	}

	// Simulate a paste event: KeyRunes with Paste flag and multiple runes.
	pasteMsg := tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("feature/my-branch"),
		Paste: true,
	}
	m = feed(m, pasteMsg)

	if m.refInput != "feature/my-branch" {
		t.Errorf("after paste, refInput = %q; want %q", m.refInput, "feature/my-branch")
	}
}

// TestRefModal_typingSingleChars verifies basic character-by-character input.
func TestRefModal_typingSingleChars(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.RefSwitcher = &stubRefSwitcher{}

	m = feed(m, key("R"))
	m = feed(m, key("m"), key("a"), key("i"), key("n"))

	if m.refInput != "main" {
		t.Errorf("refInput = %q; want %q", m.refInput, "main")
	}

	// Backspace removes one char.
	m = feed(m, key("backspace"))
	if m.refInput != "mai" {
		t.Errorf("after backspace, refInput = %q; want %q", m.refInput, "mai")
	}

	// Ctrl+U clears all.
	m = feed(m, key("ctrl+u"))
	if m.refInput != "" {
		t.Errorf("after ctrl+u, refInput = %q; want empty", m.refInput)
	}
}

// stubRefSwitcher satisfies the RefSwitcher interface for tests.
type stubRefSwitcher struct{}

func (s *stubRefSwitcher) SwitchRef(_ context.Context, _ string) (*RefSwitchResult, error) {
	return &RefSwitchResult{}, nil
}
