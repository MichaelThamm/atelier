package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// --- parseModuleAddArgs: --var-file ---

func TestParseModuleAddArgs_VarFile(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"url", "--var-file", "test.tfvars"}, []string{"test.tfvars"}},
		{[]string{"url", "--var-file=prod.tfvars"}, []string{"prod.tfvars"}},
		{[]string{"url", "--var-file", "a.tfvars", "--var-file=b.tfvars"}, []string{"a.tfvars", "b.tfvars"}},
	}
	for _, c := range cases {
		opts, err := parseModuleArgs(c.args)
		if err != nil {
			t.Fatalf("parse(%v): %v", c.args, err)
		}
		if !slices.Equal(opts.VarFiles, c.want) {
			t.Errorf("parse(%v) var files = %v, want %v", c.args, opts.VarFiles, c.want)
		}
	}
	if _, err := parseModuleArgs([]string{"url", "--var-file"}); err == nil {
		t.Error("expected error for --var-file with no value")
	}
}

func TestParseModuleAddArgs_VarFileCommaList(t *testing.T) {
	opts, err := parseModuleArgs([]string{"url", "--var-file", "a,b", "--var-file=c"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c"}; !slices.Equal(opts.VarFiles, want) {
		t.Errorf("VarFiles = %v, want %v", opts.VarFiles, want)
	}
}

// --- parseModuleAddArgs: --var ---

func TestParseModuleAddArgs_Var(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"url", "--var", "a=1"}, []string{"a=1"}},
		{[]string{"url", "--var=a=1"}, []string{"a=1"}},
		{[]string{"url", "--var", "a=1", "--var=b=2"}, []string{"a=1", "b=2"}},
		// A value may contain '='; only the first one splits.
		{[]string{"url", "--var", "url=http://host:8333/x?a=b"}, []string{"url=http://host:8333/x?a=b"}},
	}
	for _, c := range cases {
		opts, err := parseModuleArgs(c.args)
		if err != nil {
			t.Fatalf("parse(%v): %v", c.args, err)
		}
		if !slices.Equal(opts.Vars, c.want) {
			t.Errorf("parse(%v) vars = %v, want %v", c.args, opts.Vars, c.want)
		}
	}
	if _, err := parseModuleArgs([]string{"url", "--var"}); err == nil {
		t.Error("expected error for --var with no value")
	}
	if _, err := parseModuleArgs([]string{"url", "--var", "novalue"}); err == nil {
		t.Error("expected error for --var without =")
	}
}

func TestVarsToMap_SplitsOnFirstEquals(t *testing.T) {
	got := varsToMap([]string{"a=1", "url=http://h:1/x?y=z"})
	if got["a"] != "1" {
		t.Errorf("a = %q, want 1", got["a"])
	}
	if got["url"] != "http://h:1/x?y=z" {
		t.Errorf("url = %q, want the whole value", got["url"])
	}
}

