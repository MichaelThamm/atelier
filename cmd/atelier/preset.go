package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/MichaelThamm/atelier/internal/manifest"
	"github.com/MichaelThamm/atelier/internal/tui"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// presetArgIsFile reports whether a --preset argument names an existing file
// rather than a named preset from walk-up atelier.local.yaml discovery. Any
// path that resolves on disk wins: it is the more specific instruction.
func presetArgIsFile(arg string) bool {
	info, err := os.Stat(arg)
	return err == nil && !info.IsDir()
}

// presetNameFromFile derives a display name for a standalone preset file from
// its base name (e.g. cos-s3.yaml → cos-s3). Only used in log output and when
// a file wraps its sets in a `sets:` map that provides no name.
func presetNameFromFile(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// presetFileToPresets loads a standalone preset file. Two shapes are accepted:
//
//   - The atelier.local.yaml schema (a top-level `modules:` list). All presets
//     from all module entries are returned; the caller's module matching is
//     unchanged because these are applied by variable name.
//   - A flat map of variable name → value (optionally wrapped in a single
//     top-level `sets:` map). This is the ergonomic shape for a one-purpose
//     CI/deployment preset file such as docs/examples/cos-s3.yaml.
func presetFileToPresets(path string) ([]manifest.Preset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read preset file: %w", err)
	}

	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse preset file %s: %w", path, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("preset file %s is empty", path)
	}

	if _, hasModules := raw["modules"]; hasModules {
		m, warns, err := manifest.Parse(bytes.NewReader(data))
		for _, w := range warns {
			fmt.Fprintln(os.Stderr, "warning:", w)
		}
		if err != nil {
			return nil, fmt.Errorf("parse preset file %s: %w", path, err)
		}
		var out []manifest.Preset
		for _, mod := range m.Modules {
			out = append(out, mod.Presets...)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("preset file %s declares no presets", path)
		}
		return out, nil
	}

	sets := raw
	if wrapped, ok := raw["sets"]; ok {
		m, ok := wrapped.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("preset file %s: 'sets' must be a map of variable names to values", path)
		}
		sets = m
	}

	return []manifest.Preset{{Name: presetNameFromFile(path), Sets: sets}}, nil
}

// applyPresetArgs applies --preset arguments to state, in order, so that later
// arguments override earlier ones. An argument that names an existing file is
// loaded as a standalone preset file; anything else is resolved as a named
// preset from walk-up atelier.local.yaml discovery.
//
// Values land in state.Values and are persisted by a subsequent State.Write,
// exactly as if they had been configured in the TUI.
func applyPresetArgs(dir string, state *wrapper.State, presetArgs []string) error {
	if len(presetArgs) == 0 {
		return nil
	}

	// Resolve the named presets from walk-up atelier.local.yaml discovery
	// once, then look each name up in the result.
	rawNamed, warns := manifest.LoadLocalPresets(dir, modulePathFromState(state))
	for _, w := range warns {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	resolvedNamed := tui.ResolvePresets(rawNamed, state.Vars)
	byName := make(map[string]tui.ResolvedPreset, len(resolvedNamed))
	for _, rp := range resolvedNamed {
		byName[rp.Name] = rp
	}

	var applied []tui.ResolvedPreset
	var appliedNames []string
	for _, arg := range presetArgs {
		if presetArgIsFile(arg) {
			presets, err := presetFileToPresets(arg)
			if err != nil {
				return err
			}
			for _, rp := range tui.ResolvePresets(presets, state.Vars) {
				applied = append(applied, rp)
				appliedNames = append(appliedNames, rp.Name)
			}
			continue
		}

		rp, ok := byName[arg]
		if !ok {
			available := make([]string, 0, len(resolvedNamed))
			for _, p := range resolvedNamed {
				available = append(available, p.Name)
			}
			sort.Strings(available)
			return fmt.Errorf("preset %q not found; available presets: %v", arg, available)
		}
		applied = append(applied, rp)
		appliedNames = append(appliedNames, arg)
	}

	if len(applied) == 0 {
		return fmt.Errorf("no presets applied from %s", strings.Join(presetArgs, ", "))
	}

	for _, rp := range applied {
		for name, val := range rp.Values {
			state.Values[name] = val
		}
	}
	fmt.Fprintf(os.Stderr, "Applied preset(s): %s\n", strings.Join(appliedNames, ", "))
	return nil
}
