package wrapper

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

func TestVariableValue_secretValuesFallback(t *testing.T) {
	s := &State{
		Vars: []tfvars.Variable{
			{Name: "password", Type: &tftypes.Type{Kind: tftypes.KindString}, Sensitive: true},
			{Name: "endpoint", Type: &tftypes.Type{Kind: tftypes.KindString}, HasDefault: true, Default: cty.StringVal("http://default")},
		},
		Values:       map[string]cty.Value{},
		SecretValues: map[string]cty.Value{"password": cty.StringVal("hunter2")},
	}

	// Sensitive value should be found via SecretValues.
	val, ok := s.VariableValue("password")
	if !ok {
		t.Fatal("VariableValue should find sensitive value in SecretValues")
	}
	if val.AsString() != "hunter2" {
		t.Errorf("password = %q; want %q", val.AsString(), "hunter2")
	}

	// Non-sensitive value with default should fall back to default.
	val, ok = s.VariableValue("endpoint")
	if !ok {
		t.Fatal("VariableValue should find default for endpoint")
	}
	if val.AsString() != "http://default" {
		t.Errorf("endpoint = %q; want %q", val.AsString(), "http://default")
	}

	// Unknown variable should not be found.
	_, ok = s.VariableValue("nonexistent")
	if ok {
		t.Error("VariableValue should return false for unknown variable")
	}
}

func TestVariableValue_valuesTakesPrecedenceOverSecrets(t *testing.T) {
	s := &State{
		Vars: []tfvars.Variable{
			{Name: "x", Type: &tftypes.Type{Kind: tftypes.KindString}},
		},
		Values:       map[string]cty.Value{"x": cty.StringVal("from-values")},
		SecretValues: map[string]cty.Value{"x": cty.StringVal("from-secrets")},
	}

	val, ok := s.VariableValue("x")
	if !ok {
		t.Fatal("VariableValue should find value")
	}
	if val.AsString() != "from-values" {
		t.Errorf("x = %q; want %q (Values should take precedence)", val.AsString(), "from-values")
	}
}
