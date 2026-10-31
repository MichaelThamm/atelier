package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPurge_nothingToPurge(t *testing.T) {
	dir := t.TempDir()
	// No .atelier or .clone present.
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(dir)

	err := runPurge([]string{"--force"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// An empty purge is not an error: the message goes to stderr, exit code 0.
func TestRunPurge_nothingToPurgeGoesToStderr(t *testing.T) {
	dir := t.TempDir()
	out, err := captureStdout(t, func() error { return runPurge([]string{dir, "--force"}) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Errorf("nothing-to-purge wrote to stdout: %q", out)
	}
	msg, err := captureStderr(t, func() error { return runPurge([]string{dir, "--force"}) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, "nothing to purge") {
		t.Errorf("nothing-to-purge message not on stderr: %q", msg)
	}
}

func TestRunPurge_removesTargets(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".atelier"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".clone"), 0o755)
	// Put a file inside to ensure recursive removal.
	os.WriteFile(filepath.Join(dir, ".atelier", "state.json"), []byte("{}"), 0o644)

	err := runPurge([]string{dir, "--force"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, name := range []string{".atelier", ".clone"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed, but it still exists", p)
		}
	}
}

func TestRunPurge_partialRemoval(t *testing.T) {
	dir := t.TempDir()
	// Only .clone exists.
	os.MkdirAll(filepath.Join(dir, ".clone"), 0o755)

	err := runPurge([]string{dir, "--force"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p := filepath.Join(dir, ".clone")
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("expected %s to be removed", p)
	}
}

// A removal failure is reported with the standard's "cannot" wording.
func TestRunPurge_reportCannotRemove(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based failure does not apply to root")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, ".atelier")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "x"), []byte("x"), 0o644)
	// A non-writable directory keeps its contents from being removed.
	if err := os.Chmod(sub, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sub, 0o755) })

	err := runPurge([]string{dir, "--force"})
	if err == nil {
		t.Fatal("expected a removal error")
	}
	if !strings.Contains(err.Error(), "cannot remove") {
		t.Errorf("error should use 'cannot remove'; got %v", err)
	}
}

func TestRunPurge_unknownFlag(t *testing.T) {
	err := runPurge([]string{"--recursive"})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestRunPurge_tooManyArgs(t *testing.T) {
	err := runPurge([]string{"/a", "/b"})
	if err == nil {
		t.Fatal("expected error for multiple path args")
	}
}
