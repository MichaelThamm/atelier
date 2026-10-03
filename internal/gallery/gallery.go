// Package gallery is Atelier's curated set of quick starts for real Terraform
// modules: the module address, a pinned revision, optional presets, and the
// one-liner that uses them. The data is embedded so `--var-file` can resolve a
// gallery preset and `atelier gallery list` can render it with no network and no
// checkout.
//
// A gallery entry is machine-first; the human interface is the CLI and the TUI
// preset picker, not committed Markdown. See ADR-0035.
package gallery

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
)

//go:embed gallery.json
var manifestJSON []byte

//go:embed presets
var presetsFS embed.FS

// Entry is one gallery quick start. Presets is optional: a module that deploys
// with its defaults needs none.
type Entry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Module      string `json:"module"`
	Subdir      string `json:"subdir"`
	Ref         string `json:"ref"`
	// Block is the explicit HCL block name (`--as`), for a module whose
	// candidate directory gives a poor derived name (e.g. `product`).
	Block string `json:"block,omitempty"`
	// Presets are `.tfvars` bundle names in presets/, without the suffix. They
	// are composed: an entry applies all of them, in order (later wins), as its
	// curated default scenario. A module that deploys with its defaults needs
	// none.
	Presets []string `json:"presets,omitempty"`
	// AvailablePresets are bundles the entry offers but does not compose. The
	// user opts in with `--var-file`, and they surface in `--list-var-files` and
	// the TUI picker. Declaring them here rather than leaving them unreferenced
	// keeps the module association, so `gallery-check` can bind each against
	// this entry's module instead of letting it drift unchecked.
	AvailablePresets []string `json:"available_presets,omitempty"`
	// Requires lists inputs the entry leaves to the user — deployment-specific
	// values such as a Juju model UUID or S3 credentials. An entry is either a
	// bare name (rendered as `--var name=<name>`) or `name=value`, which both
	// renders a working default and gives the gallery check a value that
	// satisfies the variable's type and validation rules.
	Requires []string `json:"requires,omitempty"`
}

// ApplyArgs is the user-facing command, as argv tokens, to apply this entry:
// `atelier apply <name>`, with a `--var` for each input the entry leaves to the
// user. Returned as tokens so callers can render or join it.
func (e Entry) ApplyArgs() []string {
	args := []string{"atelier", "apply", e.Name}
	for _, r := range e.Requires {
		name, value, hasValue := strings.Cut(r, "=")
		if hasValue {
			args = append(args, "--var", name+"="+value)
			continue
		}
		args = append(args, "--var", name+"=<"+name+">")
	}
	return args
}

// RequiresNames returns the variable names in Requires, stripping any `=value`
// default.
func (e Entry) RequiresNames() []string {
	out := make([]string, 0, len(e.Requires))
	for _, r := range e.Requires {
		name, _, _ := strings.Cut(r, "=")
		out = append(out, name)
	}
	return out
}

// AllPresets returns every preset the entry offers, composed ones first.
func (e Entry) AllPresets() []string {
	return append(append([]string{}, e.Presets...), e.AvailablePresets...)
}

// ApplyCommand renders the user-facing command as a single line.
func (e Entry) ApplyCommand() string {
	return strings.Join(e.ApplyArgs(), " ")
}

// ShortRef is the pinned ref shortened for display. The full ref stays in the
// manifest and the module source; only the human-facing form is abbreviated.
func (e Entry) ShortRef() string {
	if len(e.Ref) > 12 {
		return e.Ref[:12]
	}
	return e.Ref
}

// ScaffoldCommand renders the non-applying form CI runs: scaffold the entry by
// name, failing on any preset binding problem. Required inputs the gallery
// cannot supply are omitted, so the entry's static preset is still validated.
func (e Entry) ScaffoldCommand() string {
	return "atelier add " + e.Name + " --strict --yes"
}

