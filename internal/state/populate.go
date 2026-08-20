package state

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// SetAttributes overwrites specific attributes on specific resource instances in
// terraform.tfstate. `overrides` is keyed by resource address, then by attribute
// name; a nil value writes JSON null.
//
// This differs from NormalizeNullAttributes, which only ever replaces a null
// with a default. SetAttributes can write in either direction, which is required
// to correct a state value that is non-null where the configuration wants null.
// Addresses absent from state, and attributes absent from an instance, are
// ignored. Returns the number of attributes changed.
func SetAttributes(dir string, overrides map[string]map[string]interface{}) (int, error) {
	if len(overrides) == 0 {
		return 0, nil
	}
	path := filepath.Join(dir, "terraform.tfstate")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	var raw rawState
	if err := json.Unmarshal(data, &raw); err != nil {
		return 0, err
	}
	if raw.Version == 0 {
		return 0, nil
	}

	changed := 0
	for i := range raw.Resources {
		r := &raw.Resources[i]
		for j := range r.Instances {
			inst := &r.Instances[j]
			attrs, ok := overrides[buildAddress(r.Module, r.Type, r.Name, inst.IndexKey)]
			if !ok {
				continue
			}
			for k, want := range attrs {
				cur, present := inst.Attributes[k]
				if !present || sameJSON(cur, want) {
					continue
				}
				inst.Attributes[k] = want
				changed++
			}
		}
	}

	if changed == 0 {
		return 0, nil
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return 0, err
	}
	out = append(out, '\n')
	if err := writeStateFile(path, out); err != nil {
		return 0, err
	}
	return changed, nil
}

// sameJSON compares two decoded JSON values for equality.
func sameJSON(a, b interface{}) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ab) == string(bb)
}
