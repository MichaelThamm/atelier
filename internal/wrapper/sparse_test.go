package wrapper

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/canonical/atelier/internal/tftypes"
	"github.com/canonical/atelier/internal/tfvars"
)

func mustParseType(t *testing.T, src string) *tftypes.Type {
	t.Helper()
	tp, err := tftypes.ParseTypeExpr(src)
	if err != nil {
		t.Fatal(err)
	}
	return tp
}

func TestShouldEmit_required(t *testing.T) {
	v := &tfvars.Variable{
		Name: "model_uuid",
		Type: mustParseType(t, "string"),
		// No default.
	}
	if !ShouldEmit(v, cty.StringVal("abc")) {
		t.Error("required vars must always emit (set)")
	}
	if !ShouldEmit(v, cty.NilVal) {
		t.Error("required vars must emit even when unset (placeholder)")
	}
}

func TestShouldEmit_optional(t *testing.T) {
	v := &tfvars.Variable{
		Name:       "internal_tls",
		Type:       mustParseType(t, "bool"),
		HasDefault: true,
		Default:    cty.True,
	}
	if ShouldEmit(v, cty.True) {
		t.Error("matches default → should not emit")
	}
	if !ShouldEmit(v, cty.False) {
		t.Error("differs from default → should emit")
	}
	if ShouldEmit(v, cty.NilVal) {
		t.Error("unset optional → should not emit (defaults apply)")
	}
}

func TestSparseValue_object_sparseRecursive(t *testing.T) {
	// Variable's type has optional fields with declared defaults.
	typ := mustParseType(t, `object({
		app_name = optional(string, "alertmanager")
		units    = optional(number, 1)
		config   = optional(map(string), {})
	})`)
	v := &tfvars.Variable{
		Name:       "alertmanager",
		Type:       typ,
		HasDefault: true,
		Default:    cty.EmptyObjectVal,
	}

	// All-default current value.
	cur := cty.ObjectVal(map[string]cty.Value{
		"app_name": cty.StringVal("alertmanager"),
		"units":    cty.NumberIntVal(1),
		"config":   cty.MapValEmpty(cty.String),
	})
	if ShouldEmit(v, cur) {
		t.Error("all-at-default object should not emit")
	}

	// Only units differs.
	cur = cty.ObjectVal(map[string]cty.Value{
		"app_name": cty.StringVal("alertmanager"),
		"units":    cty.NumberIntVal(3),
		"config":   cty.MapValEmpty(cty.String),
	})
	if !ShouldEmit(v, cur) {
		t.Error("differing units should make object emit")
	}
	sparse := SparseValue(v, cur)
	if !sparse.Type().IsObjectType() {
		t.Fatalf("sparse not object: %v", sparse.GoString())
	}
	m := sparse.AsValueMap()
	if len(m) != 1 {
		t.Errorf("sparse should contain only the differing field, got %d", len(m))
	}
	if !m["units"].Equals(cty.NumberIntVal(3)).True() {
		t.Errorf("sparse units = %v", m["units"].GoString())
	}
	if _, ok := m["app_name"]; ok {
		t.Errorf("app_name should not appear (at default)")
	}
}

func TestSparseValue_nestedObject(t *testing.T) {
	typ := mustParseType(t, `object({
		outer_field = optional(string, "outer")
		nested      = optional(object({
			inner_a = optional(string, "a")
			inner_b = optional(number, 0)
		}), {})
	})`)
	v := &tfvars.Variable{
		Name:       "blob",
		Type:       typ,
		HasDefault: true,
		Default:    cty.EmptyObjectVal,
	}

	cur := cty.ObjectVal(map[string]cty.Value{
		"outer_field": cty.StringVal("outer"),
		"nested": cty.ObjectVal(map[string]cty.Value{
			"inner_a": cty.StringVal("a"),
			"inner_b": cty.NumberIntVal(7),
		}),
	})
	if !ShouldEmit(v, cur) {
		t.Error("should emit; nested.inner_b differs from its default")
	}
	sparse := SparseValue(v, cur)
	m := sparse.AsValueMap()
	if _, ok := m["outer_field"]; ok {
		t.Errorf("outer_field at default; should not appear")
	}
	nested, ok := m["nested"]
	if !ok {
		t.Fatalf("nested should appear (inner_b differs)")
	}
	nm := nested.AsValueMap()
	if _, ok := nm["inner_a"]; ok {
		t.Errorf("inner_a at default; should not appear")
	}
	if !nm["inner_b"].Equals(cty.NumberIntVal(7)).True() {
		t.Errorf("inner_b = %v", nm["inner_b"].GoString())
	}
}

func TestSparseValue_primitive_passthrough(t *testing.T) {
	v := &tfvars.Variable{
		Name: "x",
		Type: mustParseType(t, "string"),
	}
	got := SparseValue(v, cty.StringVal("hello"))
	if got.AsString() != "hello" {
		t.Errorf("got %v", got.GoString())
	}
}
