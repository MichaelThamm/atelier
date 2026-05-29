package tui

import (
	"bytes"
	"strings"
	"sync"
	"time"
)

// ProgressTracker holds thread-safe progress state for long-running terraform
// operations. The plan/apply goroutine updates it via a ProgressWriter; the
// TUI reads it on each spinner tick to display elapsed time and phase info.
type ProgressTracker struct {
	mu        sync.Mutex
	phase     string
	startTime time.Time
}

// NewProgressTracker creates a tracker and records the start time.
func NewProgressTracker() *ProgressTracker {
	return &ProgressTracker{startTime: time.Now()}
}

// SetPhase updates the current phase text (thread-safe).
func (p *ProgressTracker) SetPhase(s string) {
	p.mu.Lock()
	p.phase = s
	p.mu.Unlock()
}

// Phase returns the current phase text (thread-safe).
func (p *ProgressTracker) Phase() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.phase
}

// Elapsed returns the duration since the tracker was created.
func (p *ProgressTracker) Elapsed() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return time.Since(p.startTime)
}

// ProgressWriter is an io.Writer that parses terraform's human-readable
// stdout line-by-line and updates a ProgressTracker with meaningful phase
// information.
type ProgressWriter struct {
	Tracker *ProgressTracker
	buf     []byte
}

func (w *ProgressWriter) Write(p []byte) (n int, err error) {
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSpace(string(w.buf[:idx]))
		w.buf = w.buf[idx+1:]
		if phase := extractPhase(line); phase != "" {
			w.Tracker.SetPhase(phase)
		}
	}
	return len(p), nil
}

// extractPhase distils a raw terraform output line into a short, user-friendly
// phase string. Returns "" for lines that should be ignored (blank, decoration).
func extractPhase(line string) string {
	if line == "" {
		return ""
	}
	// Skip decoration / noise lines.
	if strings.HasPrefix(line, "─") || strings.HasPrefix(line, "╷") ||
		strings.HasPrefix(line, "│") || strings.HasPrefix(line, "╵") {
		return ""
	}

	// Init phases.
	if strings.Contains(line, "Initializing the backend") {
		return "Initializing backend…"
	}
	if strings.Contains(line, "Initializing provider plugins") {
		return "Initializing providers…"
	}
	if strings.HasPrefix(line, "- Installing") {
		// e.g. "- Installing hashicorp/aws v5.31.0..."
		return line
	}
	if strings.HasPrefix(line, "- Finding") {
		// e.g. "- Finding hashicorp/aws versions matching "~> 5.0"..."
		return line
	}
	if strings.Contains(line, "Initializing modules") {
		return "Initializing modules…"
	}

	// Plan/apply resource-level progress.
	if strings.Contains(line, "Refreshing state...") {
		return extractResourcePhase(line, "Refreshing")
	}
	if strings.Contains(line, "Creating...") {
		return extractResourcePhase(line, "Creating")
	}
	if strings.Contains(line, "Modifying...") {
		return extractResourcePhase(line, "Modifying")
	}
	if strings.Contains(line, "Destroying...") {
		return extractResourcePhase(line, "Destroying")
	}
	if strings.Contains(line, "Still creating") || strings.Contains(line, "Still modifying") ||
		strings.Contains(line, "Still destroying") || strings.Contains(line, "Still reading") {
		return extractResourcePhase(line, "")
	}
	if strings.Contains(line, "Creation complete") || strings.Contains(line, "Modifications complete") ||
		strings.Contains(line, "Destruction complete") {
		return extractResourcePhase(line, "")
	}
	if strings.Contains(line, "Reading...") || strings.Contains(line, "Read complete") {
		return extractResourcePhase(line, "Reading")
	}

	// Plan summary line.
	if strings.HasPrefix(line, "Plan:") {
		return line
	}
	if strings.HasPrefix(line, "Apply complete!") {
		return line
	}
	if strings.HasPrefix(line, "No changes.") {
		return line
	}

	return ""
}

// extractResourcePhase shortens a resource-level line like
// "module.charm.juju_application.app: Creating..." to just the resource
// address and action.
func extractResourcePhase(line, _ string) string {
	// Terraform resource lines have the form "address: Action..."
	// Just return the line as-is (truncation happens at render time).
	if idx := strings.Index(line, ":"); idx > 0 {
		return strings.TrimSpace(line)
	}
	return strings.TrimSpace(line)
}
