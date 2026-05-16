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
    groups:
      - name: "TLS"
        variables: [internal_tls, external_certificates_offer_url]
      - name: "Applications"
        variables: [alertmanager, grafana]
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
	if len(cos.Groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(cos.Groups))
	}
	if cos.Groups[0].Name != "TLS" {
		t.Errorf("group order: %v", cos.Groups[0].Name)
	}

	cos2 := m.Modules[1]
	if cos2.Path != "terraform/cos" || cos2.Name != "COS" {
		t.Errorf("second module: %+v", cos2)
	}
	if len(cos2.Groups) != 0 {
		t.Errorf("second module should have no groups, got %d", len(cos2.Groups))
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
		{"empty group name", "modules:\n  - path: x\n    name: A\n    groups:\n      - variables: [a]\n", "groups[0].name"},
		{"empty group variables", "modules:\n  - path: x\n    name: A\n    groups:\n      - name: G\n        variables: []\n", "needs at least one variable"},
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

func TestApplyGroups_withManifest(t *testing.T) {
	m, _, err := Parse(strings.NewReader(exampleManifest))
	if err != nil {
		t.Fatal(err)
	}
	mod := m.FindModule("terraform/cos-lite")
	// Vars present: TLS pair fully, half of Applications, and one not in the manifest.
	vars := []string{
		"internal_tls",
		"external_certificates_offer_url",
		"alertmanager",
		"loki",          // not in manifest groups
		"orphan",        // not in manifest
	}
	groups := ApplyGroups(mod, vars)
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups (TLS, Applications, Other), got %d: %+v", len(groups), groups)
	}
	if groups[0].Name != "TLS" || len(groups[0].Variables) != 2 {
		t.Errorf("TLS group: %+v", groups[0])
	}
	if groups[1].Name != "Applications" || len(groups[1].Variables) != 1 || groups[1].Variables[0] != "alertmanager" {
		t.Errorf("Applications group: %+v", groups[1])
	}
	if groups[2].Name != "Other" || len(groups[2].Variables) != 2 {
		t.Errorf("Other group: %+v", groups[2])
	}
}

func TestApplyGroups_noManifest(t *testing.T) {
	vars := []string{"a", "b", "c"}
	groups := ApplyGroups(nil, vars)
	if len(groups) != 1 || groups[0].Name != "" {
		t.Errorf("nil-manifest groups: %+v", groups)
	}
	if len(groups[0].Variables) != 3 {
		t.Errorf("variables: %+v", groups[0].Variables)
	}
}
