package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoBasename(t *testing.T) {
	cases := []struct{ in, want string }{
		{"git::https://github.com/canonical/observability-stack.git", "observability-stack"},
		{"https://github.com/canonical/observability-stack.git?ref=main", "observability-stack"},
		{"git@github.com:canonical/observability-stack.git", "observability-stack"},
		{"/home/user/local-thing", "local-thing"},
		{"./relative", "relative"},
		{"https://example.com/", "example.com"}, // last segment if no .git
		{"", "repo"},
	}
	for _, c := range cases {
		if got := repoBasename(c.in); got != c.want {
			t.Errorf("repoBasename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestComposeSource(t *testing.T) {
	cases := []struct {
		remote, modulePath, ref, want string
	}{
		{"https://example.com/m.git", "terraform/cos-lite", "v1.2.0",
			"git::https://example.com/m.git//terraform/cos-lite?ref=v1.2.0"},
		{"git::ssh://git@example.com/m.git", "", "main",
			"git::ssh://git@example.com/m.git?ref=main"},
		{"./local", "modules/x", "",
			"./local//modules/x"},
		{"/abs/path", ".", "",
			"/abs/path"},
	}
	for _, c := range cases {
		got := composeSource(c.remote, c.modulePath, c.ref)
		if got != c.want {
			t.Errorf("composeSource(%q, %q, %q) = %q, want %q", c.remote, c.modulePath, c.ref, got, c.want)
		}
	}
}

func TestDecomposeSource(t *testing.T) {
	cases := []struct {
		in, wantURL, wantRef string
	}{
		{"git::https://example.com/m.git//terraform/x?ref=v1", "https://example.com/m.git", "v1"},
		{"https://example.com/m.git?ref=main", "https://example.com/m.git", "main"},
		{"./local//modules/x", "./local", ""},
		{"git::https://example.com/m.git", "https://example.com/m.git", ""},
	}
	for _, c := range cases {
		gotURL, gotRef := decomposeSource(c.in)
		if gotURL != c.wantURL || gotRef != c.wantRef {
			t.Errorf("decomposeSource(%q) = (%q, %q), want (%q, %q)", c.in, gotURL, gotRef, c.wantURL, c.wantRef)
		}
	}
}

func TestModuleBlockName(t *testing.T) {
	cases := []struct{ in, fallback, want string }{
		{"terraform/cos-lite", "", "cos_lite"},
		{"cos", "", "cos"},
		{"terraform/123numeric", "", "m123numeric"},
		{"weird-name!", "", "weird_name"},
		{".", "", "this"},
		{"", "", "this"},
		{".", "terraform-aws-s3-bucket", "terraform_aws_s3_bucket"},
		{".", "observability-stack", "observability_stack"},
	}
	for _, c := range cases {
		if got := ModuleBlockName(c.in, c.fallback); got != c.want {
			t.Errorf("ModuleBlockName(%q, %q) = %q, want %q", c.in, c.fallback, got, c.want)
		}
	}
}

func TestReadRequiredProviders(t *testing.T) {
	dir := t.TempDir()
	const tf = `
terraform {
  required_providers {
    juju = {
      source  = "juju/juju"
      version = ">= 0.10"
    }
    aws = {
      source = "hashicorp/aws"
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "versions.tf"), []byte(tf), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRequiredProviders(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got["juju"].Source != "juju/juju" || got["juju"].Version != ">= 0.10" {
		t.Errorf("juju: %+v", got["juju"])
	}
	if got["aws"].Source != "hashicorp/aws" || got["aws"].Version != "" {
		t.Errorf("aws: %+v", got["aws"])
	}
}

func TestReadRequiredProviders_emptyDir(t *testing.T) {
	got, err := ReadRequiredProviders(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %+v", got)
	}
}

func TestPrepareState_localModule(t *testing.T) {
	cloneDir := t.TempDir()
	modPath := "terraform/cos-lite"
	candidateDir := filepath.Join(cloneDir, modPath)
	if err := os.MkdirAll(candidateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(candidateDir, "variables.tf"), []byte(`
variable "model_uuid" {
  type = string
}
variable "internal_tls" {
  type    = bool
  default = true
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "versions.tf"), []byte(`
terraform {
  required_providers {
    juju = {
      source  = "juju/juju"
      version = ">= 0.10"
    }
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	wrapperDir := t.TempDir()
	state, err := PrepareState(wrapperDir, cloneDir, modPath, "abc123", "v1.2.0", "https://example.com/m.git")
	if err != nil {
		t.Fatal(err)
	}
	if state.ModuleBlockName != "cos_lite" {
		t.Errorf("ModuleBlockName = %q", state.ModuleBlockName)
	}
	if state.Source != "git::https://example.com/m.git//terraform/cos-lite?ref=v1.2.0" {
		t.Errorf("Source = %q", state.Source)
	}
	if len(state.Vars) != 2 {
		t.Errorf("expected 2 vars, got %d", len(state.Vars))
	}
	if state.RequiredProviders["juju"].Source != "juju/juju" {
		t.Errorf("juju provider not detected: %+v", state.RequiredProviders)
	}
	if len(state.Providers) != 1 || state.Providers[0].Name != "juju" {
		t.Errorf("expected juju provider block, got %+v", state.Providers)
	}
}

func TestCandidatePaths(t *testing.T) {
	// Lightweight check; mainly to ensure the helper compiles cleanly.
	cs := candidatePaths(nil)
	if len(cs) != 0 {
		t.Errorf("nil → non-empty: %v", cs)
	}
}
