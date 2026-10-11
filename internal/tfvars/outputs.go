package tfvars

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
)

// Output describes a module-declared output. Terraform infers an output's type
// from its value expression when no type constraint is declared; Atelier does
// not evaluate expressions, so Type is KindAny in that case. See ADR-0060.
type Output struct {
	Name string
	// Type is the declared type constraint, or KindAny when the block declares
	// none (the common case) or the constraint cannot be parsed.
	Type        *tftypes.Type
	Description string
	Sensitive   bool
	// Deprecated is the deprecation message when the output declares one, else "".
	Deprecated string
	Ephemeral  bool
	// ValueRaw is the literal source of the `value` expression. Atelier keeps
	// it verbatim rather than evaluating it (ADR-0012).
	ValueRaw string

	// DeclRange points back to the output's declaration site in source.
	DeclRange hcl.Range
}

func outputsInBody(body *hclsyntax.Body) ([]Output, error) {
	var outs []Output
	for _, block := range body.Blocks {
		if block.Type != "output" {
			continue
		}
		if len(block.Labels) != 1 {
			return nil, fmt.Errorf("output block at %s must have exactly one label (the output name)", block.DefRange().String())
		}
		o, err := parseOutputBlock(block)
		if err != nil {
			return nil, err
		}
		outs = append(outs, o)
	}
	return outs, nil
}

func parseOutputBlock(block *hclsyntax.Block) (Output, error) {
	o := Output{
		Name:      block.Labels[0],
		Type:      &tftypes.Type{Kind: tftypes.KindAny},
		DeclRange: block.DefRange(),
	}

	attrs := block.Body.Attributes

	if a, ok := attrs["type"]; ok {
		// An unparseable constraint falls back to KindAny rather than failing
		// the whole load (ADR-0060): the type is a hint for wiring, not a gate.
		if typ, err := tftypes.ParseTypeExpr(exprSource(a.Expr)); err == nil {
			o.Type = typ
		}
	}

	if a, ok := attrs["description"]; ok {
		if val, diags := a.Expr.Value(nil); !diags.HasErrors() && val.Type() == cty.String && !val.IsNull() {
			o.Description = val.AsString()
		}
	}
	if a, ok := attrs["sensitive"]; ok {
		if val, diags := a.Expr.Value(nil); !diags.HasErrors() && val.Type() == cty.Bool && !val.IsNull() {
			o.Sensitive = val.True()
		}
	}
	if a, ok := attrs["deprecated"]; ok {
		if val, diags := a.Expr.Value(nil); !diags.HasErrors() && val.Type() == cty.String && !val.IsNull() {
			o.Deprecated = val.AsString()
		}
	}
	if a, ok := attrs["ephemeral"]; ok {
		if val, diags := a.Expr.Value(nil); !diags.HasErrors() && val.Type() == cty.Bool && !val.IsNull() {
			o.Ephemeral = val.True()
		}
	}
	if a, ok := attrs["value"]; ok {
		o.ValueRaw = exprSource(a.Expr)
	}

	return o, nil
}
