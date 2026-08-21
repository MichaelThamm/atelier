package importer

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTF(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A wrapper Atelier authored earlier, or any already-initialised root, declares
// its providers and is invoked without the PROVIDER positional. Reading the
// declarations back is what lets import wire provider support in that mode
// instead of silently importing nothing.
func TestDeclaredProviderSources(t *testing.T) {
	dir := t.TempDir()
	writeTF(t, dir, "versions.tf", `
terraform {
  required_providers {
    juju = {
      source  = "juju/juju"
      version = ">= 1.4.0"
    }
  }
}
`)
	got := DeclaredProviderSources(dir)
	if len(got) != 1 || got[0] != "juju/juju" {
		t.Fatalf("got %v, want [juju/juju]", got)
	}
}

// Sorted output keeps behaviour deterministic when several providers are declared.
func TestDeclaredProviderSourcesSorted(t *testing.T) {
	dir := t.TempDir()
	writeTF(t, dir, "versions.tf", `
terraform {
  required_providers {
    juju = {
      source = "juju/juju"
    }
    null = {
      source = "hashicorp/null"
    }
  }
}
`)
	got := DeclaredProviderSources(dir)
	if len(got) != 2 || got[0] != "hashicorp/null" || got[1] != "juju/juju" {
		t.Fatalf("got %v, want [hashicorp/null juju/juju]", got)
	}
}

func TestDeclaredProviderSourcesEmpty(t *testing.T) {
	if got := DeclaredProviderSources(t.TempDir()); len(got) != 0 {
		t.Errorf("no declarations should yield none, got %v", got)
	}
	if got := DeclaredProviderSources(filepath.Join(t.TempDir(), "missing")); got != nil {
		t.Errorf("unreadable dir should yield nil, got %v", got)
	}
}
