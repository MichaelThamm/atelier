package tui

import (
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/manifest"
	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// ResolvedPreset is a preset ready for application: its Sets are already
// converted to cty.Values keyed by variable name.
type ResolvedPreset struct {
	Name        string
	Description string
	Values      map[string]cty.Value
}

// ResolvePresets converts raw manifest presets into resolved presets using
// the module's declared variable types. Variables referenced in the preset
// that don't exist in the module are silently dropped.
func ResolvePresets(presets []manifest.Preset, vars []tfvars.Variable) []ResolvedPreset {
	varMap := make(map[string]*tfvars.Variable, len(vars))
	for i := range vars {
		varMap[vars[i].Name] = &vars[i]
	}

	var out []ResolvedPreset
	for _, p := range presets {
		rp := ResolvedPreset{
			Name:        p.Name,
			Description: p.Description,
			Values:      make(map[string]cty.Value),
		}
		for name, raw := range p.Sets {
			v, ok := varMap[name]
			if !ok {
				continue // variable not declared in module; skip
			}
			val, err := anyToCty(raw, v.Type)
			if err != nil {
				continue // type mismatch; skip gracefully
			}
			rp.Values[name] = val
		}
		if len(rp.Values) > 0 {
			out = append(out, rp)
		}
	}
	return out
}

// anyToCty converts a YAML-parsed value (any) to a cty.Value guided by the
// declared variable type. It handles scalars, maps, lists, and objects.
func anyToCty(raw any, typ *tftypes.Type) (cty.Value, error) {
	if typ == nil {
		return cty.NilVal, fmt.Errorf("nil type")
	}
	switch typ.Kind {
	case tftypes.KindString:
		s, ok := raw.(string)
		if !ok {
			return cty.NilVal, fmt.Errorf("expected string, got %T", raw)
		}
		return cty.StringVal(s), nil

	case tftypes.KindBool:
		b, ok := raw.(bool)
		if !ok {
			return cty.NilVal, fmt.Errorf("expected bool, got %T", raw)
		}
		if b {
			return cty.True, nil
		}
		return cty.False, nil

	case tftypes.KindNumber:
		switch n := raw.(type) {
		case int:
			return cty.NumberIntVal(int64(n)), nil
		case float64:
			return cty.NumberFloatVal(n), nil
		default:
			return cty.NilVal, fmt.Errorf("expected number, got %T", raw)
		}

	case tftypes.KindMap:
		m, ok := raw.(map[string]any)
		if !ok {
			return cty.NilVal, fmt.Errorf("expected map, got %T", raw)
		}
		if len(m) == 0 {
			return cty.MapValEmpty(cty.String), nil
		}
		vals := make(map[string]cty.Value, len(m))
		for k, v := range m {
			cv, err := anyToCty(v, typ.Element)
			if err != nil {
				return cty.NilVal, fmt.Errorf("map[%q]: %w", k, err)
			}
			vals[k] = cv
		}
		return cty.MapVal(vals), nil

	case tftypes.KindObject:
		m, ok := raw.(map[string]any)
		if !ok {
			return cty.NilVal, fmt.Errorf("expected object (map), got %T", raw)
		}
		vals := make(map[string]cty.Value, len(typ.AttrOrder))
		// Start with defaults for all declared fields.
		for _, name := range typ.AttrOrder {
			attr := typ.Attributes[name]
			if attr.HasDefault {
				vals[name] = attr.Default
			}
		}
		// Override with preset values.
		for k, v := range m {
			attr, ok := typ.Attributes[k]
			if !ok {
				continue // unknown field; skip
			}
			cv, err := anyToCty(v, attr.Type)
			if err != nil {
				continue // type mismatch; skip
			}
			vals[k] = cv
		}
		return cty.ObjectVal(vals), nil

	case tftypes.KindList, tftypes.KindSet:
		slice, ok := raw.([]any)
		if !ok {
			return cty.NilVal, fmt.Errorf("expected list/set, got %T", raw)
		}
		if len(slice) == 0 {
			if typ.Kind == tftypes.KindSet {
				return cty.SetValEmpty(cty.String), nil
			}
			return cty.ListValEmpty(cty.String), nil
		}
		vals := make([]cty.Value, 0, len(slice))
		for i, item := range slice {
			cv, err := anyToCty(item, typ.Element)
			if err != nil {
				return cty.NilVal, fmt.Errorf("[%d]: %w", i, err)
			}
			vals = append(vals, cv)
		}
		if typ.Kind == tftypes.KindSet {
			return cty.SetVal(vals), nil
		}
		return cty.ListVal(vals), nil
	}

	return cty.NilVal, fmt.Errorf("unsupported type kind: %v", typ.Kind)
}
