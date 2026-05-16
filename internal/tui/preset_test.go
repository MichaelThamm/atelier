package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/canonical/atelier/internal/manifest"
	"github.com/canonical/atelier/internal/tftypes"
	"github.com/canonical/atelier/internal/tfvars"
	"github.com/canonical/atelier/internal/wrapper"
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

func TestResolvePresets_scalar(t *testing.T) {
	presets := []manifest.Preset{
		{
			Name:        "HA",
			Description: "High availability",
			Sets: map[string]any{
				"internal_tls": true,
			},
		},
	}
	vars := sampleVarsForPreset(t)
	resolved := ResolvePresets(presets, vars)
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved preset, got %d", len(resolved))
	}
	rp := resolved[0]
	if rp.Name != "HA" {
		t.Errorf("name = %q", rp.Name)
	}
	v, ok := rp.Values["internal_tls"]
	if !ok {
		t.Fatal("internal_tls not in resolved values")
	}
	if !v.True() {
		t.Errorf("internal_tls = %v; want true", v.GoString())
	}
}

func TestResolvePresets_object(t *testing.T) {
	presets := []manifest.Preset{
		{
			Name: "HA",
			Sets: map[string]any{
				"alertmanager": map[string]any{
					"units": 3,
				},
			},
		},
	}
	vars := sampleVarsForPreset(t)
	resolved := ResolvePresets(presets, vars)
	if len(resolved) != 1 {
		t.Fatalf("expected 1 preset, got %d", len(resolved))
	}
	v := resolved[0].Values["alertmanager"]
	if v.IsNull() || !v.Type().IsObjectType() {
		t.Fatalf("alertmanager value: %v", v.GoString())
	}
	units := v.AsValueMap()["units"]
	if !units.Equals(cty.NumberIntVal(3)).True() {
		t.Errorf("units = %v; want 3", units.GoString())
	}
	// app_name should be filled with its default.
	appName := v.AsValueMap()["app_name"]
	if appName.AsString() != "alertmanager" {
		t.Errorf("app_name = %v; want default 'alertmanager'", appName.GoString())
	}
}

func TestResolvePresets_map(t *testing.T) {
	presets := []manifest.Preset{
		{
			Name: "Tagged",
			Sets: map[string]any{
				"labels": map[string]any{
					"env":  "prod",
					"team": "obs",
				},
			},
		},
	}
	vars := sampleVarsForPreset(t)
	resolved := ResolvePresets(presets, vars)
	if len(resolved) != 1 {
		t.Fatalf("expected 1 preset, got %d", len(resolved))
	}
	v := resolved[0].Values["labels"]
	m := v.AsValueMap()
	if m["env"].AsString() != "prod" || m["team"].AsString() != "obs" {
		t.Errorf("labels = %v", v.GoString())
	}
}

func TestResolvePresets_unknownVariable_skipped(t *testing.T) {
	presets := []manifest.Preset{
		{
			Name: "X",
			Sets: map[string]any{
				"nonexistent": "hello",
			},
		},
	}
	vars := sampleVarsForPreset(t)
	resolved := ResolvePresets(presets, vars)
	// Preset has no valid variables → dropped entirely.
	if len(resolved) != 0 {
		t.Errorf("expected 0 resolved presets (unknown var), got %d", len(resolved))
	}
}

func TestResolvePresets_typeMismatch_skipped(t *testing.T) {
	presets := []manifest.Preset{
		{
			Name: "Bad",
			Sets: map[string]any{
				"internal_tls": "not-a-bool",
			},
		},
	}
	vars := sampleVarsForPreset(t)
	resolved := ResolvePresets(presets, vars)
	// The bool variable gets a string → type error → skipped.
	if len(resolved) != 0 {
		t.Errorf("expected 0 (type mismatch skipped), got %d", len(resolved))
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
	presets := ResolvePresets([]manifest.Preset{
		{
			Name:        "Minimal",
			Description: "Bare minimum.",
			Sets: map[string]any{
				"internal_tls": false,
			},
		},
		{
			Name:        "HA Production",
			Description: "Multi-unit with TLS.",
			Sets: map[string]any{
				"internal_tls": true,
				"alertmanager": map[string]any{"units": 3},
			},
		},
	}, vars)
	m.SetPresets(presets)
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
	if !strings.Contains(out, "Minimal") {
		t.Errorf("picker view missing preset name; got:\n%s", out)
	}
	if !strings.Contains(out, "HA Production") {
		t.Errorf("picker view missing second preset; got:\n%s", out)
	}
	if !strings.Contains(out, "Esc") {
		t.Errorf("picker view missing Esc hint; got:\n%s", out)
	}
}

func TestAnyToCty_list(t *testing.T) {
	typ := &tftypes.Type{Kind: tftypes.KindList, Element: &tftypes.Type{Kind: tftypes.KindString}}
	val, err := anyToCty([]any{"a", "b"}, typ)
	if err != nil {
		t.Fatal(err)
	}
	if val.LengthInt() != 2 {
		t.Errorf("list length = %d; want 2", val.LengthInt())
	}
}
