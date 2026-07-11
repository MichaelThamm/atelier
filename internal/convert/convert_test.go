package convert

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListTFFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.tf"), []byte("# main"), 0o644)
	os.WriteFile(filepath.Join(dir, "variables.tf"), []byte("# vars"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# readme"), 0o644)

	files, err := listTFFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 .tf files, got %d: %v", len(files), files)
	}
}

func TestShouldRelocateFile(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"main.tf", true},
		{"variables.tf", true},
		{"outputs.tf", true},
		{"terraform.tfvars", true},
		{"prod.tfvars", true},
		{"values.tfvars.json", true},
		{".terraform.lock.hcl", true},
		{"README.md", false},
		{".gitignore", false},
		{"terraform.tfstate", false},
		{"terraform.tfstate.backup", false},
	}
	for _, c := range cases {
		if got := shouldRelocateFile(c.name); got != c.want {
			t.Errorf("shouldRelocateFile(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRelocateFiles(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(src, "module")

	// Create test files.
	os.WriteFile(filepath.Join(src, "main.tf"), []byte(`resource "null_resource" "x" {}`), 0o644)
	os.WriteFile(filepath.Join(src, "variables.tf"), []byte(`variable "name" {}`), 0o644)
	os.WriteFile(filepath.Join(src, "README.md"), []byte("# hi"), 0o644)
	os.WriteFile(filepath.Join(src, "terraform.tfstate"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(src, ".terraform.lock.hcl"), []byte("lock"), 0o644)
	os.MkdirAll(filepath.Join(src, ".terraform"), 0o755)
	os.MkdirAll(filepath.Join(src, "templates"), 0o755)
	os.WriteFile(filepath.Join(src, "templates", "config.tpl"), []byte("tpl"), 0o644)

	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}

	moved, err := relocateFiles(src, dst)
	if err != nil {
		t.Fatal(err)
	}

	// Check moved files.
	if len(moved) < 3 {
		t.Fatalf("expected at least 3 moved items, got %d: %v", len(moved), moved)
	}

	// Verify .tf files moved.
	if _, err := os.Stat(filepath.Join(dst, "main.tf")); err != nil {
		t.Error("main.tf not moved to module/")
	}
	if _, err := os.Stat(filepath.Join(dst, "variables.tf")); err != nil {
		t.Error("variables.tf not moved to module/")
	}
	if _, err := os.Stat(filepath.Join(dst, ".terraform.lock.hcl")); err != nil {
		t.Error(".terraform.lock.hcl not moved to module/")
	}

	// Verify templates/ subdir moved.
	if _, err := os.Stat(filepath.Join(dst, "templates", "config.tpl")); err != nil {
		t.Error("templates/config.tpl not moved to module/")
	}

	// Verify state NOT moved.
	if _, err := os.Stat(filepath.Join(src, "terraform.tfstate")); err != nil {
		t.Error("terraform.tfstate should NOT be moved")
	}

	// Verify README NOT moved.
	if _, err := os.Stat(filepath.Join(src, "README.md")); err != nil {
		t.Error("README.md should NOT be moved")
	}

	// Verify .terraform NOT moved.
	if _, err := os.Stat(filepath.Join(src, ".terraform")); err != nil {
		t.Error(".terraform/ should NOT be moved")
	}
}

func TestFindGitModuleBlock(t *testing.T) {
	dir := t.TempDir()
	tf := `
terraform {
  required_providers {
    juju = { source = "juju/juju" }
  }
}

resource "juju_model" "cos" {
  name = "cos-lite"
}

module "cos" {
  source       = "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=main"
  risk         = "edge"
  internal_tls = false
}
`
	os.WriteFile(filepath.Join(dir, "main.tf"), []byte(tf), 0o644)

	info := findGitModuleBlock(dir)
	if info == nil {
		t.Fatal("expected to find a git module block")
	}
	if info.BlockName != "cos" {
		t.Errorf("BlockName = %q, want %q", info.BlockName, "cos")
	}
	if info.SourceURL != "https://github.com/canonical/observability-stack.git" {
		t.Errorf("SourceURL = %q", info.SourceURL)
	}
	if info.Ref != "main" {
		t.Errorf("Ref = %q, want %q", info.Ref, "main")
	}
	if info.ModulePath != "terraform/cos-lite" {
		t.Errorf("ModulePath = %q, want %q", info.ModulePath, "terraform/cos-lite")
	}
}

func TestFindGitModuleBlock_localSource(t *testing.T) {
	dir := t.TempDir()
	tf := `module "local" { source = "./modules/vpc" }`
	os.WriteFile(filepath.Join(dir, "main.tf"), []byte(tf), 0o644)

	info := findGitModuleBlock(dir)
	if info != nil {
		t.Errorf("expected nil for local source, got %+v", info)
	}
}

func TestFindGitModuleBlock_noModule(t *testing.T) {
	dir := t.TempDir()
	tf := `resource "null_resource" "x" {}`
	os.WriteFile(filepath.Join(dir, "main.tf"), []byte(tf), 0o644)

	info := findGitModuleBlock(dir)
	if info != nil {
		t.Errorf("expected nil for no module block, got %+v", info)
	}
}

func TestIsGitSource(t *testing.T) {
	cases := []struct {
		src  string
		want bool
	}{
		{"git::https://github.com/org/repo.git//path?ref=v1", true},
		{"https://github.com/org/repo.git//path", true},
		{"github.com/org/repo//path", true},
		{"./modules/vpc", false},
		{"../shared/network", false},
		{"/absolute/path", false},
		{"hashicorp/consul/aws", false}, // registry source
	}
	for _, c := range cases {
		if got := isGitSource(c.src); got != c.want {
			t.Errorf("isGitSource(%q) = %v, want %v", c.src, got, c.want)
		}
	}
}

func TestDecomposeSource(t *testing.T) {
	cases := []struct {
		src     string
		wantURL string
		wantRef string
	}{
		{
			"git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=main",
			"https://github.com/canonical/observability-stack.git",
			"main",
		},
		{
			"git::https://github.com/org/repo.git?ref=v1.0",
			"https://github.com/org/repo.git",
			"v1.0",
		},
		{
			"https://github.com/org/repo.git//path",
			"https://github.com/org/repo.git",
			"",
		},
	}
	for _, c := range cases {
		url, ref := decomposeSource(c.src)
		if url != c.wantURL || ref != c.wantRef {
			t.Errorf("decomposeSource(%q) = (%q, %q), want (%q, %q)",
				c.src, url, ref, c.wantURL, c.wantRef)
		}
	}
}

func TestExtractModulePath(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"git::https://github.com/org/repo.git//terraform/cos-lite?ref=main", "terraform/cos-lite"},
		{"git::https://github.com/org/repo.git?ref=v1", ""},
		{"https://github.com/org/repo.git//subdir", "subdir"},
	}
	for _, c := range cases {
		if got := extractModulePath(c.src); got != c.want {
			t.Errorf("extractModulePath(%q) = %q, want %q", c.src, got, c.want)
		}
	}
}

func TestConvertRefusesExistingAtelierDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.tf"), []byte("# test"), 0o644)
	os.MkdirAll(filepath.Join(dir, ".atelier"), 0o755)

	_, err := Run(nil, Options{Dir: dir})
	if err == nil {
		t.Fatal("expected error when .atelier/ exists")
	}
}

func TestConvertRefusesEmptyDir(t *testing.T) {
	dir := t.TempDir()

	_, err := Run(nil, Options{Dir: dir})
	if err == nil {
		t.Fatal("expected error when no .tf files")
	}
}
