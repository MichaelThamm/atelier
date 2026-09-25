package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zclconf/go-cty/cty"

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
		opts, err := parseModuleAddArgs(c.args)
		if err != nil {
			t.Fatalf("parse(%v): %v", c.args, err)
		}
		if !slices.Equal(opts.VarFiles, c.want) {
			t.Errorf("parse(%v) var files = %v, want %v", c.args, opts.VarFiles, c.want)
		}
	}
	if _, err := parseModuleAddArgs([]string{"url", "--var-file"}); err == nil {
		t.Error("expected error for --var-file with no value")
	}
}

func TestParseModuleAddArgs_VarFileCommaList(t *testing.T) {
	opts, err := parseModuleAddArgs([]string{"url", "--var-file", "a,b", "--var-file=c"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c"}; !slices.Equal(opts.VarFiles, want) {
		t.Errorf("VarFiles = %v, want %v", opts.VarFiles, want)
	}
}

// --- applyVarFiles ---

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
	warns, err := applyVarFiles(s, []string{a, b}, false)
	if err != nil {
		t.Fatalf("applyVarFiles: %v", err)
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
	if _, err := applyVarFiles(s, []string{filepath.Join(dir, "missing.tfvars")}, false); err == nil {
		t.Error("expected an error for a missing --var-file")
	}
}

// TestApplyVarFiles_warnsAndStrictFails pins the ADR-0032 binding behaviour:
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
	warns, err := applyVarFiles(s, []string{f}, false)
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
	if _, err := applyVarFiles(s, []string{f}, true); err == nil {
		t.Error("--strict should make binding problems fatal")
	}
}

func TestParseModuleAddArgs_ListVarFiles(t *testing.T) {
	opts, err := parseModuleAddArgs([]string{"url", "--list-var-files"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.ListVarFiles {
		t.Error("ListVarFiles not set")
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
