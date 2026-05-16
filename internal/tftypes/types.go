// Package tftypes models Terraform variable types and values for Atelier.
//
// Atelier needs more information than a bare cty.Type carries: it must know
// which object fields were declared with optional() (and what their declared
// defaults are) so the sparse-plus-required write rule (ADR-0007) can decide
// what to emit.
package tftypes

import "github.com/zclconf/go-cty/cty"

// Kind enumerates the variable-type shapes Atelier recognises. Atelier maps
// each kind to a widget; see SPEC §8.
type Kind int

const (
	KindString Kind = iota
	KindBool
	KindNumber
	KindList
	KindSet
	KindMap
	KindObject
	KindTuple
	KindAny
)

func (k Kind) String() string {
	switch k {
	case KindString:
		return "string"
	case KindBool:
		return "bool"
	case KindNumber:
		return "number"
	case KindList:
		return "list"
	case KindSet:
		return "set"
	case KindMap:
		return "map"
	case KindObject:
		return "object"
	case KindTuple:
		return "tuple"
	case KindAny:
		return "any"
	}
	return "unknown"
}

// Type is Atelier's representation of a Terraform variable type. It is a
// recursive structure mirroring the type expression the module author wrote.
type Type struct {
	Kind Kind

	// Element is set for list/set/map. It is the element type.
	Element *Type

	// Attributes is set for object. The map preserves declaration order via
	// the AttrOrder slice (cty would otherwise hash-randomise iteration).
	Attributes map[string]*ObjectAttr
	AttrOrder  []string

	// Tuple is set for tuple; one entry per positional element.
	Tuple []*Type
}

// ObjectAttr describes one field of an object type. It tracks whether the
// field was declared as optional() and, if so, whether the declaration
// supplied a default value (the second argument to optional()).
type ObjectAttr struct {
	Type     *Type
	Optional bool
	// HasDefault is true when the declaration was optional(T, x). Default
	// then holds the parsed default value (as a cty.Value to preserve type
	// fidelity).
	HasDefault bool
	Default    cty.Value
}

// IsPrimitive reports whether the type is one of string/bool/number.
func (t *Type) IsPrimitive() bool {
	return t.Kind == KindString || t.Kind == KindBool || t.Kind == KindNumber
}

// IsCollection reports whether the type is list/set/map.
func (t *Type) IsCollection() bool {
	return t.Kind == KindList || t.Kind == KindSet || t.Kind == KindMap
}

// String renders the type in HCL syntax, e.g. "object({a=string,b=optional(number,42)})".
// Used in error messages and the read-only `any`/`tuple` widgets.
func (t *Type) String() string {
	if t == nil {
		return "any"
	}
	switch t.Kind {
	case KindString, KindBool, KindNumber, KindAny:
		return t.Kind.String()
	case KindList, KindSet, KindMap:
		return t.Kind.String() + "(" + t.Element.String() + ")"
	case KindObject:
		out := "object({"
		for i, name := range t.AttrOrder {
			if i > 0 {
				out += ","
			}
			attr := t.Attributes[name]
			out += name + "="
			if attr.Optional {
				out += "optional(" + attr.Type.String() + ")"
			} else {
				out += attr.Type.String()
			}
		}
		out += "})"
		return out
	case KindTuple:
		out := "tuple(["
		for i, el := range t.Tuple {
			if i > 0 {
				out += ","
			}
			out += el.String()
		}
		out += "])"
		return out
	}
	return "unknown"
}
