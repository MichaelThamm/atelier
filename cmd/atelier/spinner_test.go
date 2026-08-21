package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsTerminal(t *testing.T) {
	// A regular file is not a terminal — this is the redirected-output case.
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("a regular file must not be treated as a terminal")
	}

	// Neither is a pipe — the `atelier import | tee log` case.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(w) {
		t.Error("a pipe must not be treated as a terminal")
	}

	if isTerminal(nil) {
		t.Error("nil must not be treated as a terminal")
	}

	// Documents what the check actually detects: /dev/null is a character
	// device, so it reads as a terminal. Harmless, since the output is discarded.
	if devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
		defer devNull.Close()
		if !isTerminal(devNull) {
			t.Error("expected a character device to read as a terminal")
		}
	}
}

// withCapturedStderr swaps os.Stderr for a temp file and returns its contents.
func withCapturedStderr(t *testing.T, fn func()) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stderr")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = f
	defer func() {
		os.Stderr = orig
		f.Close()
	}()
	fn()
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The regression: with stderr redirected, the spinner must emit the message once
// and never animate. Previously it appended a fresh copy every 100ms — 1344 of
// them in one real import run — because nothing interprets the carriage return.
func TestStartSpinnerDoesNotAnimateWhenRedirected(t *testing.T) {
	const msg = "Matching live resources to module addresses…"
	got := withCapturedStderr(t, func() {
		stop := startSpinner(msg)
		// Well past several 100ms ticks, so an animating spinner would be obvious.
		time.Sleep(350 * time.Millisecond)
		stop()
	})

	if n := strings.Count(got, msg); n != 1 {
		t.Errorf("message written %d times, want exactly 1:\n%q", n, got)
	}
	if strings.Contains(got, "\r") {
		t.Errorf("carriage returns must not be written to a non-terminal:\n%q", got)
	}
	if strings.Contains(got, "\033") {
		t.Errorf("escape sequences must not be written to a non-terminal:\n%q", got)
	}
	if got != msg+"\n" {
		t.Errorf("got %q, want a single plain line", got)
	}
}

// The stop function must stay safe to call, including more than once.
func TestStartSpinnerStopIsIdempotentWhenRedirected(t *testing.T) {
	got := withCapturedStderr(t, func() {
		stop := startSpinner("Working…")
		stop()
		stop()
	})
	if strings.Count(got, "Working…") != 1 {
		t.Errorf("got %q", got)
	}
}
