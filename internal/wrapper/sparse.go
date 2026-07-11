package wrapper

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// ShouldEmit reports whether the variable `v` should appear in the wrapper's
// module {} block given the current value, per ADR-0007.
//
//   - Required (no default declared): always emit. Even unset (a NilVal
//     current) is emitted as a placeholder so Terraform's "missing required
//     variable" error fires loudly at plan time.
//   - Optional non-object (has default): emit only when the current value
//     differs from the declared default.
//   - Optional object: emit when any field differs from its effective default
//     (declared optional() default, falling back to the variable-level
//     default, falling back to the type's zero). Equivalent to "the sparse
//     value is non-empty".
func ShouldEmit(v *tfvars.Variable, current cty.Value) bool {
	if v == nil {
		return false
	}
	if !v.HasDefault {
		return true
	}
	if current == cty.NilVal {
		return false
	}
	if v.Type != nil && v.Type.Kind == tftypes.KindObject && !current.IsNull() {
		sparse := SparseValue(v, current)
		if sparse == cty.NilVal || sparse.IsNull() {
			return false
		}
		if sparse.Type().IsObjectType() && sparse.LengthInt() == 0 {
			return false
		}
		return true
	}
	return !tftypes.EqualValue(current, v.Default)
}

// SparseValue returns the value Atelier should write for a variable, given
// the current effective value. For most types this is just `current`. For
// object types, the result is a partial object containing only fields whose
// values differ from their declared optional() default — matching the
// recursive sparse rule (SPEC §10.1 ¶3).
//
// If the resulting partial object is empty (i.e. every field is at-default),
// SparseValue returns cty.NilVal, signalling that the variable as a whole
// should not be emitted. The corresponding ShouldEmit return will be false
// for such cases, but callers should check NilVal too.
func SparseValue(v *tfvars.Variable, current cty.Value) cty.Value {
	if v == nil || v.Type == nil {
		return current
	}
	return sparseFor(v.Type, current, v.Default)
}

func sparseFor(t *tftypes.Type, current, defVal cty.Value) cty.Value {
	if t == nil || current == cty.NilVal {
		return current
	}
	if t.Kind != tftypes.KindObject {
		// For non-objects, the variable is either at-default (skipped by
		// ShouldEmit, never reaches here for the "emit" path) or has a
		// concrete value to write whole.
		return current
	}
	if current.IsNull() {
		return current
	}

	// Object: build a partial-object value containing only fields whose
	// values differ from their declared defaults (the optional(T, default)
	// second argument).
	curMap := current.AsValueMap()
	defMap := map[string]cty.Value{}
	if !defVal.IsNull() && defVal.Type().IsObjectType() {
		defMap = defVal.AsValueMap()
	}

	out := map[string]cty.Value{}
	for _, name := range t.AttrOrder {
		attr := t.Attributes[name]
		cur, hasCur := curMap[name]
		if !hasCur {
			continue
		}
		// Determine the field-level effective default: declared in
		// optional(T, x), falling back to the outer variable's `default = {}`
		// entry, falling back to the zero value of the field type.
		var fieldDefault cty.Value
		switch {
		case attr.HasDefault:
			fieldDefault = attr.Default
		default:
			if d, ok := defMap[name]; ok {
				fieldDefault = d
			} else {
				fieldDefault = tftypes.ZeroValue(attr.Type)
			}
		}

		if attr.Type != nil && attr.Type.Kind == tftypes.KindObject {
			// Recurse: only emit the sub-object if it has at least one
			// differing field.
			sub := sparseFor(attr.Type, cur, fieldDefault)
			if sub == cty.NilVal || (sub.Type().IsObjectType() && sub.LengthInt() == 0) {
				continue
			}
			out[name] = sub
			continue
		}

		if tftypes.EqualValue(cur, fieldDefault) {
			continue
		}
		out[name] = cur
	}
	if len(out) == 0 {
		return cty.EmptyObjectVal
	}
	return cty.ObjectVal(out)
}
