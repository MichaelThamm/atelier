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
// they would be for a real module.
func tfVarsVars(t *testing.T, src string) []tfvars.Variable {
	t.Helper()
	vars, err := tfvars.Parse([]byte(src), "variables.tf")
	if err != nil {
		t.Fatalf("parse variables: %v", err)
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

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
