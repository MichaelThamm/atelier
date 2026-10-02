package gallery

import (
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// PresetKeys returns the top-level attribute names an embedded preset sets,
// sorted. It is how `atelier gallery lint` decides whether a preset covers a
// required module input.
func PresetKeys(name string) ([]string, error) {
	data, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("gallery: no embedded preset %q", name)
	}
	parser := hclparse.NewParser()
	f, diags := parser.ParseHCL(data, name+".tfvars")
	if diags.HasErrors() {
		return nil, fmt.Errorf("gallery: parse preset %q: %s", name, diags.Error())
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("gallery: preset %q: unexpected body type", name)
	}
	keys := make([]string, 0, len(body.Attributes))
	for k := range body.Attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// Coverage compares an entry against the required inputs a module declares (its
// variables without a default) and reports two kinds of drift:
//
//   - uncovered: a required input that neither a preset nor `requires` supplies,
//     so `atelier apply` would fail before it could plan;
//   - stale: a `requires` entry the module no longer declares, which would be
//     offered to the user as something they must fill in.
//
// Both lists are sorted and empty when the entry is consistent.
func (e Entry) Coverage(required []string) (uncovered, stale []string, err error) {
	supplied := map[string]bool{}
	for _, p := range e.Presets {
		keys, err := PresetKeys(p)
		if err != nil {
			return nil, nil, err
		}
		for _, k := range keys {
			supplied[k] = true
		}
	}
	names := e.RequiresNames()
	for _, n := range names {
		supplied[n] = true
	}
	requiredSet := make(map[string]bool, len(required))
	for _, r := range required {
		requiredSet[r] = true
		if !supplied[r] {
			uncovered = append(uncovered, r)
		}
	}
	for _, n := range names {
		if !requiredSet[n] {
			stale = append(stale, n)
		}
	}
	sort.Strings(uncovered)
	sort.Strings(stale)
	return uncovered, stale, nil
}
