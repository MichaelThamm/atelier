package wrapper

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
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

func TestInjectModelUUID_modelObject(t *testing.T) {
	s := &State{
		Vars: []tfvars.Variable{
			mustVar(t, "model", `object({name = string, uuid = string})`, cty.EmptyObjectVal, true),
			mustVar(t, "internal_tls", "bool", cty.True, true),
		},
	}
	if got := s.InjectModelUUID("abc-def-123", "cos-lite"); got != ModelUUIDInjected {
		t.Fatalf("got %v, want ModelUUIDInjected for object model var", got)
	}
	uv := s.Values["model"].AsValueMap()
	if uv["uuid"].AsString() != "abc-def-123" {
		t.Errorf("model.uuid = %q, want abc-def-123", uv["uuid"].AsString())
	}
	if uv["name"].AsString() != "cos-lite" {
		t.Errorf("model.name = %q, want cos-lite", uv["name"].AsString())
	}
}

func TestInjectModelUUID_modelUuidString(t *testing.T) {
	s := &State{
		Vars: []tfvars.Variable{
			mustVar(t, "model_uuid", "string", cty.NilVal, false),
		},
	}
	if got := s.InjectModelUUID("xyz-789", ""); got != ModelUUIDInjected {
		t.Fatalf("got %v, want ModelUUIDInjected for string model_uuid var", got)
	}
	if s.Values["model_uuid"].AsString() != "xyz-789" {
		t.Errorf("model_uuid = %q, want xyz-789", s.Values["model_uuid"].AsString())
	}
}

func TestInjectModelUUID_nilState(t *testing.T) {
	var s *State
	if got := s.InjectModelUUID("x", ""); got != ModelUUIDNoVariable {
		t.Errorf("nil state: got %v, want ModelUUIDNoVariable", got)
	}
}

func TestInjectModelUUID_emptyUUID(t *testing.T) {
	s := &State{
		Vars: []tfvars.Variable{
			mustVar(t, "model_uuid", "string", cty.NilVal, false),
		},
	}
	if got := s.InjectModelUUID("", ""); got != ModelUUIDNoVariable {
		t.Errorf("empty UUID: got %v, want ModelUUIDNoVariable", got)
	}
}

func TestInjectModelUUID_noMatchingVar(t *testing.T) {
	s := &State{
		Vars: []tfvars.Variable{
			mustVar(t, "region", "string", cty.StringVal("us-east-1"), true),
		},
	}
	// The distinguishing case: nothing recognised to write into. This must be
	// reported to the user, so it must not be confused with "already set".
	if got := s.InjectModelUUID("abc", ""); got != ModelUUIDNoVariable {
		t.Errorf("no matching variable: got %v, want ModelUUIDNoVariable", got)
	}
}

func TestInjectModelUUID_emptyNameFallsBackToZero(t *testing.T) {
	s := &State{
		Vars: []tfvars.Variable{
			mustVar(t, "model", `object({name = string, uuid = string})`, cty.EmptyObjectVal, true),
		},
	}
	s.InjectModelUUID("abc", "")
	uv := s.Values["model"].AsValueMap()
	if uv["name"].AsString() != "" {
		t.Errorf("model.name = %q, want empty when name arg is empty", uv["name"].AsString())
	}
}

// cosLiteModelType is the `model` variable type from
// canonical/observability-stack//terraform/cos-lite. It is reproduced here
// because it exercises every shape that matters: an optional string with no
// default (uuid), an optional string with one (name), a nested optional object
// (cloud) and optional maps — all of which the module's own validation
// requires to stay null whenever uuid is set.
const cosLiteModelType = `object({
  uuid  = optional(string)
  name  = optional(string, "cos-lite")
  cloud = optional(object({
    name   = string
    region = optional(string)
  }))
  annotations       = optional(map(string))
  config            = optional(map(string))
  constraints       = optional(string)
  credential        = optional(string)
  target_controller = optional(string)
})`

func newCosLiteState(t *testing.T, dir string) *State {
	t.Helper()
	return &State{
		Dir:             dir,
		ModuleBlockName: "cos_lite",
		Source:          "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite",
		Vars:            []tfvars.Variable{mustVar(t, "model", cosLiteModelType, cty.EmptyObjectVal, true)},
		Values:          map[string]cty.Value{},
	}
}

