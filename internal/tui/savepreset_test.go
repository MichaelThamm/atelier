package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// saveTestState mirrors sampleVarsForPreset but is defined here to keep this
// test self-contained and to add sensitive/reference-bearing variables.
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

func TestSnapshotValues_capturesNonDefaultOnly(t *testing.T) {
	s := saveTestState(t, t.TempDir())
	s.Values["internal_tls"] = cty.True // non-default
	s.Values["alertmanager"] = cty.ObjectVal(map[string]cty.Value{
		"app_name": cty.StringVal("alertmanager"), // at default
		"units":    cty.NumberIntVal(3),           // non-default
	})
	// labels left at default -> omitted.

	got := snapshotValues(s)
	if len(got) != 2 {
		t.Fatalf("captured %d vars; want 2 (%v)", len(got), got)
	}
	if !got["internal_tls"].True() {
		t.Errorf("internal_tls = %#v; want true", got["internal_tls"])
	}
	am := got["alertmanager"]
	if !am.Type().IsObjectType() {
		t.Fatalf("alertmanager = %#v; want an object", am)
	}
	m := am.AsValueMap()
	if _, hasApp := m["app_name"]; hasApp {
		t.Errorf("app_name is at default and must be omitted: %#v", m)
	}
	if v, ok := m["units"]; !ok || v.AsBigFloat().String() != "3" {
		t.Errorf("alertmanager = %#v; want partial {units:3}", m)
	}
	if _, has := got["labels"]; has {
		t.Error("labels at default must be omitted")
	}
}

func TestSnapshotValues_capturesSensitiveVars(t *testing.T) {
	s := saveTestState(t, t.TempDir())
	s.Values["internal_tls"] = cty.True
	s.Values["password"] = cty.StringVal("hunter2") // sensitive

	got := snapshotValues(s)
	if v, has := got["password"]; !has || v.AsString() != "hunter2" {
		t.Errorf("sensitive variable must be captured; got %#v", got["password"])
	}
	if _, has := got["internal_tls"]; !has {
		t.Error("non-sensitive var should still be captured")
	}
}

func TestSnapshotValues_excludesWiredReferences(t *testing.T) {
	s := saveTestState(t, t.TempDir())
	s.Values["internal_tls"] = cty.True
	// endpoint is wired to an expression preserved verbatim in UnknownAttrs.
	s.UnknownAttrs = []wrapper.RawAttr{{Name: "endpoint", RawExpr: []byte("var.endpoint")}}

	if _, has := snapshotValues(s)["endpoint"]; has {
		t.Error("wired reference expression must not be captured")
	}
}

func TestSnapshotValues_emptyWhenAllDefaults(t *testing.T) {
	s := saveTestState(t, t.TempDir())
	if n := len(snapshotValues(s)); n != 0 {
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

	path := filepath.Join(dir, wrapper.PresetsDir, "ha.tfvars")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("bundle file not written: %v", err)
	}
	if !strings.Contains(string(data), "# x") {
		t.Errorf("description comment missing; got:\n%s", data)
	}
	vals, diags, err := wrapper.ReadTFVarsFileChecked(path, m.State.Vars)
	if err != nil {
		t.Fatalf("written bundle does not parse: %v", err)
	}
	if !diags.Empty() {
		t.Errorf("written bundle has diagnostics: %+v", diags)
	}
	if v, ok := vals["internal_tls"]; !ok || !v.True() {
		t.Errorf("internal_tls not captured in bundle: %#v", vals["internal_tls"])
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
	if entries, err := os.ReadDir(filepath.Join(dir, wrapper.PresetsDir)); err == nil && len(entries) > 0 {
		t.Errorf("Esc must not write a bundle; found %v", entries)
	}
}

func TestSavePreset_refusesWhenFileExists(t *testing.T) {
	dir := t.TempDir()
	presetDir := filepath.Join(dir, wrapper.PresetsDir)
	if err := os.MkdirAll(presetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(presetDir, "ha.tfvars"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := saveModalModel(t, dir)
	m = feed(m, key("s"))           // opens (nothing checked until commit)
	m = feed(m, key("h"), key("a")) // name "ha"
	m = feed(m, key("enter"))
	if !m.savePresetModal {
		t.Error("modal must stay open when the bundle already exists")
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
