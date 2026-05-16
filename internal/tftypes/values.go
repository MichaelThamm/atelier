package tftypes

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// ParseValueExpr parses an HCL expression and evaluates it to a cty.Value
// using an empty evaluation context. This is used for variable default
// expressions, which by Terraform's grammar must be literals (no references
// to other variables or functions). If the expression cannot be evaluated
// with no context, the returned error tells the caller to treat the default
// as "complex" — the variable still works, but Atelier shows the literal HCL
// in the editor.
func ParseValueExpr(src string) (cty.Value, error) {
	expr, diags := hclsyntax.ParseExpression([]byte(src), "", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return cty.NilVal, fmt.Errorf("parse value: %s", diags.Error())
	}
	val, diags := expr.Value(nil)
	if diags.HasErrors() {
		return cty.NilVal, fmt.Errorf("evaluate value: %s", diags.Error())
	}
	return val, nil
}

// ZeroValue returns the zero cty.Value for a type. For primitives it's the
// usual empty/false/0; for collections it's an empty collection of the right
// shape; for objects every field is its zero, with optional+default fields
// preferring the declared default.
func ZeroValue(t *Type) cty.Value {
	if t == nil {
		return cty.NullVal(cty.DynamicPseudoType)
	}
	switch t.Kind {
	case KindString:
		return cty.StringVal("")
	case KindBool:
		return cty.False
	case KindNumber:
		return cty.NumberIntVal(0)
	case KindList:
		return cty.ListValEmpty(CtyType(t.Element))
	case KindSet:
		return cty.SetValEmpty(CtyType(t.Element))
	case KindMap:
		return cty.MapValEmpty(CtyType(t.Element))
	case KindObject:
		fields := map[string]cty.Value{}
		for _, name := range t.AttrOrder {
			attr := t.Attributes[name]
			if attr.HasDefault {
				fields[name] = attr.Default
				continue
			}
			fields[name] = ZeroValue(attr.Type)
		}
		if len(fields) == 0 {
			return cty.EmptyObjectVal
		}
		return cty.ObjectVal(fields)
	case KindTuple:
		if len(t.Tuple) == 0 {
			return cty.EmptyTupleVal
		}
		vals := make([]cty.Value, len(t.Tuple))
		for i, el := range t.Tuple {
			vals[i] = ZeroValue(el)
		}
		return cty.TupleVal(vals)
	case KindAny:
		return cty.NullVal(cty.DynamicPseudoType)
	}
	return cty.NullVal(cty.DynamicPseudoType)
}

// CtyType returns the cty.Type corresponding to an Atelier Type. Optional()
// metadata is lost (cty's tagging is incomplete for our purposes); callers
// that need it should consult the Atelier Type directly.
func CtyType(t *Type) cty.Type {
	if t == nil {
		return cty.DynamicPseudoType
	}
	switch t.Kind {
	case KindString:
		return cty.String
	case KindBool:
		return cty.Bool
	case KindNumber:
		return cty.Number
	case KindList:
		return cty.List(CtyType(t.Element))
	case KindSet:
		return cty.Set(CtyType(t.Element))
	case KindMap:
		return cty.Map(CtyType(t.Element))
	case KindObject:
		fields := map[string]cty.Type{}
		for name, attr := range t.Attributes {
			fields[name] = CtyType(attr.Type)
		}
		return cty.Object(fields)
	case KindTuple:
		elems := make([]cty.Type, len(t.Tuple))
		for i, el := range t.Tuple {
			elems[i] = CtyType(el)
		}
		return cty.Tuple(elems)
	}
	return cty.DynamicPseudoType
}
