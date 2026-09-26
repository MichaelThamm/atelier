package tfvars

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
)

const sampleTF = `
variable "name" {
  type        = string
  default     = "hello"
  description = "A friendly name."
}

variable "model_uuid" {
  type = string
}

variable "internal_tls" {
  type    = bool
  default = true
}

variable "alertmanager" {
  type = object({
    app_name    = optional(string, "alertmanager")
    units       = optional(number, 1)
    constraints = optional(string, "arch=amd64")
  })
  default = {}
}

variable "password" {
  type      = string
  sensitive = true
}
`

func writeTF(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadFile_basic(t *testing.T) {
	dir := t.TempDir()
	p := writeTF(t, dir, "variables.tf", sampleTF)

	vars, err := LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 5 {
		t.Fatalf("expected 5 variables, got %d", len(vars))
	}

	byName := map[string]Variable{}
	for _, v := range vars {
		byName[v.Name] = v
	}

	if v := byName["name"]; v.Type.Kind != tftypes.KindString || !v.HasDefault || v.Default.AsString() != "hello" {
		t.Errorf("name: %+v", v)
	}
	if byName["name"].Description != "A friendly name." {
		t.Errorf("description: %q", byName["name"].Description)
	}

	if v := byName["model_uuid"]; v.HasDefault {
		t.Errorf("model_uuid should be required (no default), got HasDefault=%v", v.HasDefault)
	}
	mu := byName["model_uuid"]
	if !mu.IsRequired() {
		t.Errorf("model_uuid should be required")
	}

	if v := byName["internal_tls"]; v.Type.Kind != tftypes.KindBool || !v.HasDefault || !v.Default.True() {
		t.Errorf("internal_tls: %+v", v)
	}

	if v := byName["alertmanager"]; v.Type.Kind != tftypes.KindObject || len(v.Type.AttrOrder) != 3 {
		t.Errorf("alertmanager type: %+v", v.Type)
	}
	if !byName["alertmanager"].HasDefault {
		t.Errorf("alertmanager should have a default (={})")
	}

	if v := byName["password"]; !v.Sensitive {
		t.Errorf("password should be sensitive")
	}
	if v := byName["password"]; v.HasDefault {
		t.Errorf("password should be required (no default)")
	}
}

func TestLoadDir_acrossFiles(t *testing.T) {
	dir := t.TempDir()
	writeTF(t, dir, "a.tf", `variable "a" { type = string }`)
	writeTF(t, dir, "b.tf", `variable "b" { type = number }`)
	// Should be skipped (test file suffix).
	writeTF(t, dir, "x_test.tf", `variable "skipped" { type = string }`)

	vars, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 2 {
		t.Fatalf("expected 2 variables, got %d: %+v", len(vars), vars)
	}
	if vars[0].Name != "a" || vars[1].Name != "b" {
		t.Errorf("expected file-order [a, b], got [%s, %s]", vars[0].Name, vars[1].Name)
	}
}

func TestLoadDir_subdirsIgnored(t *testing.T) {
	dir := t.TempDir()
	writeTF(t, dir, "main.tf", `variable "top" { type = string }`)
	sub := filepath.Join(dir, "subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTF(t, sub, "vars.tf", `variable "should_not_load" { type = string }`)

	vars, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 1 || vars[0].Name != "top" {
		t.Errorf("subdir leaked into top-level scan: %+v", vars)
	}
}

func TestParse_nullable(t *testing.T) {
	src := `
variable "strict" {
  type     = string
  nullable = false
}
variable "loose" {
  type = string
}
`
	vars, err := Parse([]byte(src), "test.tf")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Variable{}
	for _, v := range vars {
		byName[v.Name] = v
	}
	if byName["strict"].Nullable {
		t.Errorf("strict should have nullable=false")
	}
	if !byName["loose"].Nullable {
		t.Errorf("loose should have nullable=true (default)")
	}
}

func TestParse_listAndMapDefaults(t *testing.T) {
	src := `
variable "tags" {
  type    = list(string)
  default = ["alpha", "beta"]
}
variable "labels" {
  type    = map(string)
  default = { env = "dev", team = "obs" }
}
`
	vars, err := Parse([]byte(src), "test.tf")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Variable{}
	for _, v := range vars {
		byName[v.Name] = v
	}
	tags := byName["tags"]
	if tags.Type.Kind != tftypes.KindList || tags.Type.Element.Kind != tftypes.KindString {
		t.Errorf("tags type: %+v", tags.Type)
	}
	if !tags.HasDefault || tags.Default.LengthInt() != 2 {
		t.Errorf("tags default: %+v", tags.Default.GoString())
	}

	labels := byName["labels"]
	if labels.Type.Kind != tftypes.KindMap {
		t.Errorf("labels type: %+v", labels.Type)
	}
	if !labels.HasDefault || labels.Default.LengthInt() != 2 {
		t.Errorf("labels default: %+v", labels.Default.GoString())
	}
	m := labels.Default.AsValueMap()
	if m["env"].AsString() != "dev" {
		t.Errorf("labels[env] = %v", m["env"].GoString())
	}
}

func TestParse_anyType(t *testing.T) {
	src := `variable "passthrough" {}`
	vars, err := Parse([]byte(src), "test.tf")
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 1 || vars[0].Type.Kind != tftypes.KindAny {
		t.Errorf("expected any type, got %+v", vars[0].Type)
	}
}

func TestParse_validationBlocksIgnored(t *testing.T) {
	src := `
variable "url" {
  type = string
  validation {
    condition     = length(var.url) > 0
    error_message = "Must not be empty."
  }
}
`
	vars, err := Parse([]byte(src), "test.tf")
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 1 || vars[0].Name != "url" {
		t.Fatalf("got %+v", vars)
	}
}

func TestVariable_DefaultEqualsCty(t *testing.T) {
	src := "variable \"n\" {\n  type = number\n  default = 7\n}"
	vars, err := Parse([]byte(src), "test.tf")
	if err != nil {
		t.Fatal(err)
	}
	if !vars[0].Default.Equals(cty.NumberIntVal(7)).True() {
		t.Errorf("default = %v", vars[0].Default.GoString())
	}
}
