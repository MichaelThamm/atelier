package bootstrap

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// LocalPresetDirName is the directory Atelier walks up looking for personal
// `.tfvars` bundles. One shared directory at a parent (e.g.
// `tf-testing/atelier.presets/`) is inherited by every wrapper beneath it
// (ADR-0032).
const LocalPresetDirName = wrapper.PresetsDir

// VarFile is a discoverable bundle: the name used with `--var-file`, the file
// it resolves to, and where it came from.
type VarFile struct {
	Name   string // identity for --var-file (filename without .tfvars)
	Path   string // absolute path on disk
	Source string // "local" (walk-up personal) or "repo" (committed to the module)
	// Display is what the user sees: an absolute path for local bundles, a
	// clone-relative path for repo bundles.
	Display string
	// Description is the leading comment block of the file, used by the TUI
	// picker in place of the old preset `description`.
	Description string
}

// LocalVarFiles walks up from wrapperDir (to $HOME or the filesystem root)
// collecting `<dir>/atelier.presets/*.tfvars`. A nearer file wins on a name
// collision, so a wrapper-local bundle overrides a shared one. The result is
// sorted by name.
func LocalVarFiles(wrapperDir string) []VarFile {
	if wrapperDir == "" {
		return nil
	}
	dir, err := filepath.Abs(wrapperDir)
	if err != nil {
		return nil
	}
	home, _ := os.UserHomeDir()

	seen := map[string]bool{}
	var out []VarFile
	for {
		presetDir := filepath.Join(dir, LocalPresetDirName)
		entries, err := os.ReadDir(presetDir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".tfvars") {
					continue
				}
				name := strings.TrimSuffix(e.Name(), ".tfvars")
				if seen[name] {
					continue
				}
				seen[name] = true
				p := filepath.Join(presetDir, e.Name())
				out = append(out, VarFile{Name: name, Path: p, Source: "local", Display: p, Description: bundleDescription(p)})
			}
		}
		if home != "" && dir == home {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RepoVarFiles returns the `.tfvars` files committed to the cloned module
// repository, as clone-relative entries. Search directories are as for
// ResolveVarFile (module root, module examples, then repo-level examples).
func RepoVarFiles(cloneDir, modulePath string) []VarFile {
	if cloneDir == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []VarFile
	for _, dir := range VarFileSearchDirs(cloneDir, modulePath) {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", ".terraform", "node_modules", ".venv":
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".tfvars") {
				return nil
			}
			name := strings.TrimSuffix(d.Name(), ".tfvars")
			if seen[name] {
				return nil
			}
			seen[name] = true
			rel := p
			if r, rerr := filepath.Rel(cloneDir, p); rerr == nil {
				rel = r
			}
			out = append(out, VarFile{Name: name, Path: p, Source: "repo", Display: rel, Description: bundleDescription(p)})
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ListAllVarFiles returns the union of local (walk-up) and repo bundles, local
// first. Names present in both sources are shown twice so `--list-var-files`
// makes the override relationship visible.
func ListAllVarFiles(wrapperDir, cloneDir, modulePath string) []VarFile {
	out := LocalVarFiles(wrapperDir)
	out = append(out, RepoVarFiles(cloneDir, modulePath)...)
	return out
}

// VarFileSearchDirs returns the directories searched for a repo-local
// `--var-file <name>`, in priority order (ADR-0032):
//
//  1. the module directory itself,
//  2. `<module>/examples/`,
//  3. `<repo>/terraform/examples/`,
//  4. `<repo>/examples/`.
//
// The module-adjacent locations are primary: a value file is bound to that
// module's variable schema, so it belongs beside it. The repo-level fallbacks
// exist for a shared catalog.
func VarFileSearchDirs(cloneDir, modulePath string) []string {
	moduleDir := filepath.Join(cloneDir, filepath.Clean(modulePath))
	dirs := []string{moduleDir, filepath.Join(moduleDir, "examples")}
	if cloneDir != "" {
		dirs = append(dirs,
			filepath.Join(cloneDir, "terraform", "examples"),
			filepath.Join(cloneDir, "examples"),
		)
	}
	return dirs
}

// ResolveVarFile resolves one `--var-file` argument to a concrete path, in
// precedence order (ADR-0032):
//
//  1. an existing local filesystem path,
//  2. a personal walk-up bundle in `atelier.presets/<name>.tfvars` (nearest
//     ancestor wins), so a user can override a product example by name,
//  3. a bundle committed to the cloned module repo.
//
// Reports false when nothing matches.
func ResolveVarFile(wrapperDir, cloneDir, modulePath, ref string) (string, bool) {
	if ref == "" {
		return "", false
	}
	if fi, err := os.Stat(ref); err == nil && !fi.IsDir() {
		return ref, true
	}
	if !validVarFileRef(ref) {
		return "", false
	}

	name := strings.TrimSuffix(ref, ".tfvars")
	if wrapperDir != "" {
		for _, f := range LocalVarFiles(wrapperDir) {
			if f.Name == name {
				return f.Path, true
			}
		}
	}

	if cloneDir == "" {
		return "", false
	}
	for _, dir := range VarFileSearchDirs(cloneDir, modulePath) {
		for _, cand := range varFileNameCandidates(ref) {
			p := filepath.Join(dir, cand)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p, true
			}
		}
	}
	return "", false
}

// ResolveVarFiles resolves every `--var-file` argument in order. On the first
// miss it returns an error that lists the bundles discoverable local and in
// the clone, so names are discoverable rather than guessed.
func ResolveVarFiles(wrapperDir, cloneDir, modulePath string, refs []string) ([]string, error) {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		p, ok := ResolveVarFile(wrapperDir, cloneDir, modulePath, ref)
		if !ok {
			avail := ListAllVarFiles(wrapperDir, cloneDir, modulePath)
			if len(avail) == 0 {
				return nil, fmt.Errorf("var-file %q not found (no .tfvars bundles discovered)", ref)
			}
			lines := make([]string, 0, len(avail))
			for _, f := range avail {
				lines = append(lines, fmt.Sprintf("[%s] %s (%s)", f.Source, f.Name, f.Display))
			}
			return nil, fmt.Errorf("var-file %q not found; available:\n  %s",
				ref, strings.Join(lines, "\n  "))
		}
		out = append(out, p)
	}
	return out, nil
}

// bundleDescription returns the file's leading comment block, with the leading
// "#" stripped and lines joined by spaces. It gives a bundle a human
// description without a separate metadata file.
func bundleDescription(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			if len(lines) > 0 {
				break
			}
			continue
		}
		if !strings.HasPrefix(t, "#") {
			break
		}
		lines = append(lines, strings.TrimSpace(strings.TrimPrefix(t, "#")))
	}
	return strings.Join(lines, " ")
}

// varFileNameCandidates expands a bare name to the filenames tried in each
// search directory. The `.tfvars` suffix is optional on the command line.
func varFileNameCandidates(ref string) []string {
	if strings.HasSuffix(ref, ".tfvars") {
		return []string{ref}
	}
	return []string{ref, ref + ".tfvars"}
}

// validVarFileRef rejects names that could escape the clone tree or denote a
// directory rather than a file. Absolute paths and parent traversal are not
// name-like; callers wanting those pass a local path, which is tried first.
func validVarFileRef(ref string) bool {
	if filepath.IsAbs(ref) {
		return false
	}
	clean := filepath.Clean(ref)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}
