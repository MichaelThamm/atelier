package wrapper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTF(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanDeclarations_findsRequiredProvidersOutsideVersionsTF(t *testing.T) {
	dir := t.TempDir()
	// The collision that filename-based checks miss: required_providers lives
	// in main.tf, not versions.tf.
	writeTF(t, dir, "main.tf", `
terraform {
  required_providers {
    juju = { source = "juju/juju" }
  }
}
`)
	d, err := ScanDeclarations(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !d.HasRequiredProvidersBlock() {
		t.Fatal("expected required_providers block to be detected")
	}
	if got := d.RequiredProvidersFile; got != "main.tf" {
		t.Errorf("RequiredProvidersFile = %q, want main.tf", got)
	}
	if got := d.RequiredProviders["juju"]; got != "main.tf" {
		t.Errorf("RequiredProviders[juju] = %q, want main.tf", got)
	}
}

func TestScanDeclarations_providersAndAliases(t *testing.T) {
	dir := t.TempDir()
	writeTF(t, dir, "infra.tf", `
provider "juju" {}
provider "juju" {
  alias = "secondary"
}
`)
	d, err := ScanDeclarations(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"juju", "juju.secondary"} {
		if _, ok := d.Providers[want]; !ok {
			t.Errorf("expected provider %q to be detected; got %v", want, d.Providers)
		}
	}
}

func TestScanDeclarations_looksHandAuthored(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "atelier wrapper",
			body: `module "cos" { source = "git::https://example.com/m.git?ref=v1" }`,
			want: false,
		},
		{
			name: "resource block",
			body: `resource "null_resource" "x" {}`,
			want: true,
		},
		{
			name: "locals",
			body: "locals {\n  a = 1\n}\n",
			want: true,
		},
		{
			name: "local module source",
			body: `module "m" { source = "./modules/thing" }`,
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTF(t, dir, "main.tf", c.body)
			d, err := ScanDeclarations(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := d.LooksHandAuthored(); got != c.want {
				t.Errorf("LooksHandAuthored() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestScanDeclarations_missingDirIsNotAnError(t *testing.T) {
	d, err := ScanDeclarations(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.HasRequiredProvidersBlock() || len(d.Files) != 0 {
		t.Errorf("expected empty declarations, got %+v", d)
	}
}

func TestScanDeclarations_toleratesUnparseableFile(t *testing.T) {
	dir := t.TempDir()
	writeTF(t, dir, "broken.tf", "this is not { hcl")
	writeTF(t, dir, "good.tf", `provider "juju" {}`)
	d, err := ScanDeclarations(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := d.Providers["juju"]; !ok {
		t.Error("a broken file should not stop the scan of the others")
	}
}

func TestBootstrap_skipsVersionsWhenRequiredProvidersExistsElsewhere(t *testing.T) {
	dir := t.TempDir()
	// A hand-authored root declaring its own required_providers. Writing a
	// second block would make `terraform init` fail outright.
	writeTF(t, dir, "terraform.tf", `
terraform {
  required_providers {
    aws = { source = "hashicorp/aws" }
  }
}
`)
	rep, err := Bootstrap(BootstrapOptions{
		Dir:               dir,
		ModuleBlockName:   "m",
		Source:            "git::https://example.com/m.git?ref=v1",
		RequiredProviders: map[string]RequiredProvider{"juju": {Source: "juju/juju"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, VersionsTF)); !os.IsNotExist(err) {
		t.Error("versions.tf should not be written when required_providers already exists")
	}
	if !containsSubstring(rep.Notes, "terraform.tf") || !containsSubstring(rep.Notes, "juju") {
		t.Errorf("expected a note naming the file and the missing provider; got %v", rep.Notes)
	}
}

func TestBootstrap_skipsAlreadyConfiguredProvider(t *testing.T) {
	dir := t.TempDir()
	writeTF(t, dir, "infra.tf", `provider "juju" {}`)
	rep, err := Bootstrap(BootstrapOptions{
		Dir:             dir,
		ModuleBlockName: "m",
		Source:          "git::https://example.com/m.git?ref=v1",
		Providers: []ProviderBlock{
			{Name: "juju", LocalName: "juju"},
			{Name: "aws", LocalName: "aws"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, ProvidersTF))
	if err != nil {
		t.Fatalf("providers.tf should still be written for the un-declared provider: %v", err)
	}
	if strings.Contains(string(got), `provider "juju"`) {
		t.Errorf("duplicated the user's juju provider block; got:\n%s", got)
	}
	if !strings.Contains(string(got), `provider "aws"`) {
		t.Errorf("expected aws provider block; got:\n%s", got)
	}
	if !containsSubstring(rep.Notes, "juju") {
		t.Errorf("expected a note about the kept provider; got %v", rep.Notes)
	}
}

func TestBootstrap_appendsMissingGitignorePatterns(t *testing.T) {
	dir := t.TempDir()
	// A repository with its own .gitignore that knows nothing about Atelier.
	if err := os.WriteFile(filepath.Join(dir, GitignoreFile), []byte("*.log\n.terraform/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Bootstrap(BootstrapOptions{
		Dir:             dir,
		ModuleBlockName: "m",
		Source:          "git::https://example.com/m.git?ref=v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, GitignoreFile))
	s := string(got)
	if !strings.HasPrefix(s, "*.log\n.terraform/\n") {
		t.Errorf("existing rules were not preserved verbatim; got:\n%s", s)
	}
	if !strings.Contains(s, ".atelier/") {
		t.Errorf("expected .atelier/ to be appended; got:\n%s", s)
	}
	if strings.Count(s, ".terraform/") != 1 {
		t.Errorf("a pattern the user already had was duplicated; got:\n%s", s)
	}
	if !containsSubstring(rep.Notes, GitignoreFile) {
		t.Errorf("expected a note about the .gitignore append; got %v", rep.Notes)
	}
}

func TestBootstrap_gitignoreUntouchedWhenComplete(t *testing.T) {
	dir := t.TempDir()
	full := gitignoreHeader + "\n" + strings.Join(gitignorePatterns, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, GitignoreFile), []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Bootstrap(BootstrapOptions{
		Dir:             dir,
		ModuleBlockName: "m",
		Source:          "git::https://example.com/m.git?ref=v1",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, GitignoreFile))
	if string(got) != full {
		t.Errorf(".gitignore was modified despite already covering everything; got:\n%s", got)
	}
}

func TestBootstrap_reportsCreatedFiles(t *testing.T) {
	dir := t.TempDir()
	rep, err := Bootstrap(BootstrapOptions{
		Dir:               dir,
		ModuleBlockName:   "m",
		Source:            "git::https://example.com/m.git?ref=v1",
		RequiredProviders: map[string]RequiredProvider{"juju": {Source: "juju/juju"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{GitignoreFile, ReadmeFile, VersionsTF, MainTF} {
		if !containsExact(rep.Created, want) {
			t.Errorf("expected %s in Created; got %v", want, rep.Created)
		}
	}
}

func containsSubstring(hay []string, needle string) bool {
	for _, s := range hay {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func containsExact(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
