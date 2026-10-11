package bootstrap

import (
	"os"
	"path/filepath"

	"github.com/MichaelThamm/atelier/internal/modulesource"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// RegenerateOutputs rewrites the wrapper's outputs.tf from the module blocks it
// currently declares, re-exporting each module's outputs as root outputs so
// `terraform output` works. A root output is named "<module>_<output>" so names
// never collide across modules, and an ephemeral module output is skipped (a
// root output cannot be ephemeral). It is called after a composition change:
// add, apply, rm, or a TUI save (which covers a ref switch).
func RegenerateOutputs(wrapperDir string) error {
	blocks, err := wrapper.ReadModuleBlocks(wrapperDir)
	if err != nil {
		return err
	}
	var exports []wrapper.OutputExport
	for _, b := range blocks {
		outs, err := ModuleOutputs(wrapperDir, b.Source)
		if err != nil {
			return err
		}
		for _, o := range outs {
			if o.Ephemeral {
				continue
			}
			exports = append(exports, wrapper.OutputExport{
				Name:      b.Name + "_" + o.Name,
				Module:    b.Name,
				Output:    o.Name,
				Sensitive: o.Sensitive,
			})
		}
	}
	return wrapper.WriteOutputsFile(wrapperDir, exports)
}

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