// Injecting into COS-Lite's object-typed `model` variable must render only the
// uuid. Emitting the other fields at their zero values (e.g. cloud = {name=""},
// annotations = {}) would be non-null and would trip the module's own
// validation, which requires them to be null whenever uuid is set.
func TestInjectModelUUID_rendersSparseModelObject(t *testing.T) {
	dir := t.TempDir()
	s := newCosLiteState(t, dir)
	if got := s.InjectModelUUID("b62cdacf-9e9b-4e35-8c5e-e334930e2b02", "cos-lite"); got != ModelUUIDInjected {
		t.Fatalf("got %v, want ModelUUIDInjected", got)
	}
	out, err := s.RenderMain()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	want := `module "cos_lite" {
  source = "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite"
  model = {
    uuid = "b62cdacf-9e9b-4e35-8c5e-e334930e2b02"
  }
}
`
	if got != want {
		t.Errorf("rendered main.tf mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// The step may run against a wrapper the user has already edited, so fields
// they set must survive the injection.
func TestInjectModelUUID_preservesExistingFields(t *testing.T) {
	dir := t.TempDir()
	s := newCosLiteState(t, dir)
	s.Values["model"] = cty.ObjectVal(map[string]cty.Value{
		"constraints": cty.StringVal("arch=arm64"),
	})
	if got := s.InjectModelUUID("abc-123", "cos-lite"); got != ModelUUIDInjected {
		t.Fatalf("got %v, want ModelUUIDInjected", got)
	}
	out, err := s.RenderMain()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `"abc-123"`) {
		t.Errorf("uuid not injected:\n%s", got)
	}
	if !strings.Contains(got, `"arch=arm64"`) {
		t.Errorf("pre-existing field was clobbered:\n%s", got)
	}
}

// A UUID the user chose explicitly must never be silently replaced by a
// derived one.
func TestInjectModelUUID_doesNotOverwriteExistingUUID(t *testing.T) {
	s := newCosLiteState(t, t.TempDir())
	s.Values["model"] = cty.ObjectVal(map[string]cty.Value{
		"uuid": cty.StringVal("user-chosen"),
	})
	if got := s.InjectModelUUID("derived", "cos-lite"); got != ModelUUIDAlreadySet {
		t.Fatalf("got %v, want ModelUUIDAlreadySet (must not overwrite)", got)
	}
	if got := s.Values["model"].AsValueMap()["uuid"].AsString(); got != "user-chosen" {
		t.Errorf("uuid = %q, want user-chosen", got)
	}
}

func TestInjectModelUUID_doesNotOverwriteExistingModelUUIDString(t *testing.T) {
	s := &State{
		Vars:   []tfvars.Variable{mustVar(t, "model_uuid", "string", cty.StringVal(""), true)},
		Values: map[string]cty.Value{"model_uuid": cty.StringVal("user-chosen")},
	}
	if got := s.InjectModelUUID("derived", ""); got != ModelUUIDAlreadySet {
		t.Fatalf("got %v, want ModelUUIDAlreadySet (must not overwrite)", got)
	}
	if got := s.Values["model_uuid"].AsString(); got != "user-chosen" {
		t.Errorf("model_uuid = %q, want user-chosen", got)
	}
}

// An empty-string uuid means "unset", so injection should still fill it.
func TestInjectModelUUID_fillsEmptyUUID(t *testing.T) {
	s := newCosLiteState(t, t.TempDir())
	s.Values["model"] = cty.ObjectVal(map[string]cty.Value{"uuid": cty.StringVal("")})
	if got := s.InjectModelUUID("derived", "cos-lite"); got != ModelUUIDInjected {
		t.Fatalf("got %v, want ModelUUIDInjected (empty uuid should be filled)", got)
	}
	if got := s.Values["model"].AsValueMap()["uuid"].AsString(); got != "derived" {
		t.Errorf("uuid = %q, want derived", got)
	}
}

// When no better value is available, a field must fall back to its own declared
// default so the sparse writer prunes it. Falling back to the type's zero value
// instead emitted noise the user never asked for — `name = ""` for a field
// declared optional(string, "cos-lite") — which is also the shape COS-Lite's own
// validation rejects when uuid is absent.
func TestInjectModelUUID_UnknownNameFallsBackToDeclaredDefault(t *testing.T) {
	dir := t.TempDir()
	s := newCosLiteState(t, dir)
	// name deliberately empty: the live model name could not be determined.
	if got := s.InjectModelUUID("b62cdacf-9e9b-4e35-8c5e-e334930e2b02", ""); got != ModelUUIDInjected {
		t.Fatalf("got %v, want ModelUUIDInjected", got)
	}
	out, err := s.RenderMain()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `name`) {
		t.Errorf("name should be pruned as at-default, got:\n%s", out)
	}
	want := `module "cos_lite" {
  source = "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite"
  model = {
    uuid = "b62cdacf-9e9b-4e35-8c5e-e334930e2b02"
  }
}
`
	if string(out) != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}
