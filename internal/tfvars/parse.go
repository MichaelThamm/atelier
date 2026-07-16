// Package tfvars parses the `variable` blocks of a Terraform module to
// extract Atelier's view of each variable: its name, type, default value,
// sensitive flag, description, and the source range of the declaration.
//
// The package deliberately ignores `validation {}` blocks: Atelier runs
// `terraform validate` for validation rather than re-implementing HCL
// expression evaluation (ADR-0012).
package tfvars

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
)

// Variable describes a module-declared variable.
type Variable struct {
	Name        string
	Type        *tftypes.Type
	Description string
	Sensitive   bool
	Nullable    bool // Terraform 1.x: nullable = false disables null
	HasDefault  bool
	Default     cty.Value
	// DefaultRaw is the literal HCL source of the default expression. Useful
	// for surfacing values Atelier can't evaluate at parse time (e.g.
	// expressions referencing other variables — illegal in Terraform but
	// possible in practice).
	DefaultRaw string

	// DeclRange points back to the variable's declaration site in source.
	// Useful in error messages.
	DeclRange hcl.Range
}

// IsRequired reports whether the user must supply a value for this variable
// before Terraform will plan. A variable is required iff it has no default.
func (v *Variable) IsRequired() bool {
	return !v.HasDefault
}

// VarName satisfies the wrapper-package tfvarsLike adapter.
func (v Variable) VarName() string { return v.Name }

// VarIsRequired satisfies the wrapper-package tfvarsLike adapter.
func (v Variable) VarIsRequired() bool { return !v.HasDefault }

// VarIsSensitive satisfies the wrapper-package tfvarsLike adapter.
func (v Variable) VarIsSensitive() bool { return v.Sensitive }

// LoadDir scans every *.tf file at the top level of `dir` and returns the
// declared variables in declaration order (across files, sorted by source
// position within each file; files in alphabetical order). Sub-directories
// are not scanned: a Terraform module's variables are declared in its root.
func LoadDir(dir string) ([]Variable, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read module dir %s: %w", dir, err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".tf") && !strings.HasSuffix(name, "_test.tf") {
			paths = append(paths, filepath.Join(dir, name))
		}
	}
	sort.Strings(paths)

	parser := hclparse.NewParser()
	var vars []Variable
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		f, diags := parser.ParseHCL(data, path)
		if diags.HasErrors() {
			return nil, fmt.Errorf("parse %s: %s", path, diags.Error())
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			return nil, fmt.Errorf("parse %s: unexpected body type", path)
		}
		fileVars, err := variablesInBody(body)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		vars = append(vars, fileVars...)
	}
	return vars, nil
}

// LoadFile parses a single .tf file. Useful for unit tests.
func LoadFile(path string) ([]Variable, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(data, path)
}

// Parse parses .tf source. Exported for tests and callers with content in
// memory.
func Parse(src []byte, filename string) ([]Variable, error) {
	parser := hclparse.NewParser()
	f, diags := parser.ParseHCL(src, filename)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse %s: %s", filename, diags.Error())
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("parse %s: unexpected body type", filename)
	}
	return variablesInBody(body)
}

func variablesInBody(body *hclsyntax.Body) ([]Variable, error) {
	// Collect blocks in source order. hclsyntax.Body preserves declaration
	// order in .Blocks.
	var vars []Variable
	for _, block := range body.Blocks {
		if block.Type != "variable" {
			continue
		}
		if len(block.Labels) != 1 {
			return nil, fmt.Errorf("variable block at %s must have exactly one label (the variable name)", block.DefRange().String())
		}
		v, err := parseVariableBlock(block)
		if err != nil {
			return nil, err
		}
		vars = append(vars, v)
	}
	return vars, nil
}

