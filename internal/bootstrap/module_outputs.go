package bootstrap

import (
	"os"
	"path/filepath"

	"github.com/MichaelThamm/atelier/internal/modulesource"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// ModuleOutputs returns the outputs a module block declares, resolving the
// block's source to a local directory or an existing .atelier/clone checkout.
// It returns (nil, nil) when the module is not available locally — the caller
// then simply re-exports nothing for it.
func ModuleOutputs(wrapperDir, source string) ([]tfvars.Output, error) {
	dir, ok := ModuleDirForSource(wrapperDir, source)
	if !ok {
		return nil, nil
	}
	mod, err := tfvars.LoadModule(dir)
	if err != nil {
		return nil, err
	}
	return mod.Outputs, nil
}

// ModuleDirForSource resolves a module block's source to the directory holding
// its .tf files: a local path relative to the wrapper, or an existing clone
// under .atelier/clone/. It is the read-side counterpart to ResolveAndClone.
func ModuleDirForSource(wrapperDir, source string) (string, bool) {
	if source == "" {
		return "", false
	}
	if modulesource.IsLocal(source) {
		p := source
		if !filepath.IsAbs(p) {
			p = filepath.Join(wrapperDir, p)
		}
		if isDir(p) {
			return p, true
		}
		return "", false
	}
	sub := modulesource.ModulePath(source)
	clone := filepath.Join(CloneSubdir(wrapperDir), modulesource.RepoBasename(modulesource.Remote(source)))
	if sub != "" {
		clone = filepath.Join(clone, filepath.Clean(sub))
	}
	if isDir(clone) {
		return clone, true
	}
	return "", false
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
