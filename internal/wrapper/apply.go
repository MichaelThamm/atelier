package wrapper

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// ApplyVarOverrides merges --var KEY=VALUE overrides into state, converting
// each string value to the variable's declared type. It returns warnings for
// values it could not apply — an undeclared name, or a value that does not
// convert to the declared type — so `--var` fails as loudly as `--var-file`
// rather than silently dropping the input.
//
// For an object/map variable, a supplied value is deep-merged into whatever
// value is already present rather than replacing it wholesale. This is what
// lets `--var-file no-ingress --var 'ingress={alertmanager=true}'` mean what it
// reads like: disable ingress except Alertmanager. Replacement would drop the
// keys the --var does not mention, and because those keys then compare as
// unset, the sparse writer would omit the whole attribute and the module would
// fall back to its defaults — the opposite of the user's intent.
func ApplyVarOverrides(state *State, config map[string]string) []string {
	state.EnsureValues()
	var warnings []string
	for _, varName := range slices.Sorted(maps.Keys(config)) {
		strVal := config[varName]
		v := state.FindVar(varName)
		if v == nil {
			warnings = append(warnings, fmt.Sprintf("--var %s: unknown variable ignored", varName))
			continue
		}
		val := ConvertStringToCty(strVal, v)
		if val == cty.NilVal {
			warnings = append(warnings, fmt.Sprintf("--var %s=%s: value does not fit type %s, ignored", varName, strVal, declaredType(v)))
			continue
		}
		if existing, ok := state.Values[varName]; ok && isObjectish(val) && isObjectish(existing) {
			val = mergeObjects(existing, val)
		}
		state.Values[varName] = val
	}
	return warnings
}

// ApplyVarFiles merges values from one or more Terraform variable files into
// state. Later files win over earlier ones; variables a file does not set are
// left untouched. Files may have any name (the common `-var-file` usage).
// Undeclared names and type-mismatched values are skipped and returned as
// warnings; when strict is set they become a hard error instead, so a committed
// example cannot rot silently when a variable is renamed or its type changes
// (ADR-0031). Persisting is the caller's job via state.Write().
func ApplyVarFiles(state *State, paths []string, strict bool) ([]string, error) {
	state.EnsureValues()
	var warnings []string
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("var-file %s: %w", p, err)
		}
		vals, diags, err := ReadTFVarsFileChecked(p, state.Vars)
		if err != nil {
			return nil, err
		}
		if !diags.Empty() {
			for _, n := range diags.Unknown {
				warnings = append(warnings, fmt.Sprintf("%s: unknown variable %q ignored", p, n))
			}
			for _, m := range diags.Mismatched {
				warnings = append(warnings, fmt.Sprintf("%s: %s ignored", p, m))
			}
			if strict {
				return nil, fmt.Errorf("var-file did not apply cleanly:\n  %s", strings.Join(warnings, "\n  "))
			}
		}
		for name, v := range vals {
			state.Values[name] = v
		}
	}
	return warnings, nil
}

// ConvertStringToCty converts a string value to a cty.Value based on the
// variable's declared type. It is how `--var` values become typed wrapper
// values; a value that does not fit the declared type yields cty.NilVal.
func ConvertStringToCty(strVal string, v *tfvars.Variable) cty.Value {
	if v == nil || v.Type == nil {
		// No type info; treat as string.
		return cty.StringVal(strVal)
	}

	typ := v.Type
	switch typ.Kind {
	case tftypes.KindString:
		return cty.StringVal(strVal)
	case tftypes.KindBool:
		switch strings.ToLower(strVal) {
		case "true", "1", "yes":
			return cty.True
		case "false", "0", "no":
			return cty.False
		default:
			return cty.NilVal // invalid bool
		}
	case tftypes.KindNumber:
		// Try to parse as a number: integer first, then float.
		var n int64
		if _, err := fmt.Sscanf(strVal, "%d", &n); err == nil {
			return cty.NumberIntVal(n)
		}
		var f float64
		if _, err := fmt.Sscanf(strVal, "%f", &f); err == nil {
			return cty.NumberFloatVal(f)
		}
		return cty.NilVal // invalid number
	case tftypes.KindObject, tftypes.KindMap, tftypes.KindList, tftypes.KindSet:
		// Parse HCL expressions (objects, maps, lists, sets).
		expr, diags := hclsyntax.ParseExpression([]byte(strVal), "", hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() {
			return cty.NilVal
		}
		val, diags := expr.Value(nil)
		if diags.HasErrors() {
			return cty.NilVal
		}
		return val
	default:
		// For any other type, return nil and let terraform handle it.
		return cty.NilVal
	}
}

// declaredType renders a variable's declared type for a diagnostic, falling
// back to "any" when the module did not declare one.
func declaredType(v *tfvars.Variable) string {
	if v == nil || v.Type == nil {
		return "any"
	}
	return v.Type.String()
}

// isObjectish reports whether a value is a non-null object or map, the shapes
// that support key-wise merging.
func isObjectish(v cty.Value) bool {
	if v == cty.NilVal || v.IsNull() || !v.IsKnown() {
		return false
	}
	t := v.Type()
	return t.IsObjectType() || t.IsMapType()
}

// mergeObjects deep-merges override into base: keys present in both recurse when
// both sides are objects, otherwise override wins. Keys only in base are kept,
// so fields the override does not mention survive.
func mergeObjects(base, override cty.Value) cty.Value {
	if !isObjectish(base) || !isObjectish(override) {
		return override
	}
	out := map[string]cty.Value{}
	for k, v := range base.AsValueMap() {
		out[k] = v
	}
	for k, v := range override.AsValueMap() {
		if existing, ok := out[k]; ok && isObjectish(existing) && isObjectish(v) {
			out[k] = mergeObjects(existing, v)
			continue
		}
		out[k] = v
	}
	return cty.ObjectVal(out)
}
