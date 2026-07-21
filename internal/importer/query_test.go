package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- ExistingVars ---

func TestExistingVars_NoDir(t *testing.T) {
	got := ExistingVars("/nonexistent/path")
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestExistingVars_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got := ExistingVars(dir)
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestExistingVars_NoVariableBlocks(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
resource "null_resource" "x" {}
`), 0644)
	got := ExistingVars(dir)
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestExistingVars_SingleVariable(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "variables.tf"), []byte(`
variable "model_uuid" {
  type = string
}
`), 0644)
	got := ExistingVars(dir)
	if len(got) != 1 || got[0] != "model_uuid" {
		t.Errorf("got %v, want [model_uuid]", got)
	}
}

func TestExistingVars_MultipleVariables(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "variables.tf"), []byte(`
variable "alpha" {
  type = string
}
variable "beta" {
  type = string
}
`), 0644)
	got := ExistingVars(dir)
	if len(got) != 2 {
		t.Fatalf("got %d vars, want 2", len(got))
	}
	// Should be in declaration order.
	if got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("got %v, want [alpha beta]", got)
	}
}

func TestExistingVars_Dedup(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.tf"), []byte(`variable "x" { type = string }`), 0644)
	os.WriteFile(filepath.Join(dir, "b.tf"), []byte(`variable "x" { type = string }`), 0644)
	got := ExistingVars(dir)
	if len(got) != 1 || got[0] != "x" {
		t.Errorf("got %v, want [x] (deduped)", got)
	}
}

func TestExistingVars_MalformedHCL(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bad.tf"), []byte(`this is not valid HCL {{{`), 0644)
	os.WriteFile(filepath.Join(dir, "good.tf"), []byte(`variable "ok" { type = string }`), 0644)
	got := ExistingVars(dir)
	// Bad file should be skipped; good file's variable should be returned.
	if len(got) != 1 || got[0] != "ok" {
		t.Errorf("got %v, want [ok]", got)
	}
}

func TestExistingVars_MixedBlocks(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
variable "a" { type = string }
resource "null_resource" "x" {}
variable "b" { type = string }
locals { c = 1 }
`), 0644)
	got := ExistingVars(dir)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("got %v, want [a b]", got)
	}
}

// --- RenderQueryFile with existingVars ---

func TestRenderQueryFile_SkipsExistingVars(t *testing.T) {
	selected := []ListResource{
		{Type: "juju_application", ProviderLocal: "juju"},
	}
	config := map[string]string{"model_uuid": "abc"}
	got := string(RenderQueryFile(selected, config, "model_uuid"))
	if strings.Contains(got, `variable "model_uuid"`) {
		t.Error("should not contain variable declaration for existing var")
	}
	if !strings.Contains(got, `var.model_uuid`) {
		t.Error("should still reference var.model_uuid in config block")
	}
}

func TestRenderQueryFile_DeclaresNewVars(t *testing.T) {
	selected := []ListResource{
		{Type: "juju_application", ProviderLocal: "juju"},
	}
	config := map[string]string{"model_uuid": "abc"}
	got := string(RenderQueryFile(selected, config))
	if !strings.Contains(got, `variable "model_uuid"`) {
		t.Error("should contain variable declaration")
	}
}

func TestRenderQueryFile_MixedExistingAndNew(t *testing.T) {
	selected := []ListResource{
		{Type: "juju_application", ProviderLocal: "juju"},
	}
	config := map[string]string{"model_uuid": "abc", "region": "us-east-1"}
	got := string(RenderQueryFile(selected, config, "model_uuid"))
	if strings.Contains(got, `variable "model_uuid"`) {
		t.Error("model_uuid should be skipped (already exists)")
	}
	if !strings.Contains(got, `variable "region"`) {
		t.Error("region should be declared (new)")
	}
}

// --- configKeysFor ---

func TestConfigKeysFor_SchemaKnown_Filtered(t *testing.T) {
	lr := ListResource{
		ConfigAttrs: []ConfigAttr{
			{Name: "model_uuid", Required: true},
		},
	}
	keys := []string{"model_uuid", "extra_key"}
	got := configKeysFor(lr, keys)
	if len(got) != 1 || got[0] != "model_uuid" {
		t.Errorf("got %v, want [model_uuid]", got)
	}
}

func TestConfigKeysFor_SchemaSilent_EmitAll(t *testing.T) {
	lr := ListResource{ConfigAttrs: nil}
	keys := []string{"model_uuid", "extra_key"}
	got := configKeysFor(lr, keys)
	if len(got) != 2 {
		t.Errorf("got %d keys, want 2 (all emitted when schema silent)", len(got))
	}
}

// --- HasConfigAttr ---

func TestHasConfigAttr_Found(t *testing.T) {
	lr := ListResource{ConfigAttrs: []ConfigAttr{{Name: "model_uuid"}}}
	if !lr.HasConfigAttr("model_uuid") {
		t.Error("expected true")
	}
}

func TestHasConfigAttr_NotFound(t *testing.T) {
	lr := ListResource{ConfigAttrs: []ConfigAttr{{Name: "model_uuid"}}}
	if lr.HasConfigAttr("other") {
		t.Error("expected false")
	}
}

func TestHasConfigAttr_Empty(t *testing.T) {
	lr := ListResource{}
	if lr.HasConfigAttr("model_uuid") {
		t.Error("expected false for empty")
	}
}

// --- mergeMaps ---

func TestMergeMaps_BothNil(t *testing.T) {
	got := mergeMaps(nil, nil)
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestMergeMaps_FirstNil(t *testing.T) {
	b := map[string]string{"b": "2"}
	got := mergeMaps(nil, b)
	if len(got) != 1 || got["b"] != "2" {
		t.Errorf("got %v, want {b: 2}", got)
	}
}

func TestMergeMaps_SecondNil(t *testing.T) {
	a := map[string]string{"a": "1"}
	got := mergeMaps(a, nil)
	if len(got) != 1 || got["a"] != "1" {
		t.Errorf("got %v, want {a: 1}", got)
	}
}

func TestMergeMaps_NoOverlap(t *testing.T) {
	a := map[string]string{"a": "1"}
	b := map[string]string{"b": "2"}
	got := mergeMaps(a, b)
	if len(got) != 2 || got["a"] != "1" || got["b"] != "2" {
		t.Errorf("got %v, want {a: 1, b: 2}", got)
	}
}

func TestMergeMaps_OverlapSecondWins(t *testing.T) {
	a := map[string]string{"a": "1", "shared": "from-a"}
	b := map[string]string{"b": "2", "shared": "from-b"}
	got := mergeMaps(a, b)
	if len(got) != 3 || got["shared"] != "from-b" {
		t.Errorf("got %v, want shared=from-b", got)
	}
}
