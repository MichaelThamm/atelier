package wrapper

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// mustMatch asserts that out matches the whitespace-tolerant pattern.
func mustMatch(t *testing.T, out, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(out) {
		t.Errorf("output missing /%s/; got:\n%s", pattern, out)
	}
}

// tfVarsVars parses variable declarations from source so the type metadata is
// recovered exactly as it would be for a real module. The source is written to
// disk first so type expressions are recovered from a real file.
func tfVarsVars(t *testing.T, src string) []tfvars.Variable {
	t.Helper()
	path := filepath.Join(t.TempDir(), "variables.tf")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write variables: %v", err)
	}
	vars, err := tfvars.LoadFile(path)
	if err != nil {
		t.Fatalf("load variables: %v", err)
	}
	return vars
}

// TestReadTFVarsFileChecked_conversions is the false-positive guard for binding
// diagnostics: ordinary typed values (map, list, bool, number) must be accepted
// and coerced, not reported as mismatches. A false warning here would make
// `--var-file` noisy for correct files.
func TestReadTFVarsFileChecked_conversions(t *testing.T) {
	vars := tfVarsVars(t, `
variable "name" { type = string }
variable "labels" { type = map(string) }
variable "tags" { type = list(string) }
variable "flag" { type = bool }
variable "count" { type = number }
variable "ref" { type = string }
`)
	path := filepath.Join(t.TempDir(), "ok.tfvars")
	if err := os.WriteFile(path, []byte(`
name   = "demo"
labels = { env = "dev" }
tags   = ["a", "b"]
flag   = true
count  = 2
ref    = module.other.value
`), 0o644); err != nil {
		t.Fatal(err)
	}

	vals, diags, err := ReadTFVarsFileChecked(path, vars)
	if err != nil {
		t.Fatalf("ReadTFVarsFileChecked: %v", err)
	}
	if !diags.Empty() {
		t.Fatalf("correctly-typed values must not warn: %+v", diags)
	}
	if v := vals["name"]; v.AsString() != "demo" {
		t.Errorf("name = %#v", v)
	}
	if v, ok := vals["labels"]; !ok || !v.Type().IsMapType() {
		t.Errorf("labels not coerced to a map: %#v", vals["labels"])
	}
	if v, ok := vals["tags"]; !ok || !v.Type().IsListType() {
		t.Errorf("tags not coerced to a list: %#v", vals["tags"])
	}
	if v, ok := vals["flag"]; !ok || !v.True() {
		t.Errorf("flag = %#v", vals["flag"])
	}
	if v, ok := vals["count"]; !ok || v.AsBigFloat().String() != "2" {
		t.Errorf("count = %#v", vals["count"])
	}
	if _, ok := vals["ref"]; ok {
		t.Error("a reference expression is not valid in .tfvars and must be skipped")
	}
}

func TestReadTFVarsFileChecked_reportsUnknownAndMismatch(t *testing.T) {
	vars := tfVarsVars(t, `
variable "name" { type = string }
variable "replicas" {
  type    = number
  default = 1
}
`)
	path := filepath.Join(t.TempDir(), "x.tfvars")
	if err := os.WriteFile(path, []byte("bogus = 1\nreplicas = \"three\"\nname = \"ok\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vals, diags, err := ReadTFVarsFileChecked(path, vars)
	if err != nil {
		t.Fatalf("ReadTFVarsFileChecked: %v", err)
	}
	if len(diags.Unknown) != 1 || diags.Unknown[0] != "bogus" {
		t.Errorf("unknown = %v, want [bogus]", diags.Unknown)
	}
	if len(diags.Mismatched) != 1 {
		t.Errorf("mismatched = %v, want one entry", diags.Mismatched)
	}
	if _, ok := vals["replicas"]; ok {
		t.Error("type-mismatched value must be skipped, not returned")
	}
	if v, ok := vals["name"]; !ok || v.AsString() != "ok" {
		t.Errorf("name = %#v, want ok", vals["name"])
	}
}

func TestReadTFVarsFileChecked_syntaxError(t *testing.T) {
	vars := tfVarsVars(t, `variable "name" { type = string }`)
	path := filepath.Join(t.TempDir(), "broken.tfvars")
	if err := os.WriteFile(path, []byte("name = \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadTFVarsFileChecked(path, vars); err == nil {
		t.Error("a syntactically invalid .tfvars must be an error, not silently empty")
	}
}

// TestRenderTFVarsValues_sparse covers the TUI `S` sink directly: required
// values are written, changed optionals are written, object values are written
// partially (only fields that differ from their optional() default), and
// at-default values are omitted.
func TestRenderTFVarsValues_sparse(t *testing.T) {
	vars := tfVarsVars(t, `
variable "name" { type = string }
variable "internal_tls" {
  type    = bool
  default = true
}
variable "alertmanager" {
  type = object({
    app_name = optional(string, "alertmanager")
    units    = optional(number, 1)
  })
  default = {}
}
variable "labels" {
  type    = map(string)
  default = {}
}
`)
	out := string(RenderTFVarsValues(vars, map[string]cty.Value{
		"name":         cty.StringVal("demo"),
		"internal_tls": cty.True, // at default -> omitted
		"alertmanager": cty.ObjectVal(map[string]cty.Value{
			"app_name": cty.StringVal("alertmanager"), // at default -> omitted
			"units":    cty.NumberIntVal(3),
		}),
		"labels": cty.MapValEmpty(cty.String), // at default -> omitted
	}))
	if !strings.Contains(out, `name = "demo"`) {
		t.Errorf("required value missing:\n%s", out)
	}
	mustMatch(t, out, `units\s*=\s*3`)
	if strings.Contains(out, "internal_tls") {
		t.Errorf("at-default internal_tls must be omitted:\n%s", out)
	}
	if strings.Contains(out, "app_name") {
		t.Errorf("at-default object field must be omitted:\n%s", out)
	}
	if strings.Contains(out, "labels") {
		t.Errorf("at-default map must be omitted:\n%s", out)
	}
}
