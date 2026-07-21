package tui

import (
	"sync"
	"testing"
	"time"
)

// --- extractPhase ---

func TestExtractPhase_Empty(t *testing.T) {
	if got := extractPhase(""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestExtractPhase_DecorationLines(t *testing.T) {
	for _, line := range []string{"─", "╷", "│", "╵"} {
		if got := extractPhase(line); got != "" {
			t.Errorf("line %q: got %q, want empty", line, got)
		}
	}
}

func TestExtractPhase_InitBackend(t *testing.T) {
	got := extractPhase("Initializing the backend...")
	if got != "Initializing backend…" {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_InitProviders(t *testing.T) {
	got := extractPhase("Initializing provider plugins...")
	if got != "Initializing providers…" {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_Installing(t *testing.T) {
	got := extractPhase("- Installing hashicorp/aws v5.31.0...")
	if got != "- Installing hashicorp/aws v5.31.0..." {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_Finding(t *testing.T) {
	got := extractPhase(`- Finding hashicorp/aws versions matching "~> 5.0"...`)
	if got != `- Finding hashicorp/aws versions matching "~> 5.0"...` {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_InitModules(t *testing.T) {
	got := extractPhase("Initializing modules...")
	if got != "Initializing modules…" {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_Creating(t *testing.T) {
	got := extractPhase("module.cos.juju_application.app: Creating...")
	if got != "module.cos.juju_application.app: Creating" {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_Modifying(t *testing.T) {
	got := extractPhase("module.cos.juju_application.app: Modifying...")
	if got != "module.cos.juju_application.app: Modifying" {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_Destroying(t *testing.T) {
	got := extractPhase("module.cos.juju_application.app: Destroying...")
	if got != "module.cos.juju_application.app: Destroying" {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_StillCreating(t *testing.T) {
	got := extractPhase("module.cos.juju_application.app: Still creating... [10s elapsed]")
	if got != "module.cos.juju_application.app: Still creating... [10s elapsed]" {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_CreationComplete(t *testing.T) {
	got := extractPhase("module.cos.juju_application.app: Creation complete after 5s")
	if got != "module.cos.juju_application.app: Creation complete after 5s" {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_Reading(t *testing.T) {
	got := extractPhase("module.cos.juju_application.app: Reading...")
	if got != "module.cos.juju_application.app: Reading" {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_PlanSummary(t *testing.T) {
	got := extractPhase("Plan: 3 to add, 1 to change, 0 to destroy.")
	if got != "Plan: 3 to add, 1 to change, 0 to destroy." {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_ApplyComplete(t *testing.T) {
	got := extractPhase("Apply complete! Resources: 3 added, 1 changed, 0 destroyed.")
	if got != "Apply complete! Resources: 3 added, 1 changed, 0 destroyed." {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_NoChanges(t *testing.T) {
	got := extractPhase("No changes. Your infrastructure matches the configuration.")
	if got != "No changes. Your infrastructure matches the configuration." {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_UnknownLine(t *testing.T) {
	got := extractPhase("some random terraform output")
	if got != "" {
		t.Errorf("got %q, want empty for unknown line", got)
	}
}

func TestExtractPhase_RefreshingState(t *testing.T) {
	got := extractPhase("module.cos.juju_application.app: Refreshing state... [id=abc123]")
	if got != "module.cos.juju_application.app: Refreshing" {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_ReadComplete(t *testing.T) {
	got := extractPhase("module.cos.juju_application.app: Read complete after 2s")
	if got != "module.cos.juju_application.app: Reading" {
		t.Errorf("got %q", got)
	}
}

func TestExtractPhase_StillModifying(t *testing.T) {
	got := extractPhase("module.x.aws_instance.web: Still modifying... [id=i-abc123]")
	if got != "module.x.aws_instance.web: Still modifying... [id=i-abc123]" {
		t.Errorf("got %q", got)
	}
}

// --- extractResourcePhase ---

func TestExtractResourcePhase_WithAction(t *testing.T) {
	got := extractResourcePhase("module.cos.juju_application.app: Creating...", "Creating")
	want := "module.cos.juju_application.app: Creating"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractResourcePhase_NoAction(t *testing.T) {
	got := extractResourcePhase("module.cos.juju_application.app: Still creating... [10s]", "")
	want := "module.cos.juju_application.app: Still creating... [10s]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractResourcePhase_NoColon(t *testing.T) {
	got := extractResourcePhase("simple line", "Reading")
	want := "simple line [Reading]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractResourcePhase_NoColonNoAction(t *testing.T) {
	got := extractResourcePhase("simple line", "")
	if got != "simple line" {
		t.Errorf("got %q, want simple line", got)
	}
}

// --- ProgressWriter ---

func TestProgressWriter_MultipleLines(t *testing.T) {
	tracker := NewProgressTracker()
	pw := &ProgressWriter{Tracker: tracker}
	pw.Write([]byte("line1\nline2\n"))
	if got := tracker.Phase(); got != "" {
		t.Errorf("phase after noise lines: got %q, want empty", got)
	}
}

func TestProgressWriter_CreateLine(t *testing.T) {
	tracker := NewProgressTracker()
	pw := &ProgressWriter{Tracker: tracker}
	pw.Write([]byte("module.x.juju_application.app: Creating...\n"))
	if got := tracker.Phase(); got != "module.x.juju_application.app: Creating" {
		t.Errorf("phase: got %q", got)
	}
}

func TestProgressWriter_PartialLine(t *testing.T) {
	tracker := NewProgressTracker()
	pw := &ProgressWriter{Tracker: tracker}
	pw.Write([]byte("module.x.juju_application.app: Creating..."))
	// No newline yet — phase should not be updated.
	if got := tracker.Phase(); got != "" {
		t.Errorf("phase after partial write: got %q, want empty", got)
	}
	pw.Write([]byte("\n"))
	if got := tracker.Phase(); got != "module.x.juju_application.app: Creating" {
		t.Errorf("phase after newline: got %q", got)
	}
}

func TestProgressWriter_PlanSummary(t *testing.T) {
	tracker := NewProgressTracker()
	pw := &ProgressWriter{Tracker: tracker}
	pw.Write([]byte("Plan: 2 to add, 0 to change, 0 to destroy.\n"))
	if got := tracker.Phase(); got != "Plan: 2 to add, 0 to change, 0 to destroy." {
		t.Errorf("phase: got %q", got)
	}
}

func TestProgressWriter_SkipsDecoration(t *testing.T) {
	tracker := NewProgressTracker()
	pw := &ProgressWriter{Tracker: tracker}
	pw.Write([]byte("─────\n"))
	if got := tracker.Phase(); got != "" {
		t.Errorf("phase after decoration: got %q, want empty", got)
	}
}

func TestProgressWriter_LastPhaseWins(t *testing.T) {
	tracker := NewProgressTracker()
	pw := &ProgressWriter{Tracker: tracker}
	pw.Write([]byte("module.x: Creating...\n"))
	pw.Write([]byte("module.y: Destroying...\n"))
	if got := tracker.Phase(); got != "module.y: Destroying" {
		t.Errorf("phase: got %q, want module.y: Destroying", got)
	}
}

// --- ProgressTracker ---

func TestProgressTracker_ConcurrentAccess(t *testing.T) {
	tracker := NewProgressTracker()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tracker.SetPhase("phase")
			_ = tracker.Phase()
			_ = tracker.Elapsed()
		}(i)
	}
	wg.Wait()
}

func TestProgressTracker_Elapsed(t *testing.T) {
	tracker := NewProgressTracker()
	time.Sleep(10 * time.Millisecond)
	if got := tracker.Elapsed(); got < 10*time.Millisecond {
		t.Errorf("elapsed: got %v, want >= 10ms", got)
	}
}