// Find returns the entry with this name.
func Find(name string) (Entry, bool) {
	entries, err := List()
	if err != nil {
		return Entry{}, false
	}
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// List returns the gallery entries in name order. It fails if the embedded
// manifest is malformed, so a bad manifest is caught wherever the gallery is
// read rather than producing a partial list.
func List() ([]Entry, error) {
	var entries []Entry
	if err := json.Unmarshal(manifestJSON, &entries); err != nil {
		return nil, fmt.Errorf("gallery manifest: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	if err := validate(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func validate(entries []Entry) error {
	seen := map[string]bool{}
	referenced := map[string]bool{}
	for _, e := range entries {
		switch {
		case e.Name == "":
			return fmt.Errorf("gallery manifest: entry with empty name")
		case seen[e.Name]:
			return fmt.Errorf("gallery manifest: duplicate name %q", e.Name)
		case e.Module == "":
			return fmt.Errorf("gallery manifest: %q has no module", e.Name)
		case e.Ref == "":
			return fmt.Errorf("gallery manifest: %q has no ref", e.Name)
		}
		for _, p := range e.AllPresets() {
			if !HasPreset(p) {
				return fmt.Errorf("gallery manifest: %q names preset %q, which is not embedded", e.Name, p)
			}
			referenced[p] = true
		}
		// A preset in both lists would be applied on every run and also read as
		// an opt-in, so its status would depend on which list a reader checked
		// first. Reject rather than pick one.
		for _, p := range e.Presets {
			if slices.Contains(e.AvailablePresets, p) {
				return fmt.Errorf("gallery manifest: %q lists preset %q as both composed and available", e.Name, p)
			}
		}
		seen[e.Name] = true
	}
	// An embedded preset no entry claims — composed or available — would never
	// be exercised by `gallery-check`, so it could rot unnoticed.
	files, err := presetsFS.ReadDir("presets")
	if err != nil {
		return fmt.Errorf("gallery presets: %w", err)
	}
	for _, f := range files {
		name := strings.TrimSuffix(f.Name(), ".tfvars")
		if !referenced[name] {
			return fmt.Errorf("gallery: preset %q is embedded but no entry references it", name)
		}
	}
	return nil
}

// Lookup returns the named preset's contents.
func Lookup(name string) ([]byte, bool) {
	data, err := presetsFS.ReadFile("presets/" + name + ".tfvars")
	if err != nil {
		return nil, false
	}
	return data, true
}

// HasPreset reports whether the gallery ships a preset with this name.
func HasPreset(name string) bool {
	_, ok := Lookup(name)
	return ok
}

var (
	presetDirOnce sync.Once
	presetDirPath string
	presetDirErr  error
)

// PresetDir materializes the embedded presets into a content-addressed cache
// directory and returns it. Callers that need a filesystem path — the var-file
// resolver and the TUI picker — use this; the embedded bytes remain the source
// of truth. The directory is keyed by a hash of the manifest and preset
// contents, so a new binary never reuses a stale one.
func PresetDir() (string, error) {
	presetDirOnce.Do(func() {
		presetDirPath, presetDirErr = materialize()
	})
	return presetDirPath, presetDirErr
}

// PresetPath returns the materialized path for a preset, if the gallery has it.
func PresetPath(name string) (string, bool) {
	if !HasPreset(name) {
		return "", false
	}
	dir, err := PresetDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(dir, name+".tfvars"), true
}

func materialize() (string, error) {
	entries, err := List()
	if err != nil {
		return "", err
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}

	h := sha256.New()
	h.Write(manifestJSON)
	for _, e := range entries {
		for _, p := range e.AllPresets() {
			data, ok := Lookup(p)
			if !ok {
				return "", fmt.Errorf("gallery: preset %q listed but not embedded", p)
			}
			h.Write(data)
		}
	}
	dir := filepath.Join(base, "atelier", "gallery", hex.EncodeToString(h.Sum(nil))[:16])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, e := range entries {
		for _, p := range e.AllPresets() {
			path := filepath.Join(dir, p+".tfvars")
			if _, err := os.Stat(path); err == nil {
				continue
			}
			data, _ := Lookup(p)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return "", err
			}
		}
	}
	return dir, nil
}
