package wrapper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// seedState builds a wrapper state with the given variable declarations.
func seedState(t *testing.T, vars ...tfvars.Variable) *State {
	t.Helper()
	return &State{Vars: vars, Values: map[string]cty.Value{}}
}

// mustVarT builds a variable declaration from a type expression.
func mustVarT(t *testing.T, name, typeSrc string, def cty.Value, hasDef bool) tfvars.Variable {
	t.Helper()
	tp, err := tftypes.ParseTypeExpr(typeSrc)
	if err != nil {
		t.Fatalf("parse type %q: %v", typeSrc, err)
	}
	return tfvars.Variable{Name: name, Type: tp, HasDefault: hasDef, Default: def, Nullable: true}
}

// --- ConvertStringToCty ---

func TestConvertStringToCty_String(t *testing.T) {
	v := &tfvars.Variable{
		Name: "test",
		Type: &tftypes.Type{Kind: tftypes.KindString},
	}
	got := ConvertStringToCty("hello", v)
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
		got := ConvertStringToCty(val, v)
		if got != cty.True {
			t.Errorf("ConvertStringToCty(%q) = %v, want cty.True", val, got)
		}
	}

	// Test false values
	for _, val := range []string{"false", "0", "no", "FALSE", "No"} {
		got := ConvertStringToCty(val, v)
		if got != cty.False {
			t.Errorf("ConvertStringToCty(%q) = %v, want cty.False", val, got)
		}
	}

	// Test invalid value
	got := ConvertStringToCty("invalid", v)
	if got != cty.NilVal {
		t.Errorf("ConvertStringToCty(\"invalid\") = %v, want cty.NilVal", got)
	}
}

func TestConvertStringToCty_Number(t *testing.T) {
	v := &tfvars.Variable{
		Name: "test",
		Type: &tftypes.Type{Kind: tftypes.KindNumber},
	}

	// Test integer
	got := ConvertStringToCty("42", v)
	if got.Type() != cty.Number {
		t.Errorf("ConvertStringToCty(\"42\") type = %v, want number", got.Type())
	}
	// Check that it's an integer value
	if got.AsBigFloat().IsInt() == false {
		t.Errorf("ConvertStringToCty(\"42\") should be an integer")
	}

	// Test float
	got = ConvertStringToCty("3.14", v)
	if got.Type() != cty.Number {
		t.Errorf("ConvertStringToCty(\"3.14\") type = %v, want number", got.Type())
	}

	// Test invalid value
	got = ConvertStringToCty("abc", v)
	if got != cty.NilVal {
		t.Errorf("ConvertStringToCty(\"abc\") = %v, want cty.NilVal", got)
	}
}

func TestConvertStringToCty_NilType(t *testing.T) {
	got := ConvertStringToCty("hello", nil)
	if got != cty.StringVal("hello") {
		t.Errorf("got %v, want cty.StringVal(\"hello\")", got)
	}
}

func TestConvertStringToCty_ObjectType(t *testing.T) {
	v := &tfvars.Variable{
		Name: "model",
		Type: &tftypes.Type{Kind: tftypes.KindObject},
	}
	got := ConvertStringToCty(`{uuid = "abc-123"}`, v)
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
	got := ConvertStringToCty(`{env = "prod", team = "billing"}`, v)
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
	got := ConvertStringToCty(`not valid hcl`, v)
	if got != cty.NilVal {
		t.Errorf("got %v, want cty.NilVal for invalid HCL", got)
	}
}

// --- ApplyVarOverrides ---

func TestApplyVarOverrides_ObjectMergesOverExisting(t *testing.T) {
	v := mustVarT(t, "ingress", `object({alertmanager=optional(bool,true),loki=optional(bool,true),prometheus=optional(bool,true)})`, cty.NilVal, true)
	s := seedState(t, v)
	// A var-file set every key false...
	s.Values["ingress"] = cty.ObjectVal(map[string]cty.Value{
		"alertmanager": cty.False,
		"loki":         cty.False,
		"prometheus":   cty.False,
	})
	// ...and --var turns one back on. The others must survive.
	ApplyVarOverrides(s, map[string]string{"ingress": "{alertmanager=true}"})

	got := s.Values["ingress"].AsValueMap()
	if got["alertmanager"] != cty.True {
		t.Errorf("alertmanager = %v, want true", got["alertmanager"])
	}
	if got["loki"] != cty.False || got["prometheus"] != cty.False {
		t.Errorf("unmentioned keys were lost: %v", got)
	}
}

