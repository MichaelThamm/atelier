package candidate

import (
	"os"
	"path/filepath"
	"testing"
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

	got, _, err := Discover(root)
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

	got, _, err := Discover(root)
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

	got, _, err := Discover(root)
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

	got, _, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("unexpected candidates: %+v", got)
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
	got, _, err := Discover(root)
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

func TestDiscover_heuristic_rootNotExcludedByWrapperRef(t *testing.T) {
	// Simulates terraform-aws-modules layout: root has variables, and a
	// wrappers/ subdir references root via source = "../../".
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "variables.tf"), `variable "bucket" { type = string }`)
	wrapper := filepath.Join(root, "wrappers", "s3")
	mkdir(t, wrapper)
	writeFile(t, filepath.Join(wrapper, "main.tf"), `
variable "defaults" { type = any }
variable "items" { type = any }
module "wrapper" {
  source = "../../"
}
`)

	got, _, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	// Root (".") must be among candidates despite being referenced.
	// Wrapper must be excluded because all its variables are type any.
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1 (root only): %+v", len(got), got)
	}
	if got[0].Path != "." {
		t.Errorf("expected root candidate, got %q", got[0].Path)
	}
}

func TestDiscover_heuristic_excludesAllAnyVarsCandidates(t *testing.T) {
	root := t.TempDir()
	// A module with typed variables — should be discovered.
	typed := filepath.Join(root, "typed")
	mkdir(t, typed)
	writeFile(t, filepath.Join(typed, "variables.tf"), `
variable "name" { type = string }
variable "count" { type = number }
`)
	// A module with only any-typed variables — should be excluded.
	anyOnly := filepath.Join(root, "any-only")
	mkdir(t, anyOnly)
	writeFile(t, filepath.Join(anyOnly, "variables.tf"), `
variable "defaults" { type = any }
variable "items" { type = any }
`)
	// A module with no type constraint (defaults to any) — should be excluded.
	noType := filepath.Join(root, "no-type")
	mkdir(t, noType)
	writeFile(t, filepath.Join(noType, "variables.tf"), `
variable "stuff" {}
`)
	// A module with a mix — should be discovered (has at least one typed var).
	mixed := filepath.Join(root, "mixed")
	mkdir(t, mixed)
	writeFile(t, filepath.Join(mixed, "variables.tf"), `
variable "defaults" { type = any }
variable "name" { type = string }
`)

	got, _, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, c := range got {
		paths[c.Path] = true
	}
	if !paths["typed"] {
		t.Error("expected 'typed' to be discovered")
	}
	if !paths["mixed"] {
		t.Error("expected 'mixed' to be discovered")
	}
	if paths["any-only"] {
		t.Error("expected 'any-only' to be excluded (all vars are type any)")
	}
	if paths["no-type"] {
		t.Error("expected 'no-type' to be excluded (no type constraint = any)")
	}
}
