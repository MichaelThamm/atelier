package bootstrap

import (
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

	// Group blocks by the clone directory they resolve to. One clone is checked
	// out at one revision, so if the blocks sharing it are pinned at different
	// refs, the clone can only match one of them — reading it would re-export
	// outputs that a block at the other ref may not declare, which Terraform
	// then rejects. Such a repo is skipped rather than guessed at.
	type group struct {
		refs   map[string]bool
		blocks []wrapper.ModuleBlockInfo
	}
	groups := map[string]*group{}
	var order []string
	for _, b := range blocks {
		key, ref := cloneKeyAndRef(wrapperDir, b.Source)
		g := groups[key]
		if g == nil {
			g = &group{refs: map[string]bool{}}
			groups[key] = g
			order = append(order, key)
		}
		g.refs[ref] = true
		g.blocks = append(g.blocks, b)
	}

	var exports []wrapper.OutputExport
	for _, key := range order {
		g := groups[key]
		if len(g.refs) > 1 {
			continue
		}
		for _, b := range g.blocks {
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
	}
	return wrapper.WriteOutputsFile(wrapperDir, exports)
}

// cloneKeyAndRef returns the directory a block's source resolves to and its
// literal ref. Two blocks share a clone exactly when their keys are equal.
func cloneKeyAndRef(wrapperDir, source string) (key, ref string) {
	if source == "" {
		return "", ""
	}
	_, ref = modulesource.Decompose(source)
	return wrapper.ModuleDirPath(wrapperDir, source), ref
}

// ModuleOutputs returns the outputs a module block declares, resolving the
// block's source to a local directory or an existing .atelier/clone checkout.
// It returns (nil, nil) when the module is not available locally — the caller
// then simply re-exports nothing for it.
func ModuleOutputs(wrapperDir, source string) ([]tfvars.Output, error) {
	dir, ok := wrapper.ModuleDir(wrapperDir, source)
	if !ok {
		return nil, nil
	}
	mod, err := tfvars.LoadModule(dir)
	if err != nil {
		return nil, err
	}
	return mod.Outputs, nil
}
