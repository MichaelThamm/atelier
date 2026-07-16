package tui

import (
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/manifest"
	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
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

// ctyToAny converts a cty.Value into a YAML-serialisable Go value (the inverse
// of anyToCty). It walks the value itself rather than a declared type so it
// faithfully reproduces partial objects produced by wrapper.SparseValue —
// only the fields that differ from their defaults are present. Numbers are
// emitted as int64 when integral (so units render as `1`, not `1.0`) and
// float64 otherwise; nulls become nil (YAML `null`), which is a meaningful
// preset value (e.g. `s3_endpoint: null`).
func ctyToAny(v cty.Value) any {
	if v == cty.NilVal || v.IsNull() {
		return nil
	}
	t := v.Type()
	switch {
	case t == cty.Bool:
		return v.True()
	case t == cty.String:
		return v.AsString()
	case t == cty.Number:
		bf := v.AsBigFloat()
		if bf.IsInt() {
			i, _ := bf.Int64()
			return i
		}
		f, _ := bf.Float64()
		return f
	case t.IsObjectType() || t.IsMapType():
		out := make(map[string]any, v.LengthInt())
		for it := v.ElementIterator(); it.Next(); {
			k, ev := it.Element()
			out[k.AsString()] = ctyToAny(ev)
		}
		return out
	case t.IsListType() || t.IsSetType() || t.IsTupleType():
		out := make([]any, 0, v.LengthInt())
		for it := v.ElementIterator(); it.Next(); {
			_, ev := it.Element()
			out = append(out, ctyToAny(ev))
		}
		return out
	}
	return nil
}

// snapshotPreset builds a manifest.Preset capturing the primary module's
// current, non-default configuration — exactly the set of arguments Atelier
// would write to main.tf (wrapper.ShouldEmit + SparseValue, ADR-0007). This is
// the "generate a preset from what you have" path: it lets users bootstrap an
// atelier.local.yaml without hand-writing the DSL.
//
// It deliberately excludes two things that cannot round-trip through the
// preset DSL, or should never land in a shared file:
//
//   - Sensitive variables (secrets): never serialised, so a committed preset
//     can't leak credentials. Secrets remain hand-authorable in the file and
//     still load via [F] — this only governs generation.
//   - Wired reference expressions (var./module./data./local., preserved in
//     UnknownAttrs): the DSL holds concrete values only, so there is nothing
//     faithful to write.
//
// The returned int is the number of variables captured; the caller uses it to
// refuse saving an empty preset.
func snapshotPreset(s *wrapper.State, name, description string) (manifest.Preset, int) {
	raw := make(map[string]bool, len(s.UnknownAttrs))
	for _, ra := range s.UnknownAttrs {
		raw[ra.Name] = true
	}

	sets := make(map[string]any)
	for i := range s.Vars {
		v := &s.Vars[i]
		if v.Sensitive || raw[v.Name] {
			continue
		}
		current, _ := s.VariableValue(v.Name)
		if current == cty.NilVal {
			continue // required-but-unset: nothing concrete to capture
		}
		if !wrapper.ShouldEmit(v, current) {
			continue // at its default — omit, matching main.tf
		}
		writeVal := wrapper.SparseValue(v, current)
		if writeVal == cty.NilVal {
			continue
		}
		sets[v.Name] = ctyToAny(writeVal)
	}

	return manifest.Preset{Name: name, Description: description, Sets: sets}, len(sets)
}
