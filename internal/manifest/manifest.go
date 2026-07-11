// Package manifest parses Atelier's optional local presets file,
// atelier.local.yaml (SPEC §11). This is a user-owned, wrapper-local file:
// Atelier never reads presets from the upstream module repository. The file
// is discovered by walking up from the wrapper directory, so a single file
// placed at a parent (e.g. tf-testing/atelier.local.yaml) is shared by every
// wrapper beneath it.
//
// The v1 schema is intentionally minimal: a single top-level modules: list,
// each module declaring a path and a set of presets. A module entry with
// path "." applies to the wrapper's primary module regardless of its upstream
// sub-path, which is the ergonomic default for local files.
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

// LocalFileName is the wrapper-local presets file Atelier discovers by
// walking up from the wrapper directory.
const LocalFileName = "atelier.local.yaml"

// PrimaryModulePath is the special module path that matches the wrapper's
// primary module regardless of its upstream sub-path. Local files use it so
// users don't have to track the module's path within the upstream repo.
const PrimaryModulePath = "."

// Manifest is the parsed atelier.local.yaml.
type Manifest struct {
	Modules []Module `yaml:"modules"`
}

// Module is one declared module's presets. Name and Description are optional
// for local files (they are not used to name candidates); only Path and
// Presets are consumed.
type Module struct {
	Path        string   `yaml:"path"`
	Name        string   `yaml:"name,omitempty"`
	Description string   `yaml:"description,omitempty"`
	Presets     []Preset `yaml:"presets,omitempty"`
}

// Preset is a named bundle of variable overrides a module maintainer
// declares. Users can apply a preset to bulk-set variables, then tweak.
type Preset struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description,omitempty"`
	Sets        map[string]any `yaml:"sets"`
}

// Load reads and parses an atelier.local.yaml at the given path. Returns a
// (nil-Manifest, nil-error, nil-warnings) tuple if the file does not exist —
// the file is optional. Returns (manifest, nil, warnings) on parse success,
// possibly with non-fatal warnings (e.g. unknown fields).
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

// LoadLocalPresets discovers atelier.local.yaml files by walking up from
// wrapperDir and returns the presets that apply to the wrapper's primary
// module, identified by primaryModulePath (the module's sub-path within its
// upstream repo, e.g. "terraform/cos").
//
// Discovery walks from wrapperDir up to the filesystem root (or $HOME,
// whichever comes first), collecting every atelier.local.yaml found. Presets
// from files nearer the wrapper take precedence: when two files declare a
// preset with the same name, the nearer file wins. Within a single file,
// presets from a module entry whose path matches primaryModulePath take
// precedence over those from a "." (primary) entry.
//
// Non-fatal parse issues are returned as warnings; a malformed file is
// skipped with a warning rather than aborting discovery.
func LoadLocalPresets(wrapperDir, primaryModulePath string) ([]Preset, []string) {
	dirs := ancestorDirs(wrapperDir)

	var warnings []string
	seen := map[string]bool{}
	var out []Preset

	// dirs is ordered nearest-first, which is the precedence we want: the
	// first file to define a given preset name wins.
	for _, dir := range dirs {
		path := filepath.Join(dir, LocalFileName)
		m, warns, err := Load(path)
		for _, w := range warns {
			warnings = append(warnings, fmt.Sprintf("%s: %s", path, w))
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v (skipped)", path, err))
			continue
		}
		if m == nil {
			continue // file absent
		}
		for _, p := range m.presetsForModule(primaryModulePath) {
			if seen[p.Name] {
				continue // nearer file already defined this preset
			}
			seen[p.Name] = true
			out = append(out, p)
		}
	}
	return out, warnings
}

// presetsForModule returns the presets that apply to primaryModulePath. An
// exact path match takes precedence over a PrimaryModulePath (".") entry when
// both are present.
func (m *Manifest) presetsForModule(primaryModulePath string) []Preset {
	if m == nil {
		return nil
	}
	var exact, primary []Preset
	for i := range m.Modules {
		switch m.Modules[i].Path {
		case primaryModulePath:
			exact = append(exact, m.Modules[i].Presets...)
		case PrimaryModulePath:
			primary = append(primary, m.Modules[i].Presets...)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return primary
}

// ancestorDirs returns dir and each of its parents, nearest-first, stopping
// at the filesystem root or the user's home directory (whichever is
// encountered first). $HOME itself is included; directories above it are not.
func ancestorDirs(dir string) []string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	home, _ := os.UserHomeDir()

	var dirs []string
	cur := abs
	for {
		dirs = append(dirs, cur)
		if home != "" && cur == home {
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break // reached filesystem root
		}
		cur = parent
	}
	return dirs
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
			warnings = append(warnings, fmt.Sprintf("unknown top-level key %q in atelier.local.yaml (ignored)", k))
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
		if seenPaths[mod.Path] {
			return fmt.Errorf("manifest: duplicate module path %q", mod.Path)
		}
		seenPaths[mod.Path] = true
		for j, p := range mod.Presets {
			if p.Name == "" {
				return fmt.Errorf("manifest: modules[%d].presets[%d].name is required", i, j)
			}
			if len(p.Sets) == 0 {
				return fmt.Errorf("manifest: modules[%d].presets[%d] (%q) needs at least one entry in sets", i, j, p.Name)
			}
		}
	}
	return nil
}




