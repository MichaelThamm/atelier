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

// tfVarsVars parses variable declarations from source so the Raw block
// sources (which variables.tf mirroring depends on) are populated exactly as
// they would be for a real module. The source is written to disk first so
// type expressions and Raw are recovered from a real file, as in production.
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

func TestRenderVariablesTF_preservesValidationAndDefaults(t *testing.T) {
	vars := tfVarsVars(t, `
variable "name" {
  type        = string
  description = "A name."
  validation {
    condition     = length(var.name) > 0
    error_message = "must not be empty"
  }
}
variable "replicas" {
  type    = number
  default = 1
}
`)
	s := &State{Dir: t.TempDir(), Vars: vars}
	out := string(s.RenderVariablesTF())
	for _, want := range []string{`variable "name"`, "validation", "error_message", `variable "replicas"`, "default = 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("variables.tf missing %q; got:\n%s", want, out)
		}
	}
}

func TestWrite_tfvarsMode_isSparseAndForwards(t *testing.T) {
	vars := tfVarsVars(t, `
variable "model_uuid" {
  type = string
}
variable "internal_tls" {
  type    = bool
  default = true
}
variable "replicas" {
  type    = number
  default = 1
}
`)
	dir := t.TempDir()
	s := &State{
		Dir:             dir,
		ModuleBlockName: "cos_lite",
		Source:          "git::https://example.com/m.git?ref=v1",
		Vars:            vars,
		TFVarsMode:      true,
		Values: map[string]cty.Value{
			"model_uuid":   cty.StringVal("abc-123"),
			"internal_tls": cty.False,
		},
	}
	if err := s.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}

	tfVars := readFile(t, filepath.Join(dir, TFVarsFile))
	if !strings.Contains(tfVars, "Atelier-managed values") {
		t.Errorf("terraform.tfvars lost its provenance header; got:\n%s", tfVars)
	}
	mustMatch(t, tfVars, `model_uuid\s*=\s*"abc-123"`)
	mustMatch(t, tfVars, `internal_tls\s*=\s*false`)
	if strings.Contains(tfVars, "replicas") {
		t.Errorf("terraform.tfvars should stay sparse (replicas at default); got:\n%s", tfVars)
	}

	varsTF := readFile(t, filepath.Join(dir, VariablesTF))
	if !strings.Contains(varsTF, `variable "model_uuid"`) {
		t.Errorf("variables.tf missing mirrored block; got:\n%s", varsTF)
	}

	main := readFile(t, filepath.Join(dir, MainTF))
	if !strings.Contains(main, "atelier:tfvars") {
		t.Errorf("main.tf missing mode marker; got:\n%s", main)
	}
	mustMatch(t, main, `model_uuid\s*=\s*var\.model_uuid`)
	mustMatch(t, main, `internal_tls\s*=\s*var\.internal_tls`)
	mustMatch(t, main, `replicas\s*=\s*var\.replicas`)

	if !IsTFVarsMode(dir) {
		t.Error("IsTFVarsMode = false after writing a pass-through wrapper")
	}
}

func TestReadTFVars_roundTrip(t *testing.T) {
	vars := tfVarsVars(t, `
variable "model_uuid" {
  type = string
}
variable "internal_tls" {
  type    = bool
  default = true
}
variable "replicas" {
  type    = number
  default = 1
}
`)
	dir := t.TempDir()
	s := &State{
		Dir:             dir,
		ModuleBlockName: "cos_lite",
		Source:          "git::https://example.com/m.git?ref=v1",
		Vars:            vars,
		TFVarsMode:      true,
		Values: map[string]cty.Value{
			"model_uuid":   cty.StringVal("abc-123"),
			"internal_tls": cty.False,
		},
	}
	if err := s.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := ReadTFVars(dir, vars)
	if err != nil {
		t.Fatalf("ReadTFVars: %v", err)
	}
	if v, ok := got["model_uuid"]; !ok || v.AsString() != "abc-123" {
		t.Errorf("model_uuid = %#v", got["model_uuid"])
	}
	if v, ok := got["internal_tls"]; !ok || v.True() {
		t.Errorf("internal_tls = %#v", got["internal_tls"])
	}
	if _, ok := got["replicas"]; ok {
		t.Errorf("replicas should not be materialized at default; got %#v", got["replicas"])
	}
}