func TestApplyVarOverrides_ObjectDeepMerge(t *testing.T) {
	v := mustVarT(t, "app", `object({units=optional(number,1),cfg=optional(object({a=optional(string,"x"),b=optional(string,"y")}))})`, cty.NilVal, true)
	s := seedState(t, v)
	s.Values["app"] = cty.ObjectVal(map[string]cty.Value{
		"units": cty.NumberIntVal(3),
		"cfg": cty.ObjectVal(map[string]cty.Value{
			"a": cty.StringVal("x"),
			"b": cty.StringVal("y"),
		}),
	})
	ApplyVarOverrides(s, map[string]string{"app": `{cfg={a="z"}}`})

	app := s.Values["app"].AsValueMap()
	if app["units"].AsBigFloat().String() != "3" {
		t.Errorf("units = %v, want 3 (untouched)", app["units"])
	}
	cfg := app["cfg"].AsValueMap()
	if cfg["a"].AsString() != "z" {
		t.Errorf("cfg.a = %v, want z", cfg["a"])
	}
	if cfg["b"].AsString() != "y" {
		t.Errorf("cfg.b = %v, want y (nested merge)", cfg["b"])
	}
}

func TestApplyVarOverrides_ScalarReplaces(t *testing.T) {
	s := seedState(t, mustVarT(t, "internal_tls", "bool", cty.True, true))
	s.Values["internal_tls"] = cty.True
	ApplyVarOverrides(s, map[string]string{"internal_tls": "false"})
	if s.Values["internal_tls"] != cty.False {
		t.Errorf("internal_tls = %v, want false", s.Values["internal_tls"])
	}
}

// cty's AsValueMap panics on null/unknown objects, so the merge guard must be
// exercised: a null existing value is replaced, not merged into.
func TestApplyVarOverrides_NullExistingValueReplacedNotMerged(t *testing.T) {
	s := seedState(t, mustVarT(t, "ingress", `object({a=optional(bool,true)})`, cty.NilVal, true))
	s.Values["ingress"] = cty.NullVal(cty.Object(map[string]cty.Type{"a": cty.Bool}))

	warns := ApplyVarOverrides(s, map[string]string{"ingress": "{a=false}"})
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	got := s.Values["ingress"]
	if got.IsNull() {
		t.Fatal("null value should have been replaced by the override")
	}
	if got.AsValueMap()["a"] != cty.False {
		t.Errorf("a = %v, want false", got.AsValueMap()["a"])
	}
}

func TestApplyVarOverrides_WinsOverVarFiles(t *testing.T) {
	s := seedState(t,
		mustVarT(t, "name", "string", cty.NilVal, false),
		mustVarT(t, "replicas", "number", cty.NumberIntVal(1), true),
	)
	s.Values["name"] = cty.StringVal("from-file")

	warns := ApplyVarOverrides(s, map[string]string{"name": "from-var", "replicas": "5"})

	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	if v := s.Values["name"]; v.AsString() != "from-var" {
		t.Errorf("name = %v, want --var to win over the var-file", v)
	}
	if v := s.Values["replicas"]; v.AsBigFloat().String() != "5" {
		t.Errorf("replicas = %v, want 5", v)
	}
}

// An undeclared --var name is reported, not silently dropped.
func TestApplyVarOverrides_UnknownVariableWarns(t *testing.T) {
	s := seedState(t, mustVarT(t, "name", "string", cty.NilVal, false))
	warns := ApplyVarOverrides(s, map[string]string{"nonexistent": "1"})
	if len(warns) != 1 || !strings.Contains(warns[0], "unknown variable") {
		t.Errorf("warnings = %v, want one about an unknown variable", warns)
	}
	if _, ok := s.Values["nonexistent"]; ok {
		t.Error("unknown variable should not be written")
	}
}