func TestParseModuleAddArgs_ListVarFiles(t *testing.T) {
	opts, err := parseModuleArgs([]string{"url", "--list-var-files"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.ListVarFiles {
		t.Error("ListVarFiles not set")
	}
}

// --- applyVarFlags ---

// applyVarFlags encodes the rule `module add` relies on: a --var-file seeds
// values, then --var overrides win. This pins that ordering at the shared
// helper, so a refactor of either caller cannot silently flip it.
func TestApplyVarFlags_VarWinsOverVarFile(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "seed.tfvars")
	if err := os.WriteFile(bundle, []byte("name = \"from-file\"\nreplicas = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := &wrapper.State{
		Vars: []tfvars.Variable{
			{Name: "name", Type: &tftypes.Type{Kind: tftypes.KindString}},
			{Name: "replicas", Type: &tftypes.Type{Kind: tftypes.KindNumber}, HasDefault: true, Default: cty.NumberIntVal(1)},
		},
		Values: map[string]cty.Value{},
	}

	err := applyVarFlags(state, dir, "", "", []string{bundle}, []string{"name=from-var"}, false)
	if err != nil {
		t.Fatalf("applyVarFlags: %v", err)
	}
	if v := state.Values["name"]; v.AsString() != "from-var" {
		t.Errorf("name = %v, want --var to win over the var-file", v)
	}
	if v := state.Values["replicas"]; v.AsBigFloat().String() != "2" {
		t.Errorf("replicas = %v, want 2 from the var-file", v)
	}
}

// A --var-file name that matches nothing is skipped with a warning, not fatal:
// a gallery entry's preset may be superseded by a bundle the user supplies.
func TestApplyVarFlags_MissingFileIsSkipped(t *testing.T) {
	state := &wrapper.State{
		Vars: []tfvars.Variable{
			{Name: "name", Type: &tftypes.Type{Kind: tftypes.KindString}},
		},
		Values: map[string]cty.Value{},
	}
	if err := applyVarFlags(state, t.TempDir(), "", "", []string{"nope.tfvars"}, nil, false); err != nil {
		t.Fatalf("a missing --var-file must be skipped, not fatal: %v", err)
	}
	// A name that resolves in a later source must still apply.
	dir := t.TempDir()
	bundle := filepath.Join(dir, "seed.tfvars")
	if err := os.WriteFile(bundle, []byte("name = \"from-file\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyVarFlags(state, dir, "", "", []string{bundle}, nil, false); err != nil {
		t.Fatalf("applyVarFlags: %v", err)
	}
	if v := state.Values["name"]; v.AsString() != "from-file" {
		t.Errorf("name = %v, want from-file", v)
	}
}

// --- sanitizeBlockName ---

func TestSanitizeBlockName_Hyphens(t *testing.T) {
	got := sanitizeBlockName("my-module")
	if got != "my_module" {
		t.Errorf("got %q, want my_module", got)
	}
}

func TestSanitizeBlockName_Dots(t *testing.T) {
	got := sanitizeBlockName("my.module")
	if got != "my_module" {
		t.Errorf("got %q, want my_module", got)
	}
}

func TestSanitizeBlockName_LeadingDigits(t *testing.T) {
	got := sanitizeBlockName("123module")
	if got != "module" {
		t.Errorf("got %q, want module", got)
	}
}

func TestSanitizeBlockName_SpecialChars(t *testing.T) {
	got := sanitizeBlockName("foo@bar$baz{qux}")
	if got != "foobarbazqux" {
		t.Errorf("got %q, want foobarbazqux", got)
	}
}

func TestSanitizeBlockName_Spaces(t *testing.T) {
	got := sanitizeBlockName("my module")
	if got != "mymodule" {
		t.Errorf("got %q, want mymodule", got)
	}
}

func TestSanitizeBlockName_Empty(t *testing.T) {
	got := sanitizeBlockName("@#$")
	if got != "module" {
		t.Errorf("got %q, want module (fallback)", got)
	}
}

func TestSanitizeBlockName_LeadingDigitsAndSpecial(t *testing.T) {
	got := sanitizeBlockName("123@#$")
	if got != "module" {
		t.Errorf("got %q, want module (fallback)", got)
	}
}

func TestSanitizeBlockName_AlreadyValid(t *testing.T) {
	got := sanitizeBlockName("valid_name")
	if got != "valid_name" {
		t.Errorf("got %q, want valid_name", got)
	}
}

func TestSanitizeBlockName_Underscores(t *testing.T) {
	got := sanitizeBlockName("a_b_c")
	if got != "a_b_c" {
		t.Errorf("got %q, want a_b_c", got)
	}
}

// --- uniqueBlockName ---

func TestUniqueBlockName_NoCollision(t *testing.T) {
	got := uniqueBlockName("cos_lite", nil)
	if got != "cos_lite" {
		t.Errorf("got %q, want cos_lite", got)
	}
}

func TestUniqueBlockName_Collision(t *testing.T) {
	existing := []wrapper.ModuleBlockInfo{
		{Name: "cos_lite"},
	}
	got := uniqueBlockName("cos_lite", existing)
	if got != "cos_lite_2" {
		t.Errorf("got %q, want cos_lite_2", got)
	}
}

func TestUniqueBlockName_MultipleCollisions(t *testing.T) {
	existing := []wrapper.ModuleBlockInfo{
		{Name: "cos_lite"},
		{Name: "cos_lite_2"},
		{Name: "cos_lite_3"},
	}
	got := uniqueBlockName("cos_lite", existing)
	if got != "cos_lite_4" {
		t.Errorf("got %q, want cos_lite_4", got)
	}
}

func TestUniqueBlockName_EmptyExisting(t *testing.T) {
	got := uniqueBlockName("anything", []wrapper.ModuleBlockInfo{})
	if got != "anything" {
		t.Errorf("got %q, want anything", got)
	}
}

// --- parseModuleArgs: --dir ---

func TestParseModuleArgs_Dir(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"url", "--dir", "cos-lite"}, "cos-lite"},
		{[]string{"url", "--dir=cos-lite"}, "cos-lite"},
		{[]string{"url"}, ""},
	} {
		opts, err := parseModuleArgs(c.args)
		if err != nil {
			t.Fatalf("parse(%v): %v", c.args, err)
		}
		if opts.Dir != c.want {
			t.Errorf("parse(%v) Dir = %q, want %q", c.args, opts.Dir, c.want)
		}
	}
	if _, err := parseModuleArgs([]string{"url", "--dir"}); err == nil {
		t.Error("expected error for --dir with no value")
	}
}

// --- unsetRequiredVars ---

func TestUnsetRequiredVars(t *testing.T) {
	state := &wrapper.State{
		Vars: []tfvars.Variable{
			{Name: "required_unset"},
			{Name: "required_set"},
			{Name: "required_null"},
			{Name: "optional", HasDefault: true, Default: cty.StringVal("x")},
		},
		Values: map[string]cty.Value{
			"required_set":  cty.StringVal("v"),
			"required_null": cty.NullVal(cty.String),
		},
	}
	got := unsetRequiredVars(state)
	want := []string{"required_unset", "required_null"}
	if !slices.Equal(got, want) {
		t.Errorf("unsetRequiredVars = %v, want %v", got, want)
	}
}

func TestUnsetRequiredVars_none(t *testing.T) {
	state := &wrapper.State{
		Vars:   []tfvars.Variable{{Name: "a"}, {Name: "b", HasDefault: true, Default: cty.NumberIntVal(1)}},
		Values: map[string]cty.Value{"a": cty.StringVal("v")},
	}
	if got := unsetRequiredVars(state); len(got) != 0 {
		t.Errorf("unsetRequiredVars = %v, want none", got)
	}
}

// --- checkApplyTarget ---

func TestCheckApplyTarget(t *testing.T) {
	base := t.TempDir()

	if err := checkApplyTarget(filepath.Join(base, "new")); err != nil {
		t.Errorf("missing target: %v", err)
	}

	empty := filepath.Join(base, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkApplyTarget(empty); err != nil {
		t.Errorf("empty target: %v", err)
	}

	nonEmpty := filepath.Join(base, "full")
	if err := os.Mkdir(nonEmpty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "main.tf"), []byte("# existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkApplyTarget(nonEmpty); err == nil {
		t.Error("expected an error for a non-empty target")
	}

	file := filepath.Join(base, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkApplyTarget(file); err == nil {
		t.Error("expected an error when the target is a file")
	}
}
