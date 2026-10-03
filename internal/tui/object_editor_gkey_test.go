package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// cosOffersLikeVar mirrors charmed-spark's `cos_offers`: an object whose
// fields are all strings a user types into. It is the shape that surfaced the
// `g` field-jump stealing a typed character.
func cosOffersLikeVar(t *testing.T) tfvars.Variable {
	t.Helper()
	tp, err := tftypes.ParseTypeExpr(`object({
		dashboard = optional(string, "")
		logging   = optional(string, "")
		metrics   = optional(string, "")
	})`)
	if err != nil {
		t.Fatal(err)
	}
	return tfvars.Variable{
		Name:       "cos_offers",
		Type:       tp,
		HasDefault: true,
		Default:    cty.EmptyObjectVal,
	}
}

// TestObjectEditor_TypeableRunesReachScalarField covers the field-jump keys
// that are also ordinary characters: with a caret in a string field they must
// insert, not move the field cursor (ADR-0020 §3 — a key the focused cell can
// use belongs to the cell).
func TestObjectEditor_TypeableRunesReachScalarField(t *testing.T) {
	for _, r := range []string{"g", "G", "j", "k", "a", "d", "l", "p", "s", "q", "n", "f", "t", "y", "e", "o"} {
		t.Run(r, func(t *testing.T) {
			oe := objectEditorOf(t, cosOffersLikeVar(t))
			// Focus the second field so a field jump would be observable.
			oe = drive(t, oe, "down")
			if oe.fields[oe.cursor].Name != "logging" {
				t.Fatalf("setup: focused = %q", oe.fields[oe.cursor].Name)
			}

			oe = drive(t, oe, r)

			val := oe.CurrentValue().AsValueMap()["logging"]
			if got := val.AsString(); got != r {
				t.Errorf("after typing %q, logging = %q; want %q", r, got, r)
			}
			if oe.fields[oe.cursor].Name != "logging" {
				t.Errorf("typing %q moved the field cursor to %q", r, oe.fields[oe.cursor].Name)
			}
		})
	}
}

// TestObjectEditor_FieldJumpsOnCaretlessField confirms g/G still move the
// field cursor when the focused field has no caret to type into, so the
// navigation is narrowed rather than removed.
func TestObjectEditor_FieldJumpsOnCaretlessField(t *testing.T) {
	tp, err := tftypes.ParseTypeExpr(`object({
		name  = optional(string, "")
		items = optional(list(string), [])
		tail  = optional(string, "")
	})`)
	if err != nil {
		t.Fatal(err)
	}
	oe := objectEditorOf(t, tfvars.Variable{
		Name: "obj", Type: tp, HasDefault: true, Default: cty.EmptyObjectVal,
	})

	for oe.fields[oe.cursor].Name != "items" {
		oe = drive(t, oe, "down")
	}
	if objectFieldHasCellInput(oe.focusedField()) {
		t.Fatalf("setup: collection field unexpectedly has a cell input")
	}

	oe = drive(t, oe, "g")
	if oe.fields[oe.cursor].Name != "name" {
		t.Errorf("g on a caretless field: focused = %q; want name", oe.fields[oe.cursor].Name)
	}

	for oe.fields[oe.cursor].Name != "items" {
		oe = drive(t, oe, "down")
	}
	oe = drive(t, oe, "G")
	if got := oe.fields[oe.cursor].Name; got != "tail" {
		t.Errorf("G on a caretless field: focused = %q; want tail", got)
	}
}

// TestObjectEditor_TypeableRunes_ThroughTopLevelModel drives the reported
// path: a composed wrapper, focus the editor, type into an object field. The
// top-level router must not intercept a printable rune either.
func TestObjectEditor_TypeableRunes_ThroughTopLevelModel(t *testing.T) {
	state := sampleState(t)
	state.Vars = append(state.Vars, cosOffersLikeVar(t))
	m := New(state, "charmed_spark")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	for i := 0; i < len(state.Vars)-1; i++ {
		m = feed(m, key("down"))
	}
	if v := m.SelectedVariable(); v == nil || v.Name != "cos_offers" {
		t.Fatalf("setup: SelectedVariable = %v", v)
	}
	m = feed(m, key("tab"))
	if m.focus != focusRight {
		t.Fatalf("focus = %v, want focusRight", m.focus)
	}

	// Second field ("logging"), then type a "g" into it.
	m = feed(m, key("down"), key("g"))

	val, ok := m.State.Values["cos_offers"]
	if !ok {
		t.Fatal("cos_offers not stored in state")
	}
	got := val.AsValueMap()["logging"].AsString()
	if got != "g" {
		t.Errorf("logging = %q; want %q", got, "g")
	}
}
