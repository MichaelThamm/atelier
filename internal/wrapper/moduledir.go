package wrapper

import (
	"os"
	"path/filepath"

	"github.com/MichaelThamm/atelier/internal/modulesource"
)

// CloneDir is the directory under .atelier/ where Atelier clones module
// repositories; each repository gets a subdirectory named after it.
func CloneDir(wrapperDir string) string {
	return filepath.Join(wrapperDir, AtelierDir, "clone")
}

// ModuleDirPath is the local directory a module block's source resolves to,
// whether or not it exists: the absolute local path, or the clone path under
// .atelier/clone/. It is the one place that maps a source address to a
// directory, so the read paths (rootgraph, outputs re-export) cannot drift.
func ModuleDirPath(wrapperDir, source string) string {
	if source == "" {
		return ""
	}
	if modulesource.IsLocal(source) {
		p := source
		if !filepath.IsAbs(p) {
			p = filepath.Join(wrapperDir, p)
		}
		return p
	}
	sub := modulesource.ModulePath(source)
	clone := filepath.Join(CloneDir(wrapperDir), modulesource.RepoBasename(modulesource.Remote(source)))
	if sub == "" {
		return clone
	}
	return filepath.Join(clone, filepath.Clean(sub))
}

// ModuleDir is ModuleDirPath when that directory exists on disk, and reports
// whether it does.
func ModuleDir(wrapperDir, source string) (string, bool) {
	p := ModuleDirPath(wrapperDir, source)
	if p == "" || !isDir(p) {
		return "", false
	}
	return p, true
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
