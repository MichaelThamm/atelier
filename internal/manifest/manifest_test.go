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
    presets:
      - name: "Minimal"
        sets:
          internal_tls: false
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
		{"duplicate path", "modules:\n  - path: x\n  - path: x\n", "duplicate"},
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

func writeLocal(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, LocalFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func presetNames(ps []Preset) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func TestLoadLocalPresets_walksUp(t *testing.T) {
	root := t.TempDir()
	// Shared file at a parent dir, using "." to target the primary module.
	writeLocal(t, root, `
modules:
  - path: "."
    presets:
      - name: shared
        sets:
          internal_tls: false
`)
	wrapper := filepath.Join(root, "tf-testing", "cos")
	if err := os.MkdirAll(wrapper, 0o755); err != nil {
		t.Fatal(err)
	}

	got, warns := LoadLocalPresets(wrapper, "terraform/cos")
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	if len(got) != 1 || got[0].Name != "shared" {
		t.Fatalf("presets = %v, want [shared]", presetNames(got))
	}
}

func TestLoadLocalPresets_nearestWinsOnNameClash(t *testing.T) {
	root := t.TempDir()
	writeLocal(t, root, `
modules:
  - path: "."
    presets:
      - name: dev
        sets: { internal_tls: false }
      - name: parent-only
        sets: { internal_tls: false }
`)
	wrapper := filepath.Join(root, "child")
	writeLocal(t, wrapper, `
modules:
  - path: "."
    presets:
      - name: dev
        description: nearer
        sets: { internal_tls: true }
`)

	got, _ := LoadLocalPresets(wrapper, "terraform/cos")
	var dev *Preset
	for i := range got {
		if got[i].Name == "dev" {
			dev = &got[i]
		}
	}
	if dev == nil {
		t.Fatalf("dev preset missing: %v", presetNames(got))
	}
	if dev.Description != "nearer" {
		t.Errorf("nearer file should win: desc = %q", dev.Description)
	}
	// The parent-only preset still shows up (union).
	found := false
	for _, p := range got {
		if p.Name == "parent-only" {
			found = true
		}
	}
	if !found {
		t.Errorf("parent-only preset should be included: %v", presetNames(got))
	}
}

func TestLoadLocalPresets_exactPathBeatsPrimary(t *testing.T) {
	root := t.TempDir()
	writeLocal(t, root, `
modules:
  - path: "."
    presets:
      - name: from-primary
        sets: { internal_tls: false }
  - path: terraform/cos
    presets:
      - name: from-exact
        sets: { internal_tls: true }
`)

	got, _ := LoadLocalPresets(root, "terraform/cos")
	if len(got) != 1 || got[0].Name != "from-exact" {
		t.Fatalf("exact path should win: %v", presetNames(got))
	}
}

func TestLoadLocalPresets_none(t *testing.T) {
	got, warns := LoadLocalPresets(t.TempDir(), "terraform/cos")
	if len(got) != 0 || len(warns) != 0 {
		t.Errorf("expected no presets/warnings, got %v / %v", presetNames(got), warns)
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
	if len(m.Modules) == 0 {
		t.Fatal("no modules parsed")
	}
	mod := m.Modules[0]
	if mod.Path != "terraform/cos-lite" {
		t.Fatalf("unexpected first module: %+v", mod)
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
