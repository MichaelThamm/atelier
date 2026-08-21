package wrapper

import (
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
	got := s.InjectModelUUID("abc-def-123", "cos-lite")
	if !got {
		t.Fatal("InjectModelUUID should return true for object model var")
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
	got := s.InjectModelUUID("xyz-789", "")
	if !got {
		t.Fatal("InjectModelUUID should return true for string model_uuid var")
	}
	if s.Values["model_uuid"].AsString() != "xyz-789" {
		t.Errorf("model_uuid = %q, want xyz-789", s.Values["model_uuid"].AsString())
	}
}

func TestInjectModelUUID_nilState(t *testing.T) {
	var s *State
	if got := s.InjectModelUUID("x", ""); got {
		t.Error("nil state should return false")
	}
}

func TestInjectModelUUID_emptyUUID(t *testing.T) {
	s := &State{
		Vars: []tfvars.Variable{
			mustVar(t, "model_uuid", "string", cty.NilVal, false),
		},
	}
	if got := s.InjectModelUUID("", ""); got {
		t.Error("empty UUID should return false")
	}
}

func TestInjectModelUUID_noMatchingVar(t *testing.T) {
	s := &State{
		Vars: []tfvars.Variable{
			mustVar(t, "region", "string", cty.StringVal("us-east-1"), true),
		},
	}
	if got := s.InjectModelUUID("abc", ""); got {
		t.Error("no matching variable should return false")
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
