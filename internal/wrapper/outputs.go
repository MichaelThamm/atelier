package wrapper

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// OutputsFile is the wrapper file Atelier generates to re-export each module's
// outputs as root outputs, so `terraform output` works without the user
// hand-writing output blocks. It is regenerated from the current module blocks.
const OutputsFile = "outputs.tf"

// OutputExport is one root output re-exporting a module's output.
type OutputExport struct {
	Name      string // root output name, "<module>_<output>"
	Module    string // module block name, e.g. "cos_lite"
	Output    string // the module's declared output name
	Sensitive bool   // propagated so Terraform does not reject the re-export
}

// WriteOutputsFile renders and atomically writes the wrapper's outputs.tf from
// the given exports, sorted by name. With no exports it removes the file.
func WriteOutputsFile(dir string, exports []OutputExport) error {
	path := filepath.Join(dir, OutputsFile)
	if len(exports) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	sorted := append([]OutputExport(nil), exports...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	f := hclwrite.NewEmptyFile()
	body := f.Body()
	for _, e := range sorted {
		block := body.AppendNewBlock("output", []string{e.Name})
		bb := block.Body()
		ref := "module." + e.Module + "." + e.Output
		if toks := exprTokens([]byte(ref)); toks != nil {
			bb.SetAttributeRaw("value", toks)
		} else {
			bb.SetAttributeValue("value", cty.StringVal(ref))
		}
		if e.Sensitive {
			bb.SetAttributeValue("sensitive", cty.True)
		}
		body.AppendNewline()
	}
	return writeAtomic(path, hclwrite.Format(f.Bytes()), 0o644)
}
