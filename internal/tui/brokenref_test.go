package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/gitops"
)

// TestInit_autoOpensModalOnUnresolvedRef verifies the cold-open recovery path:
// when a wrapper is loaded with a ref that no longer resolves, the TUI drops
// straight into the ref-switch modal, pre-seeds the current ref, and seeds the
// available-refs hint the bootstrap layer handed over.
func TestInit_autoOpensModalOnUnresolvedRef(t *testing.T) {
	m := New(sampleState(t), "cos")
	m.Modules[0].Ref = "feat/old-ref"
	m.Modules[0].Switcher = &stubRefSwitcher{refs: []string{"main", "v1.0"}}
	m.RefUnresolved = &RefUnresolvedInfo{
		Ref:       "feat/old-ref",
		Reason:    `ref "feat/old-ref" no longer exists on the remote`,
		Available: []string{"main", "v1.0"},
	}

	cmd := m.Init()

	if !m.refModal {
		t.Fatal("expected ref modal to auto-open on an unresolved ref")
	}
	if m.refInput.Value() != "feat/old-ref" {
		t.Errorf("refInput = %q; want the current (broken) ref pre-seeded", m.refInput.Value())
	}
	if len(m.availableRefs) != 2 {
		t.Errorf("availableRefs = %v; want the bootstrap-supplied list seeded", m.availableRefs)
	}
	if !m.refsLoading {
		t.Error("expected an async ref refresh to be in flight")
	}
	if cmd == nil {
		t.Fatal("expected Init to return a ListRefs command")
	}

	// Drive the async fetch to completion and confirm it lands.
	msg := cmd()
	loaded, ok := msg.(refsLoadedMsg)
	if !ok {
		t.Fatalf("expected refsLoadedMsg, got %T", msg)
	}
	m2 := feed(m, loaded)
	if m2.refsLoading {
		t.Error("refsLoading should clear once refs arrive")
	}
}

// TestInit_offlineDoesNotFetch verifies that when the failure was a network
// outage (Offline), Init still opens the modal but does NOT kick off a doomed
// ListRefs fetch — there's nothing to list.
func TestInit_offlineDoesNotFetch(t *testing.T) {
	m := New(sampleState(t), "cos")
	m.Modules[0].Ref = "main"
	m.Modules[0].Switcher = &stubRefSwitcher{}
	m.RefUnresolved = &RefUnresolvedInfo{
		Ref:     "main",
		Reason:  "couldn't reach the module remote",
		Offline: true,
	}

	cmd := m.Init()

	if !m.refModal {
		t.Fatal("expected ref modal to open even when offline")
	}
	if m.refsLoading {
		t.Error("should not attempt a ref fetch while offline")
	}
	if cmd != nil {
		t.Error("expected no ListRefs command when offline")
	}
}

// TestRefSwitchError_reopensModalWithPreciseMessage verifies the in-session
// failed-switch path: typing a ref that doesn't exist re-opens the modal,
// phrases a precise "not found" message, and refreshes the hint — without
// tearing down existing state.
func TestRefSwitchError_reopensModalWithPreciseMessage(t *testing.T) {
	m := New(sampleState(t), "cos")
	m.Modules[0].Ref = "main"
	m.Modules[0].Switcher = &stubRefSwitcher{refs: []string{"main", "develop"}}
	m = feed(m, refSwitchErrorMsg{err: &gitops.RefNotFoundError{
		Ref:       "typo-branch",
		Available: []string{"main", "develop"},
	}})

	if !m.refModal {
		t.Fatal("expected the modal to re-open after a failed switch")
	}
	if m.refSwitching {
		t.Error("refSwitching should be cleared after failure")
	}
	if !strings.Contains(m.refErr, "typo-branch") || !strings.Contains(m.refErr, "not found") {
		t.Errorf("refErr = %q; want a precise not-found message mentioning the ref", m.refErr)
	}
	if len(m.availableRefs) != 2 {
		t.Errorf("availableRefs = %v; want them seeded from the typed error", m.availableRefs)
	}
	// State must be intact — the module's vars are untouched by a failed switch.
	if len(m.Modules[0].State.Vars) == 0 {
		t.Error("a failed switch must not tear down existing variable schema")
	}
}

// TestRefSwitchError_genericErrorStillReopens verifies a non-RefNotFound
// failure (e.g. terraform init error) also re-opens the modal but shows the
// raw error rather than a fabricated not-found message.
func TestRefSwitchError_genericErrorStillReopens(t *testing.T) {
	m := New(sampleState(t), "cos")
	m.Modules[0].Ref = "main"
	m.Modules[0].Switcher = &stubRefSwitcher{}
	m = feed(m, refSwitchErrorMsg{err: errors.New("terraform init boom")})

	if !m.refModal {
		t.Fatal("expected the modal to re-open after a generic failure")
	}
	if !strings.Contains(m.refErr, "terraform init boom") {
		t.Errorf("refErr = %q; want the raw error surfaced", m.refErr)
	}
}

// stubRefSwitcher's ListRefs is exercised indirectly above; assert the
// signature is satisfied at compile time here.
var _ RefSwitcher = (*stubRefSwitcher)(nil)

// Guard: ListRefs must propagate the switcher's error path as a nil list.
func TestStartListRefs_swallowsErrorToNil(t *testing.T) {
	m := New(sampleState(t), "cos")
	m.Modules[0].Switcher = &stubRefSwitcher{refsErr: context.DeadlineExceeded}
	m.refModuleIdx = 0
	cmd := m.startListRefs()
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg, ok := cmd().(refsLoadedMsg)
	if !ok {
		t.Fatalf("expected refsLoadedMsg, got %T", cmd())
	}
	if msg.refs != nil {
		t.Errorf("expected nil refs on error, got %v", msg.refs)
	}
}
