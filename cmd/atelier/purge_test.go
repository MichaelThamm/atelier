package main

import (
	"os"
	"path/filepath"
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
