package wrapper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/canonical/atelier/internal/tftypes"
	"github.com/canonical/atelier/internal/tfvars"
)

func mustVar(t *testing.T, name, typeSrc string, def cty.Value, hasDef bool) tfvars.Variable {
	t.Helper()
	tp, err := tftypes.ParseTypeExpr(typeSrc)
	if err != nil {
		t.Fatalf("parse type %q: %v", typeSrc, err)
	}
	return tfvars.Variable{
		Name:       name,
		Type:       tp,
		HasDefault: hasDef,
		Default:    def,
		Nullable:   true,
	}
}

func TestWrite_freshWrapper_sparseOutput(t *testing.T) {
	dir := t.TempDir()
	s := &State{
		Dir:             dir,
		ModuleBlockName: "cos_lite",
		Source:          "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=main",
		Vars: []tfvars.Variable{
			mustVar(t, "model_uuid", "string", cty.NilVal, false),                // required
			mustVar(t, "internal_tls", "bool", cty.True, true),                   // optional with default true
			mustVar(t, "alertmanager", "object({units = optional(number, 1)})", cty.EmptyObjectVal, true), // optional object
		},
		Values: map[string]cty.Value{
			"model_uuid": cty.StringVal("abc-123"),
			// internal_tls left at default (true)
			"alertmanager": cty.ObjectVal(map[string]cty.Value{
				"units": cty.NumberIntVal(3),
			}),
		},
	}
	if err := s.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, MainTF))
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if !strings.Contains(out, `module "cos_lite"`) {
		t.Errorf("expected module block; got:\n%s", out)
	}
	if !strings.Contains(out, "source") || !strings.Contains(out, "?ref=main") {
		t.Errorf("source missing; got:\n%s", out)
	}
	if !strings.Contains(out, `model_uuid`) {
		t.Errorf("required model_uuid missing; got:\n%s", out)
	}
	if strings.Contains(out, "internal_tls") {
		t.Errorf("internal_tls should NOT appear (at default); got:\n%s", out)
	}
	if !strings.Contains(out, "alertmanager") {
		t.Errorf("alertmanager (changed) should appear; got:\n%s", out)
	}
	if !strings.Contains(out, "units") {
		t.Errorf("units field should appear in alertmanager; got:\n%s", out)
	}
}

func TestWrite_roundTrip_preservesUnknownArguments(t *testing.T) {
	dir := t.TempDir()
	initial := `module "cos_lite" {
  source     = "git::https://example.com/m.git?ref=v1"
  count      = 2
  providers  = { juju = juju }
  model_uuid = "old"
}
`
	if err := os.WriteFile(filepath.Join(dir, MainTF), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &State{
		Dir:             dir,
		ModuleBlockName: "cos_lite",
		Source:          "git::https://example.com/m.git?ref=v2",
		Vars: []tfvars.Variable{
			mustVar(t, "model_uuid", "string", cty.NilVal, false),
		},
		Values: map[string]cty.Value{
			"model_uuid": cty.StringVal("new"),
		},
	}
	if err := s.Write(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, MainTF))
	out := string(got)

	if !strings.Contains(out, "count") {
		t.Errorf("unknown attribute `count` should survive; got:\n%s", out)
	}
	if !strings.Contains(out, "providers") {
		t.Errorf("unknown attribute `providers` should survive; got:\n%s", out)
	}
	if !strings.Contains(out, `model_uuid = "new"`) && !strings.Contains(out, `"new"`) {
		t.Errorf("model_uuid should be updated to \"new\"; got:\n%s", out)
	}
	if !strings.Contains(out, "?ref=v2") {
		t.Errorf("source should be updated to v2; got:\n%s", out)
	}
}

