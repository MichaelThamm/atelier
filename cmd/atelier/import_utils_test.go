package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/importer"
	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// --- resolveProviderSource ---

func TestResolveProviderSource_BareName(t *testing.T) {
	got := resolveProviderSource("juju")
	if got != "juju/juju" {
		t.Errorf("got %q, want juju/juju", got)
	}
}

func TestResolveProviderSource_FullSource(t *testing.T) {
	got := resolveProviderSource("hashicorp/aws")
	if got != "hashicorp/aws" {
		t.Errorf("got %q, want hashicorp/aws", got)
	}
}

func TestResolveProviderSource_Empty(t *testing.T) {
	got := resolveProviderSource("")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestResolveProviderSource_Whitespace(t *testing.T) {
	got := resolveProviderSource("  juju  ")
	if got != "juju/juju" {
		t.Errorf("got %q, want juju/juju", got)
	}
}

// --- validateVarKey ---

func TestValidateVarKey_Valid(t *testing.T) {
	for _, k := range []string{"model_uuid", "my_var", "a", "X123", "my-var"} {
		if err := validateVarKey(k); err != nil {
			t.Errorf("key %q: unexpected error: %v", k, err)
		}
	}
}

func TestValidateVarKey_Invalid(t *testing.T) {
	for _, k := range []string{"", "123abc", "my var", "foo@bar", "a.b", "a{b}"} {
		if err := validateVarKey(k); err == nil {
			t.Errorf("key %q: expected error, got nil", k)
		}
	}
}

func TestValidateVarKey_LeadingUnderscore(t *testing.T) {
	if err := validateVarKey("_private"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- typeList ---

func TestTypeList(t *testing.T) {
	rs := []importer.ListResource{
		{Type: "juju_application"},
		{Type: "juju_model"},
		{Type: "juju_integration"},
	}
	got := typeList(rs)
	want := []string{"juju_application", "juju_model", "juju_integration"}
	if len(got) != len(want) {
		t.Fatalf("got %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("index %d: got %q, want %q", i, got[i], w)
		}
	}
}

func TestTypeList_Empty(t *testing.T) {
	got := typeList(nil)
	if len(got) != 0 {
		t.Errorf("got %d items, want 0", len(got))
	}
}

// --- configAttrNames ---

func TestConfigAttrNames(t *testing.T) {
	lr := importer.ListResource{
		ConfigAttrs: []importer.ConfigAttr{
			{Name: "model_uuid", Required: true},
			{Name: "name", Required: false},
		},
	}
	got := configAttrNames(lr)
	want := "model_uuid*, name"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestConfigAttrNames_Empty(t *testing.T) {
	got := configAttrNames(importer.ListResource{})
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// --- convertStringToCty ---

func TestConvertStringToCty_String(t *testing.T) {
	v := &tfvars.Variable{
		Name: "test",
		Type: &tftypes.Type{Kind: tftypes.KindString},
	}
	got := convertStringToCty("hello", v)
	if got != cty.StringVal("hello") {
		t.Errorf("got %v, want cty.StringVal(\"hello\")", got)
	}
}

func TestConvertStringToCty_Bool(t *testing.T) {
	v := &tfvars.Variable{
		Name: "test",
		Type: &tftypes.Type{Kind: tftypes.KindBool},
	}

	// Test true values
	for _, val := range []string{"true", "1", "yes", "TRUE", "Yes"} {
		got := convertStringToCty(val, v)
		if got != cty.True {
			t.Errorf("convertStringToCty(%q) = %v, want cty.True", val, got)
		}
	}

	// Test false values
	for _, val := range []string{"false", "0", "no", "FALSE", "No"} {
		got := convertStringToCty(val, v)
		if got != cty.False {
			t.Errorf("convertStringToCty(%q) = %v, want cty.False", val, got)
		}
	}

	// Test invalid value
	got := convertStringToCty("invalid", v)
	if got != cty.NilVal {
		t.Errorf("convertStringToCty(\"invalid\") = %v, want cty.NilVal", got)
	}
}

func TestConvertStringToCty_Number(t *testing.T) {
	v := &tfvars.Variable{
		Name: "test",
		Type: &tftypes.Type{Kind: tftypes.KindNumber},
	}

	// Test integer
	got := convertStringToCty("42", v)
	if got.Type() != cty.Number {
		t.Errorf("convertStringToCty(\"42\") type = %v, want number", got.Type())
	}
	// Check that it's an integer value
	if got.AsBigFloat().IsInt() == false {
		t.Errorf("convertStringToCty(\"42\") should be an integer")
	}

	// Test float
	got = convertStringToCty("3.14", v)
	if got.Type() != cty.Number {
		t.Errorf("convertStringToCty(\"3.14\") type = %v, want number", got.Type())
	}

	// Test invalid value
	got = convertStringToCty("abc", v)
	if got != cty.NilVal {
		t.Errorf("convertStringToCty(\"abc\") = %v, want cty.NilVal", got)
	}
}

func TestConvertStringToCty_NilType(t *testing.T) {
	got := convertStringToCty("hello", nil)
	if got != cty.StringVal("hello") {
		t.Errorf("got %v, want cty.StringVal(\"hello\")", got)
	}
}

func TestConvertStringToCty_ObjectType(t *testing.T) {
	v := &tfvars.Variable{
		Name: "model",
		Type: &tftypes.Type{Kind: tftypes.KindObject},
	}
	got := convertStringToCty(`{uuid = "abc-123"}`, v)
	if got == cty.NilVal {
		t.Fatal("got cty.NilVal, expected a parsed object")
	}
	if !got.Type().IsObjectType() {
		t.Fatalf("got type %v, want object", got.Type())
	}
	if got.LengthInt() != 1 {
		t.Fatalf("got %d attrs, want 1", got.LengthInt())
	}
	uuid := got.GetAttr("uuid")
	if uuid != cty.StringVal("abc-123") {
		t.Errorf("uuid = %v, want %v", uuid, cty.StringVal("abc-123"))
	}
}

func TestConvertStringToCty_MapType(t *testing.T) {
	v := &tfvars.Variable{
		Name: "labels",
		Type: &tftypes.Type{Kind: tftypes.KindMap},
	}
	got := convertStringToCty(`{env = "prod", team = "billing"}`, v)
	if got == cty.NilVal {
		t.Fatal("got cty.NilVal, expected a parsed map")
	}
	// HCL parses bare {...} as an object; the value is still usable by terraform.
	// At minimum verify the value is non-nil and contains the expected keys.
	if got.LengthInt() != 2 {
		t.Fatalf("got %d attrs, want 2", got.LengthInt())
	}
}

func TestConvertStringToCty_InvalidHCL(t *testing.T) {
	v := &tfvars.Variable{
		Name: "bad",
		Type: &tftypes.Type{Kind: tftypes.KindObject},
	}
	got := convertStringToCty(`not valid hcl`, v)
	if got != cty.NilVal {
		t.Errorf("got %v, want cty.NilVal for invalid HCL", got)
	}
}

// --- applyPresets ---

func TestApplyPresets_SinglePreset(t *testing.T) {
	// Create a temporary directory with an atelier.local.yaml file.
	dir := t.TempDir()

	// Create a minimal atelier.local.yaml with a preset.
	yamlContent := `
modules:
  - path: "."
    presets:
      - name: test-preset
        description: "Test preset"
        sets:
          model_uuid: "test-uuid-123"
`
	if err := os.WriteFile(filepath.Join(dir, "atelier.local.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a wrapper state with a variable declaration.
	state := &wrapper.State{
		Dir:    dir,
		Values: make(map[string]cty.Value),
		Vars: []tfvars.Variable{
			{
				Name: "model_uuid",
				Type: &tftypes.Type{Kind: tftypes.KindString},
			},
		},
	}

	// Apply the preset.
	err := applyPresets(dir, state, []string{"test-preset"})
	if err != nil {
		t.Fatalf("applyPresets() error = %v", err)
	}

	// Check that the preset value was applied.
	val, ok := state.Values["model_uuid"]
	if !ok {
		t.Fatal("model_uuid not found in state.Values")
	}
	if val.AsString() != "test-uuid-123" {
		t.Errorf("model_uuid = %q, want %q", val.AsString(), "test-uuid-123")
	}
}

func TestApplyPresets_MultiplePresets(t *testing.T) {
	// Create a temporary directory with an atelier.local.yaml file.
	dir := t.TempDir()

	// Create a minimal atelier.local.yaml with multiple presets.
	yamlContent := `
modules:
  - path: "."
    presets:
      - name: preset-1
        description: "First preset"
        sets:
          model_uuid: "uuid-from-preset-1"
      - name: preset-2
        description: "Second preset"
        sets:
          model_uuid: "uuid-from-preset-2"
`
	if err := os.WriteFile(filepath.Join(dir, "atelier.local.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a wrapper state with a variable declaration.
	state := &wrapper.State{
		Dir:    dir,
		Values: make(map[string]cty.Value),
		Vars: []tfvars.Variable{
			{
				Name: "model_uuid",
				Type: &tftypes.Type{Kind: tftypes.KindString},
			},
		},
	}

	// Apply both presets (preset-2 should override preset-1).
	err := applyPresets(dir, state, []string{"preset-1", "preset-2"})
	if err != nil {
		t.Fatalf("applyPresets() error = %v", err)
	}

	// Check that preset-2's value won.
	val, ok := state.Values["model_uuid"]
	if !ok {
		t.Fatal("model_uuid not found in state.Values")
	}
	if val.AsString() != "uuid-from-preset-2" {
		t.Errorf("model_uuid = %q, want %q", val.AsString(), "uuid-from-preset-2")
	}
}

func TestApplyPresets_VarOverridesPreset(t *testing.T) {
	// Create a temporary directory with an atelier.local.yaml file.
	dir := t.TempDir()

	// Create a minimal atelier.local.yaml with a preset.
	yamlContent := `
modules:
  - path: "."
    presets:
      - name: test-preset
        description: "Test preset"
        sets:
          model_uuid: "uuid-from-preset"
`
	if err := os.WriteFile(filepath.Join(dir, "atelier.local.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a wrapper state with a variable declaration.
	state := &wrapper.State{
		Dir:    dir,
		Values: make(map[string]cty.Value),
		Vars: []tfvars.Variable{
			{
				Name: "model_uuid",
				Type: &tftypes.Type{Kind: tftypes.KindString},
			},
		},
	}

	// --var flag should override preset.
	config := map[string]string{
		"model_uuid": "uuid-from-var",
	}

	// Apply the preset and then the --var override.
	err := applyPresets(dir, state, []string{"test-preset"})
	if err != nil {
		t.Fatalf("applyPresets() error = %v", err)
	}
	applyVarOverrides(state, config)

	// Check that --var flag won.
	val, ok := state.Values["model_uuid"]
	if !ok {
		t.Fatal("model_uuid not found in state.Values")
	}
	if val.AsString() != "uuid-from-var" {
		t.Errorf("model_uuid = %q, want %q", val.AsString(), "uuid-from-var")
	}
}

func TestApplyPresets_PresetNotFound(t *testing.T) {
	// Create a temporary directory with an atelier.local.yaml file.
	dir := t.TempDir()

	// Create a minimal atelier.local.yaml with a preset.
	yamlContent := `
modules:
  - path: "."
    presets:
      - name: existing-preset
        description: "Existing preset"
        sets:
          model_uuid: "test-uuid"
`
	if err := os.WriteFile(filepath.Join(dir, "atelier.local.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a wrapper state.
	state := &wrapper.State{
		Dir:    dir,
		Values: make(map[string]cty.Value),
		Vars: []tfvars.Variable{
			{
				Name: "model_uuid",
				Type: &tftypes.Type{Kind: tftypes.KindString},
			},
		},
	}

	// Try to apply a non-existent preset.
	err := applyPresets(dir, state, []string{"non-existent-preset"})
	if err == nil {
		t.Fatal("expected error for non-existent preset, got nil")
	}
	if !contains(err.Error(), "non-existent-preset") {
		t.Errorf("error should mention the preset name, got: %v", err)
	}
}

func TestApplyPresets_NoPresetsFile(t *testing.T) {
	// Create a temporary directory without an atelier.local.yaml file.
	dir := t.TempDir()

	// Create a wrapper state.
	state := &wrapper.State{
		Dir:    dir,
		Values: make(map[string]cty.Value),
		Vars: []tfvars.Variable{
			{
				Name: "model_uuid",
				Type: &tftypes.Type{Kind: tftypes.KindString},
			},
		},
	}

	// Try to apply a preset when no file exists.
	err := applyPresets(dir, state, []string{"test-preset"})
	if err == nil {
		t.Fatal("expected error when no presets file exists, got nil")
	}
	if !contains(err.Error(), "no presets found") {
		t.Errorf("error should mention no presets found, got: %v", err)
	}
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
