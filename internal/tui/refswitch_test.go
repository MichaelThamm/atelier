package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// newRefState builds a minimal switched-ref state for a module block.
func newRefState(t *testing.T, blockName, source string) *wrapper.State {
	t.Helper()
	return &wrapper.State{
		ModuleBlockName: blockName,
		Source:          source,
		Vars:            sampleState(t).Vars,
		Values:          map[string]cty.Value{},
	}
}

// A primary ref switch must write the new state back into Modules[0] — the
// slice writeAllModules iterates. Previously applyRefSwitch only reassigned
// m.State, leaving Modules[0] stale so the next save silently reverted the ref.
func TestApplyRefSwitch_primaryUpdatesOwningEntry(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	newState := newRefState(t, "cos_lite", "git::https://example.com/cos//modules/cos?ref=2.0.0")
	m.refModuleIdx = 0
	m.applyRefSwitch(&RefSwitchResult{
		State:       newState,
		LiteralRef:  "2.0.0",
		ResolvedSHA: "abc1234deadbeef",
	})

	if m.Modules[0].State != newState {
		t.Errorf("Modules[0].State not updated to the switched state")
	}
	if m.State != newState {
		t.Errorf("primary alias m.State not kept coherent with Modules[0]")
	}
	if m.Modules[0].Ref != "2.0.0" {
		t.Errorf("Modules[0].Ref = %q; want 2.0.0", m.Modules[0].Ref)
	}
	if m.Modules[0].State.Source != newState.Source {
		t.Errorf("save would write stale source %q", m.Modules[0].State.Source)
	}
}

// A ref switch must preserve WIRED reference expressions (UnknownAttrs) for
// variables the new ref still declares — not just concrete values. Dropping
// them silently deletes lines like `model_uuid = data.juju_model.x.uuid` from
// the rendered module block (the reported brittleness bug).
func TestApplyRefSwitch_preservesWiredExpressions(t *testing.T) {
	old := sampleState(t)
	old.UnknownAttrs = []wrapper.RawAttr{
		{
			Name:    "model_uuid",
			Raw:     []byte("model_uuid = data.juju_model.service_model.uuid"),
			RawExpr: []byte("data.juju_model.service_model.uuid"),
		},
	}
	m := New(old, "traefik")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// New ref still declares model_uuid (e.g. a no-diff ref bump).
	newState := newRefState(t, "traefik", "git::https://example.com/traefik//mod?ref=rev300")
	m.refModuleIdx = 0
	m.applyRefSwitch(&RefSwitchResult{
		State:       newState,
		LiteralRef:  "rev300",
		ResolvedSHA: "cafe1234",
	})

	got := m.Modules[0].State.UnknownAttrs
	if len(got) != 1 || got[0].Name != "model_uuid" {
		t.Fatalf("wired expression not carried over: %+v", got)
	}
	if string(got[0].RawExpr) != "data.juju_model.service_model.uuid" {
		t.Errorf("wired expression mangled: %q", got[0].RawExpr)
	}
}

// Wired expressions for variables the new ref no longer declares are dropped.
func TestApplyRefSwitch_dropsOrphanedWiredExpressions(t *testing.T) {
	old := sampleState(t)
	old.UnknownAttrs = []wrapper.RawAttr{
		{Name: "gone_var", Raw: []byte("gone_var = data.x.y.z"), RawExpr: []byte("data.x.y.z")},
		{Name: "model_uuid", Raw: []byte("model_uuid = data.m.u"), RawExpr: []byte("data.m.u")},
	}
	m := New(old, "traefik")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// New ref declares model_uuid but NOT gone_var (sampleState has no gone_var).
	newState := newRefState(t, "traefik", "git::https://example.com/traefik//mod?ref=rev300")
	m.refModuleIdx = 0
	m.applyRefSwitch(&RefSwitchResult{State: newState, LiteralRef: "rev300", ResolvedSHA: "f00d"})

	got := m.Modules[0].State.UnknownAttrs
	if len(got) != 1 || got[0].Name != "model_uuid" {
		t.Fatalf("expected only model_uuid retained, got %+v", got)
	}
}

// Switching a secondary module's ref must update only that entry and leave the
// primary completely untouched.
func TestApplyRefSwitch_secondaryLeavesPrimaryUntouched(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m.Modules[0].Ref = "1.0.0"
	m.AddModuleEntry(ModuleEntry{
		State:    sampleState(t),
		Name:     "mimir",
		Ref:      "1.0.0",
		Switcher: &stubRefSwitcher{},
	})
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	primaryState := m.Modules[0].State
	newSec := newRefState(t, "mimir", "git::https://example.com/mimir//mod?ref=2.0.0")
	m.refModuleIdx = 1
	m.applyRefSwitch(&RefSwitchResult{
		State:       newSec,
		LiteralRef:  "2.0.0",
		ResolvedSHA: "feed1234",
	})

	if m.Modules[1].State != newSec {
		t.Errorf("secondary entry not updated")
	}
	if m.Modules[1].Ref != "2.0.0" {
		t.Errorf("secondary ref = %q; want 2.0.0", m.Modules[1].Ref)
	}
	if m.Modules[0].State != primaryState {
		t.Errorf("primary state was mutated by a secondary switch")
	}
	if m.State != primaryState {
		t.Errorf("primary alias was mutated by a secondary switch")
	}
	if m.Modules[0].Ref != "1.0.0" {
		t.Errorf("primary ref changed by a secondary switch: %q", m.Modules[0].Ref)
	}
}

// Pressing R targets the module under the cursor, not always the primary.
func TestRefKey_targetsModuleUnderCursor(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m.Modules[0].Ref = "1.0.0"
	m.Modules[0].Switcher = &stubRefSwitcher{}
	m.AddModuleEntry(ModuleEntry{
		State:    sampleState(t),
		Name:     "mimir",
		Ref:      "3.0.0",
		Switcher: &stubRefSwitcher{},
	})
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Navigate down into the mimir section (3 primary vars, then headers are
	// skipped automatically).
	for i := 0; i < 3; i++ {
		m = feed(m, key("down"))
	}
	if m.activeModuleIdx() != 1 {
		t.Fatalf("setup: expected cursor on secondary module, idx=%d", m.activeModuleIdx())
	}

	m = feed(m, key("R"))
	if !m.refModal {
		t.Fatal("expected ref modal to open")
	}
	if m.refModuleIdx != 1 {
		t.Errorf("refModuleIdx = %d; want 1 (secondary)", m.refModuleIdx)
	}
	if m.refInput != "3.0.0" {
		t.Errorf("ref input seeded with %q; want secondary ref 3.0.0", m.refInput)
	}
}

// A module with no switcher (local source) must make R a no-op rather than
// opening the modal against some other module's switcher.
func TestRefKey_localModuleIsNoOp(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m.Modules[0].Switcher = &stubRefSwitcher{}
	m.Modules[0].Ref = "1.0.0"
	m.AddModuleEntry(ModuleEntry{
		State: sampleState(t),
		Name:  "local_mod",
		// no Switcher → local source
	})
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	for i := 0; i < 3; i++ {
		m = feed(m, key("down"))
	}
	if m.activeModuleIdx() != 1 {
		t.Fatalf("setup: expected cursor on local module, idx=%d", m.activeModuleIdx())
	}

	m = feed(m, key("R"))
	if m.refModal {
		t.Errorf("ref modal should not open for a local-source module")
	}
}
