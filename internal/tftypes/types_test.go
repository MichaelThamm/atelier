package tftypes

import (
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func TestParseTypeExpr_primitives(t *testing.T) {
	cases := []struct {
		src  string
		kind Kind
	}{
		{"string", KindString},
		{"bool", KindBool},
		{"number", KindNumber},
		{"any", KindAny},
	}
	for _, c := range cases {
		got, err := ParseTypeExpr(c.src)
		if err != nil {
			t.Fatalf("ParseTypeExpr(%q): %v", c.src, err)
		}
		if got.Kind != c.kind {
			t.Errorf("ParseTypeExpr(%q): kind = %v, want %v", c.src, got.Kind, c.kind)
		}
	}
}

func TestParseTypeExpr_collections(t *testing.T) {
	got, err := ParseTypeExpr("list(string)")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindList || got.Element.Kind != KindString {
		t.Errorf("list(string): %+v", got)
	}

	got, err = ParseTypeExpr("set(number)")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindSet || got.Element.Kind != KindNumber {
		t.Errorf("set(number): %+v", got)
	}

	got, err = ParseTypeExpr("map(bool)")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindMap || got.Element.Kind != KindBool {
		t.Errorf("map(bool): %+v", got)
	}
}

func TestParseTypeExpr_object(t *testing.T) {
	got, err := ParseTypeExpr(`object({
		name = string
		count = optional(number, 3)
		tags = optional(map(string))
	})`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindObject {
		t.Fatalf("kind = %v", got.Kind)
	}
	if len(got.AttrOrder) != 3 {
		t.Fatalf("expected 3 attrs, got %d", len(got.AttrOrder))
	}
	if got.AttrOrder[0] != "name" || got.AttrOrder[1] != "count" || got.AttrOrder[2] != "tags" {
		t.Errorf("attr order: %v", got.AttrOrder)
	}

	if a := got.Attributes["name"]; a.Optional || a.Type.Kind != KindString {
		t.Errorf("name attr: %+v", a)
	}

	cnt := got.Attributes["count"]
	if !cnt.Optional || !cnt.HasDefault {
		t.Errorf("count should be optional with default: %+v", cnt)
	}
	if cnt.Default.AsBigFloat().Cmp(cty.NumberIntVal(3).AsBigFloat()) != 0 {
		t.Errorf("count default = %v, want 3", cnt.Default.GoString())
	}

	tags := got.Attributes["tags"]
	if !tags.Optional || tags.HasDefault {
		t.Errorf("tags should be optional without default: %+v", tags)
	}
	if tags.Type.Kind != KindMap || tags.Type.Element.Kind != KindString {
		t.Errorf("tags inner type: %+v", tags.Type)
	}
}

func TestParseTypeExpr_nestedObject(t *testing.T) {
	got, err := ParseTypeExpr(`object({
		nested = object({
			x = string
		})
	})`)
	if err != nil {
		t.Fatal(err)
	}
	inner := got.Attributes["nested"].Type
	if inner.Kind != KindObject {
		t.Fatalf("nested kind = %v", inner.Kind)
	}
	if inner.Attributes["x"].Type.Kind != KindString {
		t.Errorf("nested.x: %+v", inner.Attributes["x"])
	}
}

func TestParseTypeExpr_errors(t *testing.T) {
	bad := []string{
		"banana",                   // unknown keyword
		"list(string, number)",     // wrong arity
		"optional(string)",         // optional outside object
		"object({a = optional()})", // optional with 0 args
	}
	for _, s := range bad {
		_, err := ParseTypeExpr(s)
		if err == nil {
			t.Errorf("ParseTypeExpr(%q): expected error", s)
		}
	}
}

func TestType_String(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"string", "string"},
		{"list(string)", "list(string)"},
		{"map(number)", "map(number)"},
	}
	for _, c := range cases {
		t1, err := ParseTypeExpr(c.src)
		if err != nil {
			t.Fatal(err)
		}
		if got := t1.String(); got != c.want {
			t.Errorf("%s.String() = %q, want %q", c.src, got, c.want)
		}
	}
}

