package main

import (
	"os"
	"path/filepath"
	"testing"
)

// --- parseModuleAddArgs: --preset ---

func TestParseModuleAddArgs_PresetSeparateArg(t *testing.T) {
	opts, err := parseModuleAddArgs([]string{"https://example.com/repo.git", "--preset", "cos-s3"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(opts.Presets) != 1 || opts.Presets[0] != "cos-s3" {
		t.Errorf("Presets = %v, want [cos-s3]", opts.Presets)
	}
}

func TestParseModuleAddArgs_PresetEqualsArg(t *testing.T) {
	opts, err := parseModuleAddArgs([]string{"https://example.com/repo.git", "--preset=docs/examples/cos-s3.yaml"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(opts.Presets) != 1 || opts.Presets[0] != "docs/examples/cos-s3.yaml" {
		t.Errorf("Presets = %v, want [docs/examples/cos-s3.yaml]", opts.Presets)
	}
}

func TestParseModuleAddArgs_PresetRepeatable(t *testing.T) {
	opts, err := parseModuleAddArgs([]string{
		"https://example.com/repo.git",
		"--preset", "first",
		"--preset=second.yaml",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(opts.Presets) != 2 || opts.Presets[0] != "first" || opts.Presets[1] != "second.yaml" {
		t.Errorf("Presets = %v, want [first second.yaml]", opts.Presets)
	}
}

func TestParseModuleAddArgs_PresetMissingValue(t *testing.T) {
	if _, err := parseModuleAddArgs([]string{"https://example.com/repo.git", "--preset"}); err == nil {
		t.Error("expected error for --preset with no value")
	}
}

// --- presetFileToPresets ---

func writeTempYAML(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestPresetFileToPresets_FlatMap(t *testing.T) {
	path := writeTempYAML(t, "cos-s3.yaml", `
channel: dev/edge
model_uuid: 00000000-0000-0000-0000-000000000001
s3_endpoint: http://10.0.0.1:8080
`)
	presets, err := presetFileToPresets(path)
	if err != nil {
		t.Fatalf("presetFileToPresets: %v", err)
	}
	if len(presets) != 1 {
		t.Fatalf("got %d presets, want 1", len(presets))
	}
	if presets[0].Name != "cos-s3" {
		t.Errorf("name = %q, want cos-s3", presets[0].Name)
	}
	if got := presets[0].Sets["model_uuid"]; got != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("model_uuid = %v", got)
	}
	if len(presets[0].Sets) != 3 {
		t.Errorf("got %d sets, want 3", len(presets[0].Sets))
	}
}

func TestPresetFileToPresets_WrappedSets(t *testing.T) {
	path := writeTempYAML(t, "wrapped.yaml", `
name: wrapped
sets:
  channel: dev/edge
  s3_endpoint: http://10.0.0.1:8080
`)
	presets, err := presetFileToPresets(path)
	if err != nil {
		t.Fatalf("presetFileToPresets: %v", err)
	}
	if len(presets) != 1 {
		t.Fatalf("got %d presets, want 1", len(presets))
	}
	// `name` at the top level is metadata, not a variable, once sets: is used.
	if _, ok := presets[0].Sets["name"]; ok {
		t.Errorf("top-level name leaked into sets: %v", presets[0].Sets)
	}
	if len(presets[0].Sets) != 2 {
		t.Errorf("got %d sets, want 2", len(presets[0].Sets))
	}
}

func TestPresetFileToPresets_ManifestSchema(t *testing.T) {
	path := writeTempYAML(t, "local.yaml", `
modules:
  - path: "."
    presets:
      - name: cos-s3
        sets:
          channel: dev/edge
          s3_endpoint: http://10.0.0.1:8080
`)
	presets, err := presetFileToPresets(path)
	if err != nil {
		t.Fatalf("presetFileToPresets: %v", err)
	}
	if len(presets) != 1 || presets[0].Name != "cos-s3" {
		t.Fatalf("got %#v, want one preset named cos-s3", presets)
	}
}

func TestPresetFileToPresets_Empty(t *testing.T) {
	path := writeTempYAML(t, "empty.yaml", "\n")
	if _, err := presetFileToPresets(path); err == nil {
		t.Error("expected error for empty preset file")
	}
}