func parseVariableBlock(block *hclsyntax.Block) (Variable, error) {
	v := Variable{
		Name:      block.Labels[0],
		Nullable:  true, // Terraform default
		DeclRange: block.DefRange(),
	}

	attrs := block.Body.Attributes

	// type
	if typeAttr, ok := attrs["type"]; ok {
		// The type attribute is special: it's an HCL expression that
		// expresses a type constraint. We unparse it back to source and feed
		// it to our type-expression parser.
		src := exprSource(typeAttr.Expr)
		typ, err := tftypes.ParseTypeExpr(src)
		if err != nil {
			return v, fmt.Errorf("variable %q type: %w", v.Name, err)
		}
		v.Type = typ
	} else {
		// No `type =` — Terraform infers as `any`.
		v.Type = &tftypes.Type{Kind: tftypes.KindAny}
	}

	// description
	if a, ok := attrs["description"]; ok {
		val, diags := a.Expr.Value(nil)
		if !diags.HasErrors() && val.Type() == cty.String && !val.IsNull() {
			v.Description = val.AsString()
		}
	}

	// sensitive
	if a, ok := attrs["sensitive"]; ok {
		val, diags := a.Expr.Value(nil)
		if !diags.HasErrors() && val.Type() == cty.Bool && !val.IsNull() {
			v.Sensitive = val.True()
		}
	}

	// nullable
	if a, ok := attrs["nullable"]; ok {
		val, diags := a.Expr.Value(nil)
		if !diags.HasErrors() && val.Type() == cty.Bool && !val.IsNull() {
			v.Nullable = val.True()
		}
	}

	// default
	if a, ok := attrs["default"]; ok {
		v.HasDefault = true
		v.DefaultRaw = exprSource(a.Expr)
		val, diags := a.Expr.Value(nil)
		if diags.HasErrors() {
			// The default may reference a function we can't evaluate (rare
			// but legal). Treat the default as unknown; the wrapper will
			// still work, the editor falls back to the raw text.
			v.Default = cty.NullVal(tftypes.CtyType(v.Type))
		} else {
			v.Default = val
		}
	}

	return v, nil
}

// exprSource recovers the literal source text of an HCL expression by
// slicing its source range out of the file's byte content (held by the HCL
// parser). For expressions we can identify directly (hclsyntax nodes) this
// works because hclsyntax preserves source bytes on the parsed nodes.
func exprSource(expr hcl.Expression) string {
	r := expr.Range()
	return rangeText(r, expr)
}

// rangeText extracts the bytes covered by `r` from the file backing `expr`.
// We do this by re-reading the file from disk if needed; hclsyntax does not
// retain the original byte slice on every expression node, but it does
// preserve enough to recover source via the file's source map. As a robust
// fallback we use the HCL file source registered with the parser when the
// caller used Parse / ParseHCL.
func rangeText(r hcl.Range, expr hcl.Expression) string {
	// Reading the file isn't ideal in hot paths; the parser caches files in
	// memory but doesn't expose them. For our use (per-variable, infrequent)
	// re-read is fine. If the filename is empty (in-memory parse), fall back
	// to walking the expression.
	if r.Filename != "" {
		data, err := os.ReadFile(r.Filename)
		if err == nil {
			if r.End.Byte <= len(data) && r.Start.Byte >= 0 && r.Start.Byte <= r.End.Byte {
				return string(data[r.Start.Byte:r.End.Byte])
			}
		}
	}
	// Fallback: format from the parsed structure. Good enough for primitive
	// types but not for the full general case.
	return fallbackExprText(expr)
}

func fallbackExprText(expr hcl.Expression) string {
	switch e := expr.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		if len(e.Traversal) == 1 {
			if root, ok := e.Traversal[0].(hcl.TraverseRoot); ok {
				return root.Name
			}
		}
		return "<expr>"
	case *hclsyntax.LiteralValueExpr:
		return e.Val.GoString()
	case *hclsyntax.TemplateExpr:
		if e.IsStringLiteral() {
			val, _ := e.Value(nil)
			return fmt.Sprintf("%q", val.AsString())
		}
		return "<template>"
	case *hclsyntax.FunctionCallExpr:
		var args []string
		for _, a := range e.Args {
			args = append(args, fallbackExprText(a))
		}
		return e.Name + "(" + strings.Join(args, ",") + ")"
	}
	return "<expr>"
}
