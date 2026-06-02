package wrapper

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// RemoveModuleOutputs removes output blocks from outputs.tf that reference
// the given module (i.e., outputs whose value traversal starts with
// module.<name>).
func RemoveModuleOutputs(dir, name string) error {
	outputsPath := filepath.Join(dir, OutputsTF)
	data, err := os.ReadFile(outputsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to clean up
		}
		return err
	}

	file, diags := hclwrite.ParseConfig(data, outputsPath, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return fmt.Errorf("parse outputs.tf: %s", diags.Error())
	}

	// Collect blocks to remove (can't remove while iterating).
	var toRemove []*hclwrite.Block
	prefix := "module." + name + "."
	for _, block := range file.Body().Blocks() {
		if block.Type() != "output" {
			continue
		}
		// Check if the value attribute references module.<name>.
		attr := block.Body().GetAttribute("value")
		if attr == nil {
			continue
		}
		// Inspect the raw tokens for the module reference.
		tokens := attr.BuildTokens(nil)
		src := tokensToString(tokens)
		if strings.Contains(src, prefix) {
			toRemove = append(toRemove, block)
		}
	}

	for _, block := range toRemove {
		file.Body().RemoveBlock(block)
	}

	result := hclwrite.Format(file.Bytes())
	// If file is now empty (just whitespace), remove it.
	if len(strings.TrimSpace(string(result))) == 0 {
		return os.Remove(outputsPath)
	}
	return writeAtomic(outputsPath, result, 0o644)
}

// tokensToString concatenates token bytes for simple string inspection.
func tokensToString(tokens hclwrite.Tokens) string {
	var sb strings.Builder
	for _, t := range tokens {
		sb.Write(t.Bytes)
	}
	return sb.String()
}
