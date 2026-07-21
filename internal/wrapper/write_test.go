package wrapper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
)

func TestWrite_freshWrapper_sparseOutput(t *testing.T) {
	dir := t.TempDir()
	s := &State{
		Dir:             dir,
		ModuleBlockName: "cos_lite",
		Source:          "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=main",
		Vars: []tfvars.Variable{
			mustVar(t, "model_uuid", "string", cty.NilVal, false),                                         // required
			mustVar(t, "internal_tls", "bool", cty.True, true),                                            // optional with default true
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

// TestWrite_preservesDeclaredVarSetToExpression is a regression test for a
// data-loss bug: a DECLARED module variable assigned an HCL expression Atelier
// can't evaluate (a data-source reference, var/local/module reference, or a
// function call) was silently deleted on save, because writeMain rebuilt every
// declared variable purely from its cty value and removed the attribute when
// there was none. Such expressions live in UnknownAttrs and must survive.
func TestWrite_preservesDeclaredVarSetToExpression(t *testing.T) {
	dir := t.TempDir()
	initial := `module "cos" {
  source        = "git::https://example.com/o.git//terraform/cos?ref=v1"
  risk          = "stable"
  s3_endpoint   = data.vault_generic_secret.s3.data["endpoint_url"]
  s3_access_key = data.vault_generic_secret.s3.data["access_key"]
}
`
	if err := os.WriteFile(filepath.Join(dir, MainTF), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := []tfvars.Variable{
		mustVar(t, "risk", "string", cty.StringVal("stable"), true), // optional, at default
		mustVar(t, "s3_endpoint", "string", cty.NilVal, false),      // required, not sensitive
		mustVar(t, "s3_access_key", "string", cty.NilVal, false),    // required (sensitive in real module)
	}

	// Read the existing wrapper the way the runtime does, so the reference
	// expressions land in UnknownAttrs and the literal `risk` lands in Values.
	pm, err := ReadMain(dir, vars)
	if err != nil {
		t.Fatal(err)
	}
	s := &State{
		Dir:             dir,
		ModuleBlockName: "cos",
		Source:          "git::https://example.com/o.git//terraform/cos?ref=v1",
		Vars:            vars,
		Values:          pm.Values,
		UnknownAttrs:    pm.UnknownAttrs,
	}

	if err := s.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, MainTF))
	out := string(got)

	for _, want := range []string{
		"s3_endpoint",
		"s3_access_key",
		`data.vault_generic_secret.s3.data["endpoint_url"]`,
		`data.vault_generic_secret.s3.data["access_key"]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q to survive save, but it was lost; got:\n%s", want, out)
		}
	}
}

// TestWrite_reEmitsExpressionFromScratch pins improvement #2: the writer
// re-emits a preserved reference expression explicitly from RawExpr bytes, so
// it survives even a from-scratch write (no pre-existing main.tf to pass
// through). It also exercises an index expression to pin improvement #1.
func TestWrite_reEmitsExpressionFromScratch(t *testing.T) {
	dir := t.TempDir() // intentionally NO existing main.tf
	s := &State{
		Dir:             dir,
		ModuleBlockName: "cos",
		Source:          "git::https://example.com/o.git//terraform/cos?ref=v1",
		Vars: []tfvars.Variable{
			mustVar(t, "s3_endpoint", "string", cty.NilVal, false),
		},
		UnknownAttrs: []RawAttr{
			{
				Name:    "s3_endpoint",
				Raw:     []byte(`s3_endpoint = data.vault_generic_secret.s3.data["endpoint_url"]`),
				RawExpr: []byte(`data.vault_generic_secret.s3.data["endpoint_url"]`),
			},
		},
	}
	if err := s.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, MainTF))
	out := string(got)
	if !strings.Contains(out, `s3_endpoint`) {
		t.Errorf("s3_endpoint should be emitted from scratch; got:\n%s", out)
	}
	if !strings.Contains(out, `data.vault_generic_secret.s3.data["endpoint_url"]`) {
		t.Errorf("index expression should round-trip unquoted; got:\n%s", out)
	}
	if strings.Contains(out, `"data.vault_generic_secret`) {
		t.Errorf("expression must not be quoted as a string literal; got:\n%s", out)
	}
}

// TestWrite_concreteValueSupersedesRawExpression pins that once a concrete
// value is present in Values, it wins over the preserved expression.
func TestWrite_concreteValueSupersedesRawExpression(t *testing.T) {
	dir := t.TempDir()
	initial := `module "cos" {
  source      = "git::https://example.com/o.git//terraform/cos?ref=v1"
  s3_endpoint = data.vault_generic_secret.s3.data["endpoint_url"]
}
`
	if err := os.WriteFile(filepath.Join(dir, MainTF), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &State{
		Dir:             dir,
		ModuleBlockName: "cos",
		Source:          "git::https://example.com/o.git//terraform/cos?ref=v1",
		Vars: []tfvars.Variable{
			mustVar(t, "s3_endpoint", "string", cty.NilVal, false),
		},
		Values: map[string]cty.Value{
			"s3_endpoint": cty.StringVal("https://s3.example.com"),
		},
		UnknownAttrs: []RawAttr{
			{
				Name:    "s3_endpoint",
				Raw:     []byte(`s3_endpoint = data.vault_generic_secret.s3.data["endpoint_url"]`),
				RawExpr: []byte(`data.vault_generic_secret.s3.data["endpoint_url"]`),
			},
		},
	}
	if err := s.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, MainTF))
	out := string(got)
	if !strings.Contains(out, `"https://s3.example.com"`) {
		t.Errorf("concrete value should win; got:\n%s", out)
	}
	if strings.Contains(out, "vault_generic_secret") {
		t.Errorf("stale expression should not be re-emitted once overridden; got:\n%s", out)
	}
}

// TestWrite_prunesOrphanedArgumentAfterRefSwitch is a regression test for the
// bug where switching a module to a ref that dropped a variable left the stale
// argument in main.tf. Reproduces observability-stack track/2 → main, where
// `model_uuid` no longer exists; the leftover `model_uuid = null` made
// `terraform init` fail with "An argument named model_uuid is not expected
// here."
func TestWrite_prunesOrphanedArgumentAfterRefSwitch(t *testing.T) {
	dir := t.TempDir()
	initial := `module "cos_lite" {
  source     = "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=track/2"
  model_uuid = null
  model = {
    name = "demo"
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, MainTF), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	// New schema (ref=main) no longer declares model_uuid.
	s := &State{
		Dir:             dir,
		ModuleBlockName: "cos_lite",
		Source:          "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=main",
		Vars: []tfvars.Variable{
			mustVar(t, "model", "object({name = string})", cty.NilVal, false),
		},
		Values: map[string]cty.Value{
			"model": cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("demo")}),
		},
	}
	if err := s.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, MainTF))
	out := string(got)
	if strings.Contains(out, "model_uuid") {
		t.Errorf("orphaned model_uuid should be removed after ref switch; got:\n%s", out)
	}
	if !strings.Contains(out, "?ref=main") {
		t.Errorf("source should be updated to main; got:\n%s", out)
	}
	if !strings.Contains(out, `name = "demo"`) {
		t.Errorf("still-declared model var should survive; got:\n%s", out)
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

func TestIsExpressionRef(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"module.cos.model_uuid", true},
		{"var.region", true},
		{"local.tags", true},
		{"data.juju_model.m.uuid", true},
		{`data.vault_generic_secret.s3.data["endpoint_url"]`, true}, // index expression
		{"module.net.subnets[0]", true},                             // numeric index
		{"stable", false},                                           // plain string literal
		{"hello.world", false},                                      // dotted but not a known scope
		{"", false},                                                 // empty
		{"variable.x", false},                                       // not a real scope prefix
		{"datadog.thing", false},                                    // 'data' prefix but not 'data.'
	}
	for _, c := range cases {
		if got := isExpressionRef(c.in); got != c.want {
			t.Errorf("isExpressionRef(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}

func TestReadMain_referencePopulatesRawExpr(t *testing.T) {
	dir := t.TempDir()
	content := `module "cos" {
  source      = "git::https://example.com/m.git?ref=v1"
  s3_endpoint = data.vault_generic_secret.s3.data["endpoint_url"]
}
`
	if err := os.WriteFile(filepath.Join(dir, MainTF), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	pm, err := ReadMain(dir, []tfvars.Variable{
		mustVar(t, "s3_endpoint", "string", cty.NilVal, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pm.Values["s3_endpoint"]; ok {
		t.Errorf("reference should not be materialised into Values")
	}
	var found *RawAttr
	for i := range pm.UnknownAttrs {
		if pm.UnknownAttrs[i].Name == "s3_endpoint" {
			found = &pm.UnknownAttrs[i]
		}
	}
	if found == nil {
		t.Fatalf("s3_endpoint should be captured as a RawAttr; got %+v", pm.UnknownAttrs)
	}
	if got := string(found.RawExpr); got != `data.vault_generic_secret.s3.data["endpoint_url"]` {
		t.Errorf("RawExpr = %q; want the expression only (no `name =` prefix)", got)
	}
}

func TestClearUnknownAttr(t *testing.T) {
	s := &State{
		UnknownAttrs: []RawAttr{
			{Name: "s3_endpoint", RawExpr: []byte("data.x.y")},
			{Name: "count", RawExpr: []byte("2")},
		},
	}
	s.ClearUnknownAttr("s3_endpoint")
	if len(s.UnknownAttrs) != 1 || s.UnknownAttrs[0].Name != "count" {
		t.Errorf("ClearUnknownAttr should remove only s3_endpoint; got %+v", s.UnknownAttrs)
	}
	// Clearing a non-existent name is a no-op.
	s.ClearUnknownAttr("nope")
	if len(s.UnknownAttrs) != 1 {
		t.Errorf("clearing a missing name should be a no-op; got %+v", s.UnknownAttrs)
	}
}
