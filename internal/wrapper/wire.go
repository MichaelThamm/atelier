package wrapper

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// WireModuleInput sets `input = expr` on the named module block in the
// wrapper's main.tf, leaving every other attribute — and the rest of the
// file — untouched. expr is HCL source such as `module.cos_lite.cos_offers`.
//
// It is the write side of a proposed integration (ADR-0062). The wire is plain
// HCL, so the wrapper stays standard Terraform, and the edit is atomic and
// AST-backed, so hand edits and comments survive (ADR-0007).
func WireModuleInput(dir, blockName, input, expr string) error {
	mainPath := filepath.Join(dir, MainTF)
	existing, err := os.ReadFile(mainPath)
	if err != nil {
		return err
	}
	file, diags := hclwrite.ParseConfig(existing, mainPath, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return fmt.Errorf("parse existing main.tf: %s", diags.Error())
	}
	toks := exprTokens([]byte(expr))
	if toks == nil {
		return fmt.Errorf("invalid integration expression %q", expr)
	}
	for _, b := range file.Body().Blocks() {
		if b.Type() != "module" {
			continue
		}
		labels := b.Labels()
		if len(labels) == 1 && labels[0] == blockName {
			b.Body().SetAttributeRaw(input, toks)
			return writeAtomic(mainPath, hclwrite.Format(file.Bytes()), 0o644)
		}
	}
	return fmt.Errorf("module block %q not found in %s", blockName, MainTF)
}
