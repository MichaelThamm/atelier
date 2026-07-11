// Package state reads terraform.tfstate (v4 format) directly from disk,
// without invoking terraform or validating configuration. This avoids the
// "missing required argument" failures that plague `terraform show` and
// `terraform state pull` when variables are unset.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Resource is a single managed resource or data source in the state.
type Resource struct {
	Module     string                 // e.g. "module.cos_lite.module.grafana"
	Mode       string                 // "managed" or "data"
	Type       string                 // e.g. "juju_application"
	Name       string                 // e.g. "grafana"
	Address    string                 // full address: module.cos_lite.juju_application.grafana
	Attributes map[string]interface{} // all attributes from the state
}

// Summary holds aggregate info about the state.
type Summary struct {
	Total    int
	ByModule map[string]int // module path → count
}

// State is the parsed representation of a terraform.tfstate file.
type State struct {
	Resources []Resource
	Summary   Summary
}

// Read parses terraform.tfstate from the given directory. Returns nil (no
// error) if the state file doesn't exist or is empty/trivial.
func Read(dir string) (*State, error) {
	path := filepath.Join(dir, "terraform.tfstate")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading state: %w", err)
	}
	return Parse(data)
}

// Parse decodes raw terraform.tfstate (v4) JSON into a State.
func Parse(data []byte) (*State, error) {
	var raw rawState
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decoding state: %w", err)
	}
	if raw.Version == 0 && len(raw.Resources) == 0 {
		return nil, nil
	}

	var resources []Resource
	byModule := make(map[string]int)

	for _, r := range raw.Resources {
		mod := r.Module
		for _, inst := range r.Instances {
			addr := buildAddress(mod, r.Type, r.Name, inst.IndexKey)
			resources = append(resources, Resource{
				Module:     mod,
				Mode:       r.Mode,
				Type:       r.Type,
				Name:       r.Name,
				Address:    addr,
				Attributes: inst.Attributes,
			})
			byModule[mod]++
		}
	}

	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Module != resources[j].Module {
			return resources[i].Module < resources[j].Module
		}
		return resources[i].Address < resources[j].Address
	})

	return &State{
		Resources: resources,
		Summary: Summary{
			Total:    len(resources),
			ByModule: byModule,
		},
	}, nil
}

// SummaryLine returns a compact one-liner like "State: 54 resources across 7 modules".
func (s *State) SummaryLine() string {
	if s == nil || s.Summary.Total == 0 {
		return "State: empty"
	}
	mods := len(s.Summary.ByModule)
	if mods <= 1 {
		return fmt.Sprintf("State: %d resource(s)", s.Summary.Total)
	}
	return fmt.Sprintf("State: %d resource(s) across %d modules", s.Summary.Total, mods)
}

// AttributeLines returns sorted "key = value" lines for a resource's attributes.
func (r *Resource) AttributeLines() []string {
	if len(r.Attributes) == 0 {
		return nil
	}
	keys := make([]string, 0, len(r.Attributes))
	for k := range r.Attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s = %s", k, formatValue(r.Attributes[k])))
	}
	return lines
}

func formatValue(v interface{}) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case string:
		return fmt.Sprintf("%q", val)
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case []interface{}:
		if len(val) == 0 {
			return "[]"
		}
		b, _ := json.Marshal(val)
		return string(b)
	case map[string]interface{}:
		if len(val) == 0 {
			return "{}"
		}
		b, _ := json.Marshal(val)
		return string(b)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func buildAddress(module, typ, name string, indexKey interface{}) string {
	prefix := ""
	if module != "" {
		prefix = module + "."
	}
	addr := fmt.Sprintf("%s%s.%s", prefix, typ, name)
	if indexKey != nil {
		switch k := indexKey.(type) {
		case string:
			addr += fmt.Sprintf("[%q]", k)
		case float64:
			addr += fmt.Sprintf("[%d]", int64(k))
		}
	}
	return addr
}

// rawState models the subset of terraform.tfstate v4 we care about.
type rawState struct {
	Version   int           `json:"version"`
	Resources []rawResource `json:"resources"`
}

type rawResource struct {
	Module    string        `json:"module"`
	Mode      string        `json:"mode"`
	Type      string        `json:"type"`
	Name      string        `json:"name"`
	Instances []rawInstance `json:"instances"`
}

type rawInstance struct {
	IndexKey   interface{}            `json:"index_key"`
	Attributes map[string]interface{} `json:"attributes"`
}
