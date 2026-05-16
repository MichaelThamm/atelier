// Package manifest parses Atelier's optional atelier.yaml manifest file
// (SPEC §11, ADR-0010). The v1 schema is intentionally minimal: a single
// top-level modules: list, each module declaring path/name/optional
// description/optional groups.
//
// Parsing is strict in the sense that unknown top-level keys produce a
// warning the caller can surface; structural errors produce a hard error.
package manifest

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Manifest is the parsed atelier.yaml.
type Manifest struct {
	Modules []Module `yaml:"modules"`
}

// Module is one declared module candidate.
type Module struct {
	Path        string  `yaml:"path"`
	Name        string  `yaml:"name"`
	Description string  `yaml:"description,omitempty"`
	Groups      []Group `yaml:"groups,omitempty"`
}

// Group is a UI grouping of variables in the left pane.
type Group struct {
	Name      string   `yaml:"name"`
	Variables []string `yaml:"variables"`
}

// Load reads and parses atelier.yaml at the given path. Returns a
// (nil-Manifest, nil-error, nil-warnings) tuple if the file does not exist —
// the manifest is optional. Returns (manifest, nil, warnings) on parse
// success, possibly with non-fatal warnings (e.g. unknown fields).
func Load(path string) (*Manifest, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("open manifest %s: %w", path, err)
	}
	defer f.Close()
	return Parse(f)
}

// LoadFromRepo looks for atelier.yaml at the root of a cloned repository.
// Equivalent to Load(filepath.Join(repoRoot, "atelier.yaml")).
func LoadFromRepo(repoRoot string) (*Manifest, []string, error) {
	return Load(filepath.Join(repoRoot, "atelier.yaml"))
}

// Parse parses a manifest from any io.Reader. Exported for testing and for
// callers that have the manifest in memory.
func Parse(r io.Reader) (*Manifest, []string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, fmt.Errorf("read manifest: %w", err)
	}

	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("parse manifest yaml: %w", err)
	}
	if raw == nil {
		return nil, nil, fmt.Errorf("manifest is empty")
	}

	var warnings []string
	for k := range raw {
		if k != "modules" {
			warnings = append(warnings, fmt.Sprintf("unknown top-level key %q in atelier.yaml (ignored)", k))
		}
	}

	m := &Manifest{}
	if err := yaml.Unmarshal(data, m); err != nil {
		return nil, nil, fmt.Errorf("parse manifest yaml: %w", err)
	}
	if err := m.validate(); err != nil {
		return nil, nil, err
	}
	return m, warnings, nil
}

func (m *Manifest) validate() error {
	if len(m.Modules) == 0 {
		return fmt.Errorf("manifest: at least one module is required under modules:")
	}
	seenPaths := map[string]bool{}
	for i, mod := range m.Modules {
		if mod.Path == "" {
			return fmt.Errorf("manifest: modules[%d].path is required", i)
		}
		if mod.Name == "" {
			return fmt.Errorf("manifest: modules[%d].name is required (path=%q)", i, mod.Path)
		}
		if seenPaths[mod.Path] {
			return fmt.Errorf("manifest: duplicate module path %q", mod.Path)
		}
		seenPaths[mod.Path] = true
		for j, g := range mod.Groups {
			if g.Name == "" {
				return fmt.Errorf("manifest: modules[%d].groups[%d].name is required", i, j)
			}
			if len(g.Variables) == 0 {
				return fmt.Errorf("manifest: modules[%d].groups[%d] (%q) needs at least one variable", i, j, g.Name)
			}
		}
	}
	return nil
}

// FindModule returns the Module entry for the given path, or nil if not
// declared in the manifest.
func (m *Manifest) FindModule(path string) *Module {
	if m == nil {
		return nil
	}
	for i := range m.Modules {
		if m.Modules[i].Path == path {
			return &m.Modules[i]
		}
	}
	return nil
}

// ApplyGroups returns the ordered grouping of the variable names supplied,
// based on the module's manifest. Variables declared in the manifest but not
// present in `vars` are dropped (with a warning logged by the caller);
// variables present in `vars` but unmentioned in the manifest land in an
// implicit "Other" trailing group.
//
// If the module declares no groups (or no manifest is present), all
// variables appear in a single unnamed group in declaration order.
func ApplyGroups(mod *Module, vars []string) []ResolvedGroup {
	if mod == nil || len(mod.Groups) == 0 {
		return []ResolvedGroup{{Name: "", Variables: append([]string(nil), vars...)}}
	}
	present := map[string]bool{}
	for _, v := range vars {
		present[v] = true
	}
	var out []ResolvedGroup
	seen := map[string]bool{}
	for _, g := range mod.Groups {
		rg := ResolvedGroup{Name: g.Name}
		for _, v := range g.Variables {
			if present[v] {
				rg.Variables = append(rg.Variables, v)
				seen[v] = true
			}
		}
		if len(rg.Variables) > 0 {
			out = append(out, rg)
		}
	}
	var other []string
	for _, v := range vars {
		if !seen[v] {
			other = append(other, v)
		}
	}
	if len(other) > 0 {
		out = append(out, ResolvedGroup{Name: "Other", Variables: other})
	}
	return out
}

// ResolvedGroup is the result of ApplyGroups: a UI-ready grouping of
// variables for a specific module candidate.
type ResolvedGroup struct {
	Name      string
	Variables []string
}
