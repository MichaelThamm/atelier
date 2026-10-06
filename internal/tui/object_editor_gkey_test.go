package tui

import (
	"strings"
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

// TestObjectEditor_NoBareLetterFieldJumps confirms g/G never move the field
// cursor, on a scalar field or a caretless one: Ctrl+Home/Ctrl+End are the
// documented way to jump fields.
func TestObjectEditor_NoBareLetterFieldJumps(t *testing.T) {
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

	for _, focus := range []string{"name", "items", "tail"} {
		for oe.fields[oe.cursor].Name != focus {
			oe = drive(t, oe, "down")
		}
		for _, r := range []string{"g", "G"} {
			oe = drive(t, oe, r)
			if got := oe.fields[oe.cursor].Name; got != focus {
				t.Errorf("on field %q, %q moved the field cursor to %q", focus, r, got)
			}
		}
	}
}

// TestListNav_NoBareLetterBindings pins that the vim letters are inert in the
// variable list. Left unreviewed they invite a muscle-memory contract that
// collides with typing in the editor.
func TestListNav_NoBareLetterBindings(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = feed(m, key("down"), key("down"))
	start := m.cursor
	if start == 0 {
		t.Fatal("setup: cursor should be off the first row")
	}

	for _, r := range []string{"j", "k", "g", "G"} {
		m = feed(m, key(r))
		if m.cursor != start {
			t.Errorf("%q in the variable list moved the cursor %d → %d", r, start, m.cursor)
		}
	}
}

// TestHelpModal_AdvertisesNoVimLetters keeps the `?` modal, the source of truth
// for keybindings, in step with the handlers.
func TestHelpModal_AdvertisesNoVimLetters(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	// Tall enough that the modal is not clipped before the logs section.
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 80})
	m.helpModal = true
	plain := stripANSI(m.renderHelpModal())

	for _, line := range []string{"↑/k", "↓/j", "g/G"} {
		if strings.Contains(plain, line) {
			t.Errorf("help modal still advertises %q; got:\n%s", line, plain)
		}
	}
	for _, want := range []string{
		"↑ ↓            Move cursor",
		"Ctrl+U         Delete to start of line",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("help modal missing %q; got:\n%s", want, plain)
		}
	}
	// The logs view is gone (ADR-0052); the modal must not still point at it.
	for _, gone := range []string{"Logs view", "View terraform logs", "[L]"} {
		if strings.Contains(plain, gone) {
			t.Errorf("help modal still advertises %q; got:\n%s", gone, plain)
		}
	}
	// The modal is the source of truth for keybindings, so it must not
	// advertise a key no handler binds.
	if strings.Contains(plain, "[ / ]") {
		t.Errorf("help modal advertises [ / ], which no handler binds; got:\n%s", plain)
	}

	// The plan view has its own section, which must be clean too.
	m.helpModal = false
	m.planState = planReady
	m.helpModal = true
	plan := stripANSI(m.renderHelpModal())
	for _, line := range []string{"↑/k", "↓/j", "g/G", "[ / ]"} {
		if strings.Contains(plan, line) {
			t.Errorf("plan-view help still advertises %q; got:\n%s", line, plan)
		}
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
