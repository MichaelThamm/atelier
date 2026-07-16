package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/manifest"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

func TestCtyToAny_scalars(t *testing.T) {
	cases := []struct {
		name string
		in   cty.Value
		want any
	}{
		{"bool", cty.True, true},
		{"string", cty.StringVal("hi"), "hi"},
		{"int", cty.NumberIntVal(3), int64(3)},
		{"float", cty.NumberFloatVal(1.5), 1.5},
		{"null", cty.NullVal(cty.String), nil},
		{"nilval", cty.NilVal, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ctyToAny(c.in); got != c.want {
				t.Errorf("ctyToAny(%s) = %#v; want %#v", c.name, got, c.want)
			}
		})
	}
}

func TestCtyToAny_collections(t *testing.T) {
	list := ctyToAny(cty.ListVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")}))
	if s, ok := list.([]any); !ok || len(s) != 2 || s[0] != "a" || s[1] != "b" {
		t.Errorf("list = %#v", list)
	}

	// A partial object (only the differing field), like SparseValue produces.
	obj := ctyToAny(cty.ObjectVal(map[string]cty.Value{"units": cty.NumberIntVal(3)}))
	m, ok := obj.(map[string]any)
	if !ok || len(m) != 1 || m["units"] != int64(3) {
		t.Errorf("object = %#v", obj)
	}
}

// saveVars mirrors sampleVarsForPreset but is defined here to keep this test
// self-contained and to add sensitive/reference-bearing variables.
func saveTestState(t *testing.T, dir string) *wrapper.State {
	t.Helper()
	return &wrapper.State{
		Dir: dir,
		Vars: []tfvars.Variable{
			{Name: "internal_tls", Type: mustParseType(t, "bool"), HasDefault: true, Default: cty.False},
			{Name: "alertmanager", Type: mustParseType(t, `object({ app_name = optional(string, "alertmanager"), units = optional(number, 1) })`), HasDefault: true, Default: cty.EmptyObjectVal},
			{Name: "labels", Type: mustParseType(t, "map(string)"), HasDefault: true, Default: cty.MapValEmpty(cty.String)},
			{Name: "password", Type: mustParseType(t, "string"), Sensitive: true},
			{Name: "endpoint", Type: mustParseType(t, "string"), HasDefault: true, Default: cty.StringVal("")},
		},
		Values: map[string]cty.Value{},
	}
}

func TestSnapshotPreset_capturesNonDefaultOnly(t *testing.T) {
	s := saveTestState(t, t.TempDir())
	s.Values["internal_tls"] = cty.True // non-default
	s.Values["alertmanager"] = cty.ObjectVal(map[string]cty.Value{
		"app_name": cty.StringVal("alertmanager"), // at default
		"units":    cty.NumberIntVal(3),           // non-default
	})
	// labels left at default -> omitted.

	p, n := snapshotPreset(s, "minimal", "desc")
	if p.Name != "minimal" || p.Description != "desc" {
		t.Errorf("name/desc = %q/%q", p.Name, p.Description)
	}
	if n != 2 {
		t.Fatalf("captured %d vars; want 2 (%v)", n, p.Sets)
	}
	if p.Sets["internal_tls"] != true {
		t.Errorf("internal_tls = %#v; want true", p.Sets["internal_tls"])
	}
	am, ok := p.Sets["alertmanager"].(map[string]any)
	if !ok || am["units"] != int64(3) {
		t.Errorf("alertmanager = %#v; want partial {units:3}", p.Sets["alertmanager"])
	}
	if _, hasApp := am["app_name"]; hasApp {
		t.Errorf("app_name is at default and must be omitted: %#v", am)
	}
	if _, has := p.Sets["labels"]; has {
		t.Error("labels at default must be omitted")
	}
}

func TestSnapshotPreset_excludesSecrets(t *testing.T) {
	s := saveTestState(t, t.TempDir())
	s.Values["internal_tls"] = cty.True
	s.Values["password"] = cty.StringVal("hunter2") // sensitive

	p, _ := snapshotPreset(s, "x", "")
	if _, has := p.Sets["password"]; has {
		t.Error("sensitive variable must never be serialised into a preset")
	}
	if _, has := p.Sets["internal_tls"]; !has {
		t.Error("non-sensitive var should still be captured")
	}
}

func TestSnapshotPreset_excludesWiredReferences(t *testing.T) {
	s := saveTestState(t, t.TempDir())
	s.Values["internal_tls"] = cty.True
	// endpoint is wired to an expression preserved verbatim in UnknownAttrs.
	s.UnknownAttrs = []wrapper.RawAttr{{Name: "endpoint", RawExpr: []byte("var.endpoint")}}

	p, _ := snapshotPreset(s, "x", "")
	if _, has := p.Sets["endpoint"]; has {
		t.Error("wired reference expression must not be captured")
	}
}

func TestSnapshotPreset_emptyWhenAllDefaults(t *testing.T) {
	s := saveTestState(t, t.TempDir())
	if _, n := snapshotPreset(s, "x", ""); n != 0 {
		t.Errorf("captured %d vars; want 0 for an all-default config", n)
	}
}

// --- Modal flow ---

func saveModalModel(t *testing.T, dir string) *Model {
	t.Helper()
	s := saveTestState(t, dir)
	s.Values["internal_tls"] = cty.True // ensure something to save
	m := New(s, "cos")
	return feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
}

func TestSavePreset_openTypeAndWrite(t *testing.T) {
	dir := t.TempDir()
	m := saveModalModel(t, dir)

	m = feed(m, key("s"))
	if !m.savePresetModal {
		t.Fatal("S should open the save-preset modal")
	}
	// Type a name.
	m = feed(m, key("h"), key("a"))
	// Tab to description, type one.
	m = feed(m, key("tab"))
	if m.savePresetFocus != 1 {
		t.Fatalf("focus = %d; want description (1)", m.savePresetFocus)
	}
	m = feed(m, key("x"))
	// Enter saves.
	m = feed(m, key("enter"))
	if m.savePresetModal {
		t.Fatal("modal should close after save")
	}
	if !strings.Contains(m.status, "Saved preset") {
		t.Errorf("status = %q; want save confirmation", m.status)
	}

	data, err := os.ReadFile(filepath.Join(dir, manifest.LocalFileName))
	if err != nil {
		t.Fatalf("preset file not written: %v", err)
	}
	got, _, err := manifest.Parse(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("written file does not parse: %v", err)
	}
	presets := got.Modules[0].Presets
	if len(presets) != 1 || presets[0].Name != "ha" || presets[0].Description != "x" {
		t.Errorf("written preset = %+v", presets)
	}
}

func TestSavePreset_blankNameKeepsModalOpen(t *testing.T) {
	m := saveModalModel(t, t.TempDir())
	m = feed(m, key("s"))
	m = feed(m, key("enter")) // no name typed
	if !m.savePresetModal {
		t.Error("modal should stay open when name is blank")
	}
}

func TestSavePreset_escCancels(t *testing.T) {
	dir := t.TempDir()
	m := saveModalModel(t, dir)
	m = feed(m, key("s"))
	m = feed(m, key("esc"))
	if m.savePresetModal {
		t.Error("Esc should close the modal")
	}
	if manifest.HasLocalFile(dir) {
		t.Error("Esc must not write a file")
	}
}

func TestSavePreset_refusesWhenFileExists(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, manifest.LocalFileName), []byte("modules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := saveModalModel(t, dir)
	m = feed(m, key("s"))
	if m.savePresetModal {
		t.Error("modal must not open when atelier.local.yaml already exists")
	}
	if !strings.Contains(m.status, "already exists") {
		t.Errorf("status = %q; want an 'already exists' hint", m.status)
	}
}

func TestSavePreset_refusesWhenAllDefaults(t *testing.T) {
	dir := t.TempDir()
	s := saveTestState(t, dir) // no values set
	m := New(s, "cos")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	m = feed(m, key("s"))
	if m.savePresetModal {
		t.Error("modal must not open when there is nothing to save")
	}
	if !strings.Contains(m.status, "nothing to save") {
		t.Errorf("status = %q; want 'nothing to save'", m.status)
	}
}

func TestSavePreset_sDoesNothingInRightPane(t *testing.T) {
	m := saveModalModel(t, t.TempDir())
	m = feed(m, key("tab")) // focus right pane
	m = feed(m, key("s"))
	if m.savePresetModal {
		t.Error("S should not open the modal from the right pane")
	}
}
