package main

import (
	"github.com/MichaelThamm/atelier/internal/bootstrap"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// regenerateOutputs rewrites the wrapper's outputs.tf from the module blocks it
// currently declares, re-exporting each module's outputs as root outputs so
// `terraform output` works. It is called after a composition change (add,
// apply, rm). A root output is named "<module>_<output>" so names never collide
// across modules, and an ephemeral module output is skipped because a root
// output cannot be ephemeral.
func regenerateOutputs(wrapperDir string) error {
	blocks, err := wrapper.ReadModuleBlocks(wrapperDir)
	if err != nil {
		return err
	}
	var exports []wrapper.OutputExport
	for _, b := range blocks {
		outs, err := bootstrap.ModuleOutputs(wrapperDir, b.Source)
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