func TestWrite_atDefault_removesAttribute(t *testing.T) {
	dir := t.TempDir()
	initial := `module "x" {
  source       = "git::https://example.com/m.git?ref=v1"
  internal_tls = false
}
`
	if err := os.WriteFile(filepath.Join(dir, MainTF), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &State{
		Dir:             dir,
		ModuleBlockName: "x",
		Source:          "git::https://example.com/m.git?ref=v1",
		Vars: []tfvars.Variable{
			mustVar(t, "internal_tls", "bool", cty.True, true),
		},
		Values: map[string]cty.Value{
			"internal_tls": cty.True, // revert to default
		},
	}
	if err := s.Write(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, MainTF))
	if strings.Contains(string(got), "internal_tls") {
		t.Errorf("reverted-to-default attribute should be removed; got:\n%s", got)
	}
}

func TestWrite_preservesComments(t *testing.T) {
	dir := t.TempDir()
	initial := `# Top-level comment.
module "cos" {
  # inline comment
  source     = "git::https://example.com/m.git?ref=v1"
  model_uuid = "x"
}

# trailing comment outside the module
`
	if err := os.WriteFile(filepath.Join(dir, MainTF), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &State{
		Dir:             dir,
		ModuleBlockName: "cos",
		Source:          "git::https://example.com/m.git?ref=v1",
		Vars: []tfvars.Variable{
			mustVar(t, "model_uuid", "string", cty.NilVal, false),
		},
		Values: map[string]cty.Value{
			"model_uuid": cty.StringVal("y"),
		},
	}
	if err := s.Write(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, MainTF))
	out := string(got)
	if !strings.Contains(out, "Top-level comment.") {
		t.Errorf("top-level comment lost; got:\n%s", out)
	}
	if !strings.Contains(out, "trailing comment outside the module") {
		t.Errorf("trailing comment lost; got:\n%s", out)
	}
	if !strings.Contains(out, "inline comment") {
		t.Errorf("inline comment lost; got:\n%s", out)
	}
}

func TestReadMain_recoversValues(t *testing.T) {
	dir := t.TempDir()
	content := `module "cos" {
  source       = "git::https://example.com/m.git?ref=v1"
  model_uuid   = "abc"
  internal_tls = false
}
`
	if err := os.WriteFile(filepath.Join(dir, MainTF), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	vars := []tfvars.Variable{
		mustVar(t, "model_uuid", "string", cty.NilVal, false),
		mustVar(t, "internal_tls", "bool", cty.True, true),
	}
	pm, err := ReadMain(dir, vars)
	if err != nil {
		t.Fatal(err)
	}
	if pm.ModuleBlockName != "cos" {
		t.Errorf("module name = %q", pm.ModuleBlockName)
	}
	if pm.Source != "git::https://example.com/m.git?ref=v1" {
		t.Errorf("source = %q", pm.Source)
	}
	if v, ok := pm.Values["model_uuid"]; !ok || v.AsString() != "abc" {
		t.Errorf("model_uuid: ok=%v v=%v", ok, v.GoString())
	}
	if v, ok := pm.Values["internal_tls"]; !ok || v.True() {
		t.Errorf("internal_tls: ok=%v v=%v", ok, v.GoString())
	}
}

func TestReadMain_missing_returnsNil(t *testing.T) {
	pm, err := ReadMain(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if pm != nil {
		t.Errorf("expected nil ParsedMain, got %+v", pm)
	}
}

func TestSecrets_roundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &State{
		Dir: dir,
		SecretValues: map[string]cty.Value{
			"juju_password": cty.StringVal("hunter2"),
			"juju_username": cty.StringVal("admin"),
		},
	}
	if err := s.writeSecrets(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got["juju_password"].AsString() != "hunter2" {
		t.Errorf("juju_password not round-tripped: %v", got["juju_password"].GoString())
	}
	if got["juju_username"].AsString() != "admin" {
		t.Errorf("juju_username: %v", got["juju_username"].GoString())
	}
}

func TestReadSecrets_missing_returnsEmpty(t *testing.T) {
	got, err := ReadSecrets(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %d entries", len(got))
	}
}
