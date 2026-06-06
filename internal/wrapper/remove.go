package wrapper

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// RemoveModuleBlock removes the `module "<name>"` block from main.tf.
func RemoveModuleBlock(dir, name string) error {
	mainPath := filepath.Join(dir, MainTF)
	data, err := os.ReadFile(mainPath)
	if err != nil {
		return fmt.Errorf("read main.tf: %w", err)
	}

	file, diags := hclwrite.ParseConfig(data, mainPath, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return fmt.Errorf("parse main.tf: %s", diags.Error())
	}

	found := false
	for _, block := range file.Body().Blocks() {
		if block.Type() != "module" {
			continue
		}
		labels := block.Labels()
		if len(labels) == 1 && labels[0] == name {
			file.Body().RemoveBlock(block)
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("module %q not found in main.tf", name)
	}

	return writeAtomic(mainPath, hclwrite.Format(file.Bytes()), 0o644)
}
