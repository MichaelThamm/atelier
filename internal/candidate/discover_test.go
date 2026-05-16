package candidate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/atelier/internal/manifest"
)

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover_heuristic_singleModule(t *testing.T) {
	root := t.TempDir()
	mod := filepath.Join(root, "terraform", "cos-lite")
	mkdir(t, mod)
	writeFile(t, filepath.Join(mod, "variables.tf"), `variable "x" { type = string }`)

	got, _, err := Discover(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(got), got)
	}
	if got[0].Path != "terraform/cos-lite" {
		t.Errorf("path = %q", got[0].Path)
	}
}

func TestDiscover_heuristic_excludesTestsAndExamples(t *testing.T) {
	root := t.TempDir()
	for _, sub := range []string{"terraform/cos-lite", "tests", "examples", "terraform/cos-lite/tests"} {
		mkdir(t, filepath.Join(root, sub))
		writeFile(t, filepath.Join(root, sub, "main.tf"), `variable "x" { type = string }`)
	}

	got, _, err := Discover(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "terraform/cos-lite" {
		t.Errorf("got %+v, want only terraform/cos-lite", got)
	}
}

func TestDiscover_heuristic_excludesChildModuleReferences(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	child := filepath.Join(parent, "modules", "child")
	mkdir(t, parent)
	mkdir(t, child)
	writeFile(t, filepath.Join(parent, "main.tf"), `
variable "x" { type = string }
module "child" {
  source = "./modules/child"
}
`)
	writeFile(t, filepath.Join(child, "variables.tf"), `variable "y" { type = string }`)

	got, _, err := Discover(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "parent" {
		t.Errorf("got %+v, want only parent (child is referenced)", got)
	}
}

func TestDiscover_heuristic_dirsWithoutVariableBlocksSkipped(t *testing.T) {
	root := t.TempDir()
	mod := filepath.Join(root, "thing")
	mkdir(t, mod)
	writeFile(t, filepath.Join(mod, "outputs.tf"), `output "x" { value = "y" }`)

	got, _, err := Discover(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("unexpected candidates: %+v", got)
	}
}

func TestDiscover_manifest_overridesHeuristic(t *testing.T) {
	root := t.TempDir()

	// Make three dirs that *would* be heuristic candidates...
	for _, p := range []string{"a", "b", "c"} {
		mkdir(t, filepath.Join(root, p))
		writeFile(t, filepath.Join(root, p, "vars.tf"), `variable "x" { type = string }`)
	}
	// ... but the manifest declares only a and b.
	m := &manifest.Manifest{
		Modules: []manifest.Module{
			{Path: "a", Name: "AAA"},
			{Path: "b", Name: "BBB", Description: "the B one"},
		},
	}
	got, warnings, err := Discover(root, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Name != "AAA" || got[1].Name != "BBB" {
		t.Errorf("manifest names not applied: %+v", got)
	}
	if got[1].Description != "the B one" {
		t.Errorf("manifest description not applied: %+v", got[1])
	}
}

func TestDiscover_manifest_missingDirectory_warns(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "real"))
	writeFile(t, filepath.Join(root, "real", "vars.tf"), `variable "x" { type = string }`)

	m := &manifest.Manifest{
		Modules: []manifest.Module{
			{Path: "real", Name: "Real"},
			{Path: "phantom", Name: "Phantom"},
		},
	}
	got, warnings, err := Discover(root, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("got %d candidates, want 1: %+v", len(got), got)
	}
	if len(warnings) != 1 {
		t.Errorf("expected one warning about phantom, got %v", warnings)
	}
}

func TestDiscover_readmeFirstParagraph(t *testing.T) {
	root := t.TempDir()
	mod := filepath.Join(root, "thing")
	mkdir(t, mod)
	writeFile(t, filepath.Join(mod, "vars.tf"), `variable "x" { type = string }`)
	writeFile(t, filepath.Join(mod, "README.md"), `# thing

This module wires up thing.

It also handles thong, which is unrelated.
`)
	got, _, err := Discover(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Description != "This module wires up thing." {
		t.Errorf("description = %q", got[0].Description)
	}
}

func TestFirstParagraph(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"# Title\n\nFirst para.\n\nSecond para.\n", "First para."},
		{"Just one line.", "Just one line."},
		{"# Only headings\n\n", ""},
		{"# Title\nFirst line\nSecond line\n\nThird.", "First line Second line"},
	}
	for _, c := range cases {
		if got := firstParagraph(c.in); got != c.want {
			t.Errorf("firstParagraph(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
