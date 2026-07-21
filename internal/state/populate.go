package state

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/wrapper"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// NormalizeNullAttributes replaces null state values with non-null defaults
// for the given resource addresses, working directly on terraform.tfstate
// bytes without any lossy parsed-model round-trip.
//
// For each instance at one of the given addresses, every attribute listed
// in defaults whose current value is JSON-null is replaced with the
// provided default. Attributes that are already non-null are left alone.
//
// This solves the post-import diff where the Juju provider stores null
// for empty maps (config={}, storage_directives={}) but the module
// variable type uses optional(T, default={}). Terraform treats null in
// an optional attribute as "use default", creating a diff that triggers
// RequiresReplace.
func NormalizeNullAttributes(dir string, addresses []string, defaults map[string]interface{}) error {
	if len(defaults) == 0 {
		return nil
	}
	path := filepath.Join(dir, "terraform.tfstate")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var raw rawState
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Version == 0 {
		return nil
	}

	addrSet := make(map[string]bool, len(addresses))
	for _, a := range addresses {
		addrSet[a] = true
	}

	normalized := 0
	for i := range raw.Resources {
		r := &raw.Resources[i]
		for j := range r.Instances {
			inst := &r.Instances[j]
			addr := buildAddress(r.Module, r.Type, r.Name, inst.IndexKey)
			if !addrSet[addr] {
				continue
			}
			for k, def := range defaults {
				if v, ok := inst.Attributes[k]; ok && v == nil {
					inst.Attributes[k] = def
					normalized++
				}
			}
		}
	}

	if normalized == 0 {
		return nil
	}

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return writeStateFile(path, out)
}

// ComputeNullDefaults reads the wrapper variable declarations and returns a
// map of attribute names to their non-null default values. Only attributes
// whose declared type is a primitive or collection (not dynamic/any) are
// included, and only when the default is non-null.
//
// The caller passes this map to NormalizeNullAttributes so that null state
// values are replaced with what Terraform would compute from the variable
// defaults.
func ComputeNullDefaults(ws *wrapper.State) map[string]interface{} {
	if ws == nil {
		return nil
	}

	// Collect all object-typed variables and their attribute defaults.
	defaults := map[string]interface{}{}
	for _, v := range ws.Vars {
		if v.Type == nil || v.Type.Kind != tftypes.KindObject {
			continue
		}
		for _, attrName := range v.Type.AttrOrder {
			attr := v.Type.Attributes[attrName]
			if _, exists := defaults[attrName]; exists {
				continue // already collected from another var
			}
			def := attrDefault(attr)
			if def != nil {
				defaults[attrName] = def
			}
		}
	}
	return defaults
}

// attrDefault returns the JSON-serialisable default for an object attribute,
// mirroring what Terraform uses when the user doesn't set the attribute.
func attrDefault(attr *tftypes.ObjectAttr) interface{} {
	if attr.HasDefault {
		return ctyDefaultToRaw(attr.Default, attr.Type)
	}
	return rawZeroForType(attr.Type)
}

// rawZeroForType returns the Terraform zero value as a plain Go value.
func rawZeroForType(t *tftypes.Type) interface{} {
	if t == nil {
		return nil
	}
	switch t.Kind {
	case tftypes.KindString:
		return ""
	case tftypes.KindBool:
		return false
	case tftypes.KindNumber:
		return float64(0)
	case tftypes.KindMap:
		return map[string]interface{}{}
	case tftypes.KindObject:
		return map[string]interface{}{}
	case tftypes.KindList, tftypes.KindSet:
		return []interface{}{}
	default:
		return nil
	}
}

// ctyDefaultToRaw converts a cty.Value default to a raw Go value for JSON
// serialisation.
func ctyDefaultToRaw(val cty.Value, t *tftypes.Type) interface{} {
	if val == cty.NilVal || val.IsNull() {
		return nil
	}
	ct := toCtyType(t)
	data, err := ctyjson.Marshal(val, ct)
	if err != nil {
		return rawZeroForType(t)
	}
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return rawZeroForType(t)
	}
	return raw
}

// toCtyType converts an Atelier Type to a cty.Type.
func toCtyType(t *tftypes.Type) cty.Type {
	if t == nil {
		return cty.DynamicPseudoType
	}
	switch t.Kind {
	case tftypes.KindString:
		return cty.String
	case tftypes.KindBool:
		return cty.Bool
	case tftypes.KindNumber:
		return cty.Number
	case tftypes.KindMap:
		return cty.Map(toCtyType(t.Element))
	case tftypes.KindList:
		return cty.List(toCtyType(t.Element))
	case tftypes.KindSet:
		return cty.Set(toCtyType(t.Element))
	case tftypes.KindObject:
		atys := make(map[string]cty.Type, len(t.Attributes))
		for _, name := range t.AttrOrder {
			atys[name] = toCtyType(t.Attributes[name].Type)
		}
		return cty.Object(atys)
	}
	return cty.DynamicPseudoType
}