// A --var value that does not fit the declared type is reported, not dropped.
func TestApplyVarOverrides_TypeMismatchWarns(t *testing.T) {
	s := seedState(t, mustVarT(t, "replicas", "number", cty.NumberIntVal(1), true))
	warns := ApplyVarOverrides(s, map[string]string{"replicas": "abc"})
	if len(warns) != 1 || !strings.Contains(warns[0], "does not fit type") {
		t.Errorf("warnings = %v, want one about a type mismatch", warns)
	}
}

// Warnings are ordered deterministically even though the input is a map.
func TestApplyVarOverrides_WarningsSorted(t *testing.T) {
	s := seedState(t, mustVarT(t, "name", "string", cty.NilVal, false))
	warns := ApplyVarOverrides(s, map[string]string{"zzz": "1", "aaa": "1", "mmm": "1"})
	if len(warns) != 3 {
		t.Fatalf("warnings = %v, want 3", warns)
	}
	for i := 1; i < len(warns); i++ {
		if warns[i-1] > warns[i] {
			t.Errorf("warnings not sorted: %v", warns)
		}
	}
}

func TestMergeObjects_NonObjectOverrideWins(t *testing.T) {
	base := cty.ObjectVal(map[string]cty.Value{"a": cty.True})
	// A non-objectish override replaces wholesale rather than merging.
	if got := mergeObjects(base, cty.NullVal(base.Type())); !got.IsNull() {
		t.Errorf("merge with a null override = %v, want null", got)
	}
}

// --- ApplyVarFiles ---

func TestApplyVarFiles_LaterWinsAndMissingErrors(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.tfvars")
	b := filepath.Join(dir, "b.tfvars")
	if err := os.WriteFile(a, []byte("name = \"from-a\"\nreplicas = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("name = \"from-b\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := seedState(t,
		mustVarT(t, "name", "string", cty.NilVal, false),
		mustVarT(t, "replicas", "number", cty.NumberIntVal(1), true),
	)
	warns, err := ApplyVarFiles(s, []string{a, b}, false)
	if err != nil {
		t.Fatalf("ApplyVarFiles: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	if v := s.Values["name"]; v.AsString() != "from-b" {
		t.Errorf("name = %v, want the later file to win", v)
	}
	if v := s.Values["replicas"]; v.AsBigFloat().String() != "2" {
		t.Errorf("replicas = %v, want 2 from the first file", v)
	}
	if _, err := ApplyVarFiles(s, []string{filepath.Join(dir, "missing.tfvars")}, false); err == nil {
		t.Error("expected an error for a missing --var-file")
	}
}

// TestApplyVarFiles_warnsAndStrictFails pins the ADR-0031 binding behaviour:
// an undeclared name and a type-mismatched value are skipped and reported; no
// invalid value reaches the wrapper, and --strict turns the report into an
// error so a rotted example fails CI instead of silently under-applying.
func TestApplyVarFiles_warnsAndStrictFails(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "bad.tfvars")
	if err := os.WriteFile(f, []byte("bogus = 1\nreplicas = \"three\"\nname = \"ok\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := seedState(t,
		mustVarT(t, "name", "string", cty.NilVal, false),
		mustVarT(t, "replicas", "number", cty.NumberIntVal(1), true),
	)
	warns, err := ApplyVarFiles(s, []string{f}, false)
	if err != nil {
		t.Fatalf("non-strict must warn, not fail: %v", err)
	}
	if len(warns) != 2 {
		t.Errorf("want 2 warnings (unknown name + type mismatch), got %v", warns)
	}
	if v := s.Values["name"]; v.AsString() != "ok" {
		t.Errorf("valid value not applied: %v", v)
	}
	if _, ok := s.Values["replicas"]; ok {
		t.Error("mismatched value must be skipped, not applied")
	}
	if _, err := ApplyVarFiles(s, []string{f}, true); err == nil {
		t.Error("--strict should make binding problems fatal")
	}
}
