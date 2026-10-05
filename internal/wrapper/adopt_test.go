package wrapper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// stringVar is a plain string declaration, the common shape in these tests.
func stringVar(name string) tfvars.Variable {
	return tfvars.Variable{Name: name, Type: &tftypes.Type{Kind: tftypes.KindString}}
}

func rawExpr(name, expr string) RawAttr {
	return RawAttr{Name: name, Raw: []byte(name + " = " + expr), RawExpr: []byte(expr)}
}

// AdoptPrior is the one carry-over rule for a schema change, so the cases that
// matter are what it drops, what it keeps, and that it cannot be half-applied.
func TestAdoptPrior(t *testing.T) {
	cases := []struct {
		name string
		vars []tfvars.Variable
		// seeded is what the state already carried before the prior is adopted.
		// AdoptPrior filters the prior, not the state, so anything seeded here
		// survives regardless of the schema.
		seeded     map[string]cty.Value
		priorVals  map[string]cty.Value
		priorAttrs []RawAttr
		wantVals   []string
		wantAttrs  []string
	}{
		{
			name:      "surviving value is carried",
			vars:      []tfvars.Variable{stringVar("model_uuid")},
			priorVals: map[string]cty.Value{"model_uuid": cty.StringVal("uuid-1")},
			wantVals:  []string{"model_uuid"},
		},
		{
			// The reason the rule filters at all: a ref switch that dropped a
			// variable must not write the stale argument back, which
			// `terraform init` would reject as unrecognised.
			name:      "value for a variable the new schema dropped is discarded",
			vars:      []tfvars.Variable{stringVar("kept")},
			priorVals: map[string]cty.Value{"kept": cty.StringVal("k"), "gone": cty.StringVal("g")},
			wantVals:  []string{"kept"},
		},
		{
			name:       "wired expression survives verbatim",
			vars:       []tfvars.Variable{stringVar("model_uuid")},
			priorAttrs: []RawAttr{rawExpr("model_uuid", "data.juju_model.x.uuid")},
			wantAttrs:  []string{"model_uuid"},
		},
		{
			name:       "wired expression for a dropped variable is discarded",
			vars:       []tfvars.Variable{stringVar("kept")},
			priorAttrs: []RawAttr{rawExpr("gone", "data.x.y"), rawExpr("kept", "data.a.b")},
			wantAttrs:  []string{"kept"},
		},
		{
			// A meta-argument is not a module input, so the state does not model
			// it. It survives a rewrite anyway — see the round-trip case below
			// and AdoptPrior's comment — so carrying it here would only mean
			// holding an opinion about how modules compose.
			name: "a meta-argument is not carried into the state",
			vars: []tfvars.Variable{stringVar("model_uuid")},
			priorAttrs: []RawAttr{
				rawExpr("depends_on", "[module.other]"),
				rawExpr("count", "2"),
			},
			wantAttrs: nil,
		},
		{
			name:       "a prior value replaces what the state already carried",
			vars:       []tfvars.Variable{stringVar("a")},
			seeded:     map[string]cty.Value{"a": cty.StringVal("stale")},
			priorVals:  map[string]cty.Value{"a": cty.StringVal("prior")},
			wantVals:   []string{"a"},
			wantAttrs:  nil,
			priorAttrs: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state := &State{
				Vars:   c.vars,
				Values: map[string]cty.Value{},
			}
			for k, v := range c.seeded {
				state.Values[k] = v
			}
			state.AdoptPrior(c.priorVals, c.priorAttrs)

			var gotVals []string
			for name := range state.Values {
				gotVals = append(gotVals, name)
			}
			if len(state.Values) != len(c.wantVals) {
				t.Fatalf("Values = %v, want %v", gotVals, c.wantVals)
			}
			for _, want := range c.wantVals {
				if _, ok := state.Values[want]; !ok {
					t.Errorf("value %q not carried; got %v", want, gotVals)
				}
			}
			if len(c.seeded) > 0 && c.wantVals[0] != "" {
				if got := state.Values["a"]; got.AsString() != "prior" {
					t.Errorf("a = %v, want the prior value to win", got)
				}
			}

			var gotAttrs []string
			for _, ra := range state.UnknownAttrs {
				gotAttrs = append(gotAttrs, ra.Name)
			}
			if len(gotAttrs) != len(c.wantAttrs) {
				t.Fatalf("UnknownAttrs = %v, want %v", gotAttrs, c.wantAttrs)
			}
			for i, want := range c.wantAttrs {
				if gotAttrs[i] != want {
					t.Errorf("UnknownAttrs[%d] = %q, want %q", i, gotAttrs[i], want)
				}
			}
		})
	}
}

// Applying the same prior twice must be a no-op. bootstrap.LoadRefState carries
// the prior over and Model.applyRefSwitch then carries it again from the
// in-memory state, so a caller that is not idempotent would duplicate attributes.
func TestAdoptPrior_isIdempotent(t *testing.T) {
	prior := []RawAttr{rawExpr("model_uuid", "data.x.y"), rawExpr("depends_on", "[module.a]")}
	state := &State{Vars: []tfvars.Variable{stringVar("model_uuid")}}

	state.AdoptPrior(nil, prior)
	first := len(state.UnknownAttrs)
	state.AdoptPrior(nil, prior)

	if len(state.UnknownAttrs) != first {
		t.Errorf("second AdoptPrior changed the carried attributes: %d then %d", first, len(state.UnknownAttrs))
	}
	if len(state.Values) != 0 {
		t.Errorf("carrying an expression must not materialise a value: %v", state.Values)
	}
}

// A meta-argument the state does not carry must still reach the written block:
// writes are AST-backed on the existing main.tf, so an attribute already there
// is never rewritten or pruned. This is what lets AdoptPrior ignore meta-args
// without costing the user anything.
func TestAdoptPrior_dependsOnReachesTheBlock(t *testing.T) {
	dir := t.TempDir()
	initial := `module "mimir" {
  source     = "git::https://example.com/mimir?ref=v2"
  model_uuid = "uuid-1"
  depends_on = [module.other]
}
`
	if err := os.WriteFile(filepath.Join(dir, MainTF), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	prior, err := ReadMainForBlock(dir, "mimir", []tfvars.Variable{stringVar("model_uuid")})
	if err != nil {
		t.Fatal(err)
	}

	state := &State{
		Dir:             dir,
		ModuleBlockName: "mimir",
		Source:          "git::https://example.com/mimir?ref=v2",
		Vars:            []tfvars.Variable{stringVar("model_uuid")},
	}
	state.AdoptPrior(prior.Values, prior.UnknownAttrs)
	if err := state.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, MainTF))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"depends_on = [module.other]", `model_uuid = "uuid-1"`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("expected %q in the rewritten block:\n%s", want, got)
		}
	}
}
