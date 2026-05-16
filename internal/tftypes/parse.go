package tftypes

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// ParseTypeExpr parses a Terraform type expression (e.g. "string",
// "object({a=string,b=optional(number,42)})") and returns an Atelier Type.
//
// The Terraform grammar for type expressions is a small subset of HCL: a few
// keywords (string/bool/number/any), call-like constructs (list, set, map,
// object, tuple, optional), and an object body. We walk the HCL AST directly
// rather than using cty.Type as the source of truth because cty.Type does
// not retain optional() default values; we need those for sparse writes.
func ParseTypeExpr(src string) (*Type, error) {
	expr, diags := hclsyntax.ParseExpression([]byte(src), "", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse type expression %q: %s", src, diags.Error())
	}
	return parseExpr(expr)
}

func parseExpr(expr hcl.Expression) (*Type, error) {
	switch e := expr.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		return parseKeyword(e)
	case *hclsyntax.FunctionCallExpr:
		return parseCall(e)
	case *hclsyntax.ObjectConsExpr:
		// e.g. `object({...})` — but the outer call should have unwrapped to
		// here only if someone wrote a bare object literal as a type. Not
		// valid Terraform but handle defensively.
		return nil, fmt.Errorf("bare object literal is not a valid type")
	}
	return nil, fmt.Errorf("unsupported type expression: %T", expr)
}

func parseKeyword(e *hclsyntax.ScopeTraversalExpr) (*Type, error) {
	if len(e.Traversal) != 1 {
		return nil, fmt.Errorf("unexpected multi-step traversal in type")
	}
	root, ok := e.Traversal[0].(hcl.TraverseRoot)
	if !ok {
		return nil, fmt.Errorf("unexpected traversal step in type")
	}
	switch root.Name {
	case "string":
		return &Type{Kind: KindString}, nil
	case "bool":
		return &Type{Kind: KindBool}, nil
	case "number":
		return &Type{Kind: KindNumber}, nil
	case "any":
		return &Type{Kind: KindAny}, nil
	// Bare type keywords without parentheses — Terraform allows `type = list`
	// but it's unusual; map them to their `(any)` form.
	case "list":
		return &Type{Kind: KindList, Element: &Type{Kind: KindAny}}, nil
	case "set":
		return &Type{Kind: KindSet, Element: &Type{Kind: KindAny}}, nil
	case "map":
		return &Type{Kind: KindMap, Element: &Type{Kind: KindAny}}, nil
	case "object":
		return nil, fmt.Errorf("bare `object` without ({...}) is not a valid type")
	case "tuple":
		return nil, fmt.Errorf("bare `tuple` without ([...]) is not a valid type")
	}
	return nil, fmt.Errorf("unknown type keyword %q", root.Name)
}

func parseCall(e *hclsyntax.FunctionCallExpr) (*Type, error) {
	switch e.Name {
	case "list", "set", "map":
		if len(e.Args) != 1 {
			return nil, fmt.Errorf("%s(...) takes exactly one argument", e.Name)
		}
		inner, err := parseExpr(e.Args[0])
		if err != nil {
			return nil, fmt.Errorf("%s element: %w", e.Name, err)
		}
		k := KindList
		switch e.Name {
		case "set":
			k = KindSet
		case "map":
			k = KindMap
		}
		return &Type{Kind: k, Element: inner}, nil
	case "object":
		if len(e.Args) != 1 {
			return nil, fmt.Errorf("object(...) takes exactly one argument")
		}
		obj, ok := e.Args[0].(*hclsyntax.ObjectConsExpr)
		if !ok {
			return nil, fmt.Errorf("object(...) argument must be an object literal")
		}
		return parseObjectBody(obj)
	case "tuple":
		if len(e.Args) != 1 {
			return nil, fmt.Errorf("tuple(...) takes exactly one argument")
		}
		t := &Type{Kind: KindTuple}
		tup, ok := e.Args[0].(*hclsyntax.TupleConsExpr)
		if !ok {
			return nil, fmt.Errorf("tuple(...) argument must be a list literal")
		}
		for _, el := range tup.Exprs {
			inner, err := parseExpr(el)
			if err != nil {
				return nil, fmt.Errorf("tuple element: %w", err)
			}
			t.Tuple = append(t.Tuple, inner)
		}
		return t, nil
	case "optional":
		// optional() is only valid as the value of an object attribute. The
		// caller (parseObjectBody) handles it directly; reaching it here
		// means a stray optional() was used outside an object body.
		return nil, fmt.Errorf("optional(...) is only valid inside an object body")
	}
	return nil, fmt.Errorf("unknown type constructor %q", e.Name)
}

func parseObjectBody(obj *hclsyntax.ObjectConsExpr) (*Type, error) {
	t := &Type{
		Kind:       KindObject,
		Attributes: map[string]*ObjectAttr{},
	}
	for _, item := range obj.Items {
		// Key is a quoted-style key wrapped in TemplateExpr or an
		// ObjectConsKeyExpr; ask HCL for the literal name.
		name, diags := hcl.ExprAsKeyword(item.KeyExpr), hcl.Diagnostics(nil)
		if name == "" {
			// Fall back to evaluating as a literal string.
			val, d := item.KeyExpr.Value(nil)
			diags = append(diags, d...)
			if diags.HasErrors() || val.Type() != cty.String {
				return nil, fmt.Errorf("object attribute key must be an identifier")
			}
			name = val.AsString()
		}
		attr := &ObjectAttr{}
		if call, ok := item.ValueExpr.(*hclsyntax.FunctionCallExpr); ok && call.Name == "optional" {
			if len(call.Args) < 1 || len(call.Args) > 2 {
				return nil, fmt.Errorf("optional(...) takes 1 or 2 arguments")
			}
			inner, err := parseExpr(call.Args[0])
			if err != nil {
				return nil, fmt.Errorf("optional() inner type: %w", err)
			}
			attr.Type = inner
			attr.Optional = true
			if len(call.Args) == 2 {
				val, dd := call.Args[1].Value(nil)
				if dd.HasErrors() {
					return nil, fmt.Errorf("optional() default for %q: %s", name, dd.Error())
				}
				attr.HasDefault = true
				attr.Default = val
			}
		} else {
			inner, err := parseExpr(item.ValueExpr)
			if err != nil {
				return nil, fmt.Errorf("attribute %q: %w", name, err)
			}
			attr.Type = inner
		}
		t.Attributes[name] = attr
		t.AttrOrder = append(t.AttrOrder, name)
	}
	return t, nil
}