func TestParseValueExpr(t *testing.T) {
	v, err := ParseValueExpr(`"hello"`)
	if err != nil {
		t.Fatal(err)
	}
	if v.AsString() != "hello" {
		t.Errorf("got %v", v.GoString())
	}

	v, err = ParseValueExpr(`42`)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Equals(cty.NumberIntVal(42)).True() {
		t.Errorf("got %v", v.GoString())
	}

	v, err = ParseValueExpr(`["a", "b"]`)
	if err != nil {
		t.Fatal(err)
	}
	if v.LengthInt() != 2 {
		t.Errorf("got len %d", v.LengthInt())
	}
}

func TestEqualValue_primitives(t *testing.T) {
	if !EqualValue(cty.StringVal("x"), cty.StringVal("x")) {
		t.Errorf("equal strings should be equal")
	}
	if EqualValue(cty.StringVal("x"), cty.StringVal("y")) {
		t.Errorf("differing strings should not be equal")
	}
	if !EqualValue(cty.NullVal(cty.String), cty.NullVal(cty.String)) {
		t.Errorf("two nulls should be equal")
	}
	if EqualValue(cty.NullVal(cty.String), cty.StringVal("")) {
		t.Errorf("null and empty string should differ")
	}
}

func TestEqualValue_objectByIntersection(t *testing.T) {
	a := cty.ObjectVal(map[string]cty.Value{
		"a": cty.StringVal("x"),
		"b": cty.NumberIntVal(1),
	})
	b := cty.ObjectVal(map[string]cty.Value{
		"a": cty.StringVal("x"),
		"b": cty.NumberIntVal(1),
	})
	if !EqualValue(a, b) {
		t.Errorf("identical objects should be equal")
	}

	c := cty.ObjectVal(map[string]cty.Value{
		"a": cty.StringVal("x"),
		"b": cty.NumberIntVal(2),
	})
	if EqualValue(a, c) {
		t.Errorf("differing object should not be equal")
	}

	// Object with extra field is treated as differing (consistent with
	// "value differs from declared default").
	d := cty.ObjectVal(map[string]cty.Value{
		"a": cty.StringVal("x"),
		"b": cty.NumberIntVal(1),
		"c": cty.True,
	})
	if EqualValue(a, d) {
		t.Errorf("extra field should make objects unequal")
	}
}

func TestEqualValue_set(t *testing.T) {
	a := cty.SetVal([]cty.Value{cty.StringVal("x"), cty.StringVal("y")})
	b := cty.SetVal([]cty.Value{cty.StringVal("y"), cty.StringVal("x")})
	if !EqualValue(a, b) {
		t.Errorf("sets with same elements should be equal regardless of order")
	}
}

func TestZeroValue(t *testing.T) {
	tStr, _ := ParseTypeExpr("string")
	if v := ZeroValue(tStr); v.AsString() != "" {
		t.Errorf("string zero: %v", v.GoString())
	}

	tBool, _ := ParseTypeExpr("bool")
	if v := ZeroValue(tBool); v.True() {
		t.Errorf("bool zero should be false")
	}

	tNum, _ := ParseTypeExpr("number")
	if v := ZeroValue(tNum); !v.Equals(cty.NumberIntVal(0)).True() {
		t.Errorf("number zero: %v", v.GoString())
	}

	tObj, _ := ParseTypeExpr("object({a = string, b = optional(number, 5)})")
	v := ZeroValue(tObj)
	m := v.AsValueMap()
	if m["a"].AsString() != "" {
		t.Errorf("a zero: %v", m["a"].GoString())
	}
	if !m["b"].Equals(cty.NumberIntVal(5)).True() {
		t.Errorf("b zero (optional with default): %v", m["b"].GoString())
	}
}
