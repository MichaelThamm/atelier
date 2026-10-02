package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func TestFindWrappers(t *testing.T) {
	parent := t.TempDir()

	// A full wrapper: two module blocks and .atelier/.
	full := filepath.Join(parent, "cos-lite")
	mustMkdirAll(t, filepath.Join(full, ".atelier"))
	mustWriteFile(t, filepath.Join(full, "main.tf"), `
module "cos_lite" {
  source = "git::https://example.com/cos-lite.git"
}

module "alerting" {
  source = "git::https://example.com/alerting.git"
}
`)

	// A wrapper whose .atelier/ was deleted: main.tf only, still discovered.
	rehydratable := filepath.Join(parent, "nori")
	mustMkdirAll(t, rehydratable)
	mustWriteFile(t, filepath.Join(rehydratable, "main.tf"),
		`module "nori" { source = "git::https://example.com/nori.git" }`)

	// .atelier/ but no main.tf: broken, but still a wrapper to discover.
	mustMkdirAll(t, filepath.Join(parent, "orphan", ".atelier"))

	// Not wrappers: a plain directory, a hidden directory, and a file.
	mustMkdirAll(t, filepath.Join(parent, "notes"))
	mustMkdirAll(t, filepath.Join(parent, ".hidden"))
	mustWriteFile(t, filepath.Join(parent, "README.md"), "hi")

	got, err := findWrappers(parent)
	if err != nil {
		t.Fatalf("findWrappers: %v", err)
	}
	want := []childWrapper{
		{Name: "cos-lite", Modules: []string{"alerting", "cos_lite"}},
		{Name: "nori", Modules: []string{"nori"}},
		{Name: "orphan"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findWrappers = %#v, want %#v", got, want)
	}
}

func TestFindWrappers_empty(t *testing.T) {
	got, err := findWrappers(t.TempDir())
	if err != nil {
		t.Fatalf("findWrappers: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("findWrappers on an empty dir = %#v, want none", got)
	}
}

func TestRunWrappers_unknownFlag(t *testing.T) {
	if err := runWrappers([]string{"--recursive"}); err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestRunWrappers_tooManyArgs(t *testing.T) {
	if err := runWrappers([]string{"/a", "/b"}); err == nil {
		t.Fatal("expected error for multiple path args")
	}
}

func TestRunWrappers_noWrappers(t *testing.T) {
	if err := runWrappers([]string{t.TempDir()}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
