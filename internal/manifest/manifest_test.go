package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const exampleManifest = `
modules:
  - path: terraform/cos-lite
    name: "COS Lite"
    description: |
      Production-ready observability stack.
  - path: terraform/cos
    name: "COS"
`

func TestParse_valid(t *testing.T) {
	m, warnings, err := Parse(strings.NewReader(exampleManifest))
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(m.Modules) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(m.Modules))
	}
	cos := m.Modules[0]
	if cos.Path != "terraform/cos-lite" || cos.Name != "COS Lite" {
		t.Errorf("first module: %+v", cos)
	}
	if !strings.Contains(cos.Description, "observability") {
		t.Errorf("description not preserved: %q", cos.Description)
	}

	cos2 := m.Modules[1]
	if cos2.Path != "terraform/cos" || cos2.Name != "COS" {
		t.Errorf("second module: %+v", cos2)
	}
}

func TestParse_unknownTopLevelKey_warns(t *testing.T) {
	const src = `
modules:
  - path: x
    name: X
custom_field: value
`
	_, warnings, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected a warning for the unknown top-level key")
	}
	if !strings.Contains(warnings[0], "custom_field") {
		t.Errorf("warning text: %q", warnings[0])
	}
}

func TestParse_errors(t *testing.T) {
	cases := []struct {
		name, src, wantMsg string
	}{
		{"empty", ``, "empty"},
		{"no modules", `modules: []`, "at least one"},
		{"missing path", "modules:\n  - name: x\n", ".path is required"},
		{"missing name", "modules:\n  - path: x\n", ".name is required"},
		{"duplicate path", "modules:\n  - path: x\n    name: A\n  - path: x\n    name: B\n", "duplicate"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := Parse(strings.NewReader(c.src))
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), c.wantMsg) {
				t.Errorf("error %q; expected to contain %q", err.Error(), c.wantMsg)
			}
		})
	}
}

func TestLoad_missingFile_isOK(t *testing.T) {
	m, warnings, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if m != nil || warnings != nil {
		t.Errorf("expected nil manifest and warnings when file missing, got %+v %+v", m, warnings)
	}
}

func TestLoadFromRepo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "atelier.yaml"), []byte(exampleManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _, err := LoadFromRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m == nil || len(m.Modules) != 2 {
		t.Errorf("loaded: %+v", m)
	}
}

func TestFindModule(t *testing.T) {
	m, _, err := Parse(strings.NewReader(exampleManifest))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.FindModule("terraform/cos-lite"); got == nil || got.Name != "COS Lite" {
		t.Errorf("FindModule cos-lite: %+v", got)
	}
	if got := m.FindModule("does/not/exist"); got != nil {
		t.Errorf("FindModule missing should be nil, got %+v", got)
	}
	var nilM *Manifest
	if got := nilM.FindModule("anything"); got != nil {
		t.Errorf("nil manifest.FindModule should be nil")
	}
}

// --- Preset tests ---

const manifestWithPresets = `
modules:
  - path: terraform/cos-lite
    name: "COS Lite"
    presets:
      - name: "Minimal"
        description: "Single-unit, no TLS."
        sets:
          internal_tls: false
          alertmanager:
            units: 1
      - name: "HA Production"
        description: "Multi-unit with TLS."
        sets:
          internal_tls: true
          alertmanager:
            units: 3
`

func TestParse_presets(t *testing.T) {
	m, warnings, err := Parse(strings.NewReader(manifestWithPresets))
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	mod := m.FindModule("terraform/cos-lite")
	if mod == nil {
		t.Fatal("module not found")
	}
	if len(mod.Presets) != 2 {
		t.Fatalf("expected 2 presets, got %d", len(mod.Presets))
	}
	p := mod.Presets[0]
	if p.Name != "Minimal" {
		t.Errorf("preset[0].Name = %q", p.Name)
	}
	if p.Description != "Single-unit, no TLS." {
		t.Errorf("preset[0].Description = %q", p.Description)
	}
	if len(p.Sets) != 2 {
		t.Errorf("preset[0].Sets = %v", p.Sets)
	}
	// Check nested object value is parsed correctly.
	am, ok := p.Sets["alertmanager"].(map[string]any)
	if !ok {
		t.Fatalf("alertmanager not a map: %T", p.Sets["alertmanager"])
	}
	if am["units"] != 1 {
		t.Errorf("alertmanager.units = %v", am["units"])
	}
}

func TestParse_preset_missingName(t *testing.T) {
	const src = `
modules:
  - path: x
    name: X
    presets:
      - sets:
          foo: bar
`
	_, _, err := Parse(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "presets[0].name") {
		t.Errorf("error: %v", err)
	}
}

func TestParse_preset_emptySets(t *testing.T) {
	const src = `
modules:
  - path: x
    name: X
    presets:
      - name: Empty
        sets: {}
`
	_, _, err := Parse(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "at least one entry in sets") {
		t.Errorf("error: %v", err)
	}
}