func TestIsTFVarsMode_falseForClassicWrapper(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, MainTF), []byte(`module "m" {
  source = "git::https://example.com/m.git"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsTFVarsMode(dir) {
		t.Error("classic main.tf detected as tfvars mode")
	}
}

func TestFilterPassthroughAttrs(t *testing.T) {
	vars := tfVarsVars(t, `variable "risk" { type = string }`)
	unknown := []RawAttr{
		{Name: "risk", RawExpr: []byte("var.risk")},             // generated forward — drop
		{Name: "count", RawExpr: []byte("2")},                   // meta-argument — keep
		{Name: "model", RawExpr: []byte("module.a.model_name")}, // wired expression — keep
	}
	got := FilterPassthroughAttrs(unknown, vars)
	names := map[string]bool{}
	for _, ra := range got {
		names[ra.Name] = true
	}
	if names["risk"] {
		t.Error("generated forward `risk = var.risk` should be filtered out")
	}
	if !names["count"] || !names["model"] {
		t.Errorf("meta/wired attrs should survive; got %+v", got)
	}
}

func TestRenderPassthroughMain_preservesWiredExpression(t *testing.T) {
	vars := tfVarsVars(t, `variable "model_name" { type = string }`)
	s := &State{
		Dir:             t.TempDir(),
		ModuleBlockName: "alerting",
		Source:          "git::https://example.com/m.git?ref=v1",
		Vars:            vars,
		TFVarsMode:      true,
		UnknownAttrs: []RawAttr{
			{Name: "model_name", RawExpr: []byte("module.cos_lite.model_name")},
		},
	}
	out := string(s.RenderPassthroughMain())
	mustMatch(t, out, `model_name\s*=\s*module\.cos_lite\.model_name`)
	if regexp.MustCompile(`model_name\s*=\s*var\.model_name`).MatchString(out) {
		t.Errorf("wired variable should not forward from var; got:\n%s", out)
	}
}

func TestReadTFVarsFile_arbitraryName(t *testing.T) {
	vars := tfVarsVars(t, `variable "name" { type = string }`)
	path := filepath.Join(t.TempDir(), "test.tfvars")
	if err := os.WriteFile(path, []byte("name = \"from-file\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadTFVarsFile(path, vars)
	if err != nil {
		t.Fatalf("ReadTFVarsFile: %v", err)
	}
	if v, ok := got["name"]; !ok || v.AsString() != "from-file" {
		t.Errorf("name = %#v, want from-file", got["name"])
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

// TestWrite_tfvarsMode_idempotent guards the TUI's auto-save: Write is called
// after every edit, so a second Write with unchanged values must produce
// byte-identical files, or the wrapper churns on disk and in git.
func TestWrite_tfvarsMode_idempotent(t *testing.T) {
	vars := tfVarsVars(t, `
variable "name" { type = string }
variable "internal_tls" {
  type    = bool
  default = true
}
variable "labels" {
  type    = map(string)
  default = {}
}
variable "alertmanager" {
  type = object({
    app_name = optional(string, "alertmanager")
    units    = optional(number, 1)
  })
  default = {}
}
`)
	dir := t.TempDir()
	s := &State{
		Dir:             dir,
		ModuleBlockName: "cos",
		Source:          "git::https://example.com/m.git?ref=v1",
		Vars:            vars,
		TFVarsMode:      true,
		Values: map[string]cty.Value{
			"name":         cty.StringVal("demo"),
			"internal_tls": cty.False,
			"labels":       cty.MapVal(map[string]cty.Value{"env": cty.StringVal("dev")}),
			"alertmanager": cty.ObjectVal(map[string]cty.Value{"units": cty.NumberIntVal(3)}),
		},
	}
	if err := s.Write(); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	first := map[string]string{}
	for _, f := range []string{MainTF, VariablesTF, TFVarsFile} {
		first[f] = readFile(t, filepath.Join(dir, f))
	}

	if err := s.Write(); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	for _, f := range []string{MainTF, VariablesTF, TFVarsFile} {
		if got := readFile(t, filepath.Join(dir, f)); got != first[f] {
			t.Errorf("%s changed on an unchanged second Write:\n--- first ---\n%s\n--- second ---\n%s", f, first[f], got)
		}
	}
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

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
