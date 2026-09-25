package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tui"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPresetsFromBundles_discoversLocalAndRepo pins the TUI repoint (ADR-0032):
// the picker is fed by `.tfvars` bundles from a personal walk-up
// atelier.presets/ directory and from the module repo's examples, read against
// the module schema.
func TestPresetsFromBundles_discoversLocalAndRepo(t *testing.T) {
	base := t.TempDir()
	wrap := filepath.Join(base, "cos")
	writeTestFile(t, filepath.Join(base, "atelier.presets", "cos-s3.tfvars"),
		"# Shared S3\ns3_endpoint = \"http://local:8333\"\n")

	clone := t.TempDir()
	writeTestFile(t, filepath.Join(clone, "terraform", "cos", "examples", "units.tfvars"), "units = 1\n")

	state := seedState(t,
		mustVarT(t, "s3_endpoint", "string", cty.NilVal, false),
		mustVarT(t, "units", "number", cty.NumberIntVal(3), true),
	)

	got := presetsFromBundles(state, wrap, clone, "terraform/cos")
	byName := map[string]tui.ResolvedPreset{}
	for _, p := range got {
		byName[p.Name] = p
	}

	p, ok := byName["cos-s3"]
	if !ok {
		t.Fatalf("cos-s3 bundle not discovered: %+v", got)
	}
	if p.Source != "local" {
		t.Errorf("cos-s3 source = %q, want local", p.Source)
	}
	if v, ok := p.Values["s3_endpoint"]; !ok || v.AsString() != "http://local:8333" {
		t.Errorf("cos-s3 value not read: %#v", p.Values["s3_endpoint"])
	}
	if p.Description != "Shared S3" {
		t.Errorf("description = %q, want %q", p.Description, "Shared S3")
	}

	if u, ok := byName["units"]; !ok || u.Source != "repo" {
		t.Errorf("units bundle not discovered as repo: %+v", got)
	}
}
