package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

func sampleVarsForPreset(t *testing.T) []tfvars.Variable {
	t.Helper()
	return []tfvars.Variable{
		{
			Name:       "internal_tls",
			Type:       mustParseType(t, "bool"),
			HasDefault: true,
			Default:    cty.False,
		},
		{
			Name:       "alertmanager",
			Type:       mustParseType(t, `object({ app_name = optional(string, "alertmanager"), units = optional(number, 1) })`),
			HasDefault: true,
			Default:    cty.EmptyObjectVal,
		},
		{
			Name:       "labels",
			Type:       mustParseType(t, "map(string)"),
			HasDefault: true,
			Default:    cty.MapValEmpty(cty.String),
		},
	}
}

// --- Preset picker + apply integration tests ---

func presetTestModel(t *testing.T) *Model {
	t.Helper()
	vars := sampleVarsForPreset(t)
	state := &wrapper.State{
		Vars:   vars,
		Values: map[string]cty.Value{},
	}
	m := New(state, "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.SetPresets([]ResolvedPreset{
		{
			Name:        "Minimal",
			Description: "Bare minimum.",
			Values:      map[string]cty.Value{"internal_tls": cty.False},
			Source:      "local",
		},
		{
			Name:        "HA Production",
			Description: "Multi-unit with TLS.",
			Values: map[string]cty.Value{
				"internal_tls": cty.True,
				"alertmanager": cty.ObjectVal(map[string]cty.Value{"units": cty.NumberIntVal(3)}),
			},
			Source: "repo",
		},
	})
	return m
}

func TestPresetPicker_openAndCancel(t *testing.T) {
	m := presetTestModel(t)
	// F opens the picker.
	m = feed(m, key("f"))
	if !m.presetPicker {
		t.Fatal("picker should be open after F")
	}
	// Esc closes it without applying.
	m = feed(m, key("esc"))
	if m.presetPicker {
		t.Fatal("picker should close on Esc")
	}
	// No values should have been set.
	if _, ok := m.State.Values["internal_tls"]; ok {
		t.Error("no values should be set after cancel")
	}
}

func TestPresetPicker_navigateAndApply(t *testing.T) {
	m := presetTestModel(t)
	// Open picker and navigate to second preset.
	m = feed(m, key("f"))
	m = feed(m, key("down"))
	if m.presetCursor != 1 {
		t.Fatalf("cursor = %d; want 1", m.presetCursor)
	}
	// Apply "HA Production".
	m = feed(m, key("enter"))
	if m.presetPicker {
		t.Fatal("picker should close on enter")
	}
	// Verify values were applied.
	v, ok := m.State.Values["internal_tls"]
	if !ok || !v.True() {
		t.Errorf("internal_tls = %v; want true", v.GoString())
	}
	am, ok := m.State.Values["alertmanager"]
	if !ok {
		t.Fatal("alertmanager not set")
	}
	units := am.AsValueMap()["units"]
	if !units.Equals(cty.NumberIntVal(3)).True() {
		t.Errorf("units = %v; want 3", units.GoString())
	}
}

func TestPresetPicker_statusMessage(t *testing.T) {
	m := presetTestModel(t)
	m = feed(m, key("f"), key("enter"))
	if !strings.Contains(m.status, "Minimal") {
		t.Errorf("status = %q; want to contain preset name", m.status)
	}
}

func TestPresetPicker_fDoesNothingWithoutPresets(t *testing.T) {
	state := sampleState(t)
	m := New(state, "test")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = feed(m, key("f"))
	if m.presetPicker {
		t.Error("picker should not open when no presets available")
	}
}

func TestPresetPicker_fDoesNothingInRightPane(t *testing.T) {
	m := presetTestModel(t)
	// Focus right pane.
	m = feed(m, key("tab"))
	m = feed(m, key("f"))
	if m.presetPicker {
		t.Error("picker should not open from right pane")
	}
}

func TestPresetPicker_view(t *testing.T) {
	m := presetTestModel(t)
	m = feed(m, key("f"))
	out := stripANSI(m.View())
	for _, want := range []string{"Minimal", "HA Production", "[local]", "[repo]", "Esc"} {
		if !strings.Contains(out, want) {
			t.Errorf("picker view missing %q; got:\n%s", want, out)
		}
	}
}

func TestApplyPreset_capturesSensitiveValues(t *testing.T) {
	vars := []tfvars.Variable{
		{Name: "endpoint", Type: mustParseType(t, "string"), HasDefault: true, Default: cty.StringVal("")},
		{Name: "password", Type: mustParseType(t, "string"), Sensitive: true},
	}
	presets := []ResolvedPreset{{
		Name: "test",
		Values: map[string]cty.Value{
			"endpoint": cty.StringVal("http://example.com"),
			"password": cty.StringVal("secret123"),
		},
		Source: "repo",
	}}
	state := &wrapper.State{
		Vars:   vars,
		Values: map[string]cty.Value{},
	}
	m := New(state, "test")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.SetPresets(presets)
	m.applyPreset(0)

	// All values go to Values (no SecretValues indirection).
	if m.State.Values["password"].AsString() != "secret123" {
		t.Errorf("Values[password] = %q; want %q", m.State.Values["password"].AsString(), "secret123")
	}
	if m.State.Values["endpoint"].AsString() != "http://example.com" {
		t.Errorf("Values[endpoint] = %q; want %q", m.State.Values["endpoint"].AsString(), "http://example.com")
	}
}
