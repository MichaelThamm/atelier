package wrapper

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// PresetsDir is the directory Atelier walks up looking for personal `.tfvars`
// bundles (ADR-0032). Shared with the TUI's save-preset flow.
const PresetsDir = "atelier.presets"

// RenderTFVarsValues renders a sparse `.tfvars` body from concrete values:
// declared variables in declaration order, only those present in values, and
// only those that differ from their default (ADR-0007's ShouldEmit/SparseValue
// rule). Used to author a preset bundle from the current configuration.
// Reference expressions are not supported (`.tfvars` is constants-only).
func RenderTFVarsValues(vars []tfvars.Variable, values map[string]cty.Value) []byte {
	file := hclwrite.NewEmptyFile()
	body := file.Body()
	for i := range vars {
		v := &vars[i]
		val, ok := values[v.Name]
		if !ok {
			continue
		}
		if !ShouldEmit(v, val) {
			continue
		}
		writeVal := SparseValue(v, val)
		if writeVal == cty.NilVal {
			continue
		}
		body.SetAttributeValue(v.Name, writeVal)
	}
	return hclwrite.Format(file.Bytes())
}

// VarFileDiagnostics reports what a variable file contained but Atelier could
// not apply: attribute names the module does not declare, and values that do
// not fit the declared type. Both are skipped rather than written, and are
// surfaced so a committed example cannot rot silently when a variable is
// renamed or its type changes (ADR-0032).
type VarFileDiagnostics struct {
	// Unknown lists attribute names the module does not declare.
	Unknown []string
	// Mismatched lists "<name>: <reason>" for values that are not convertible
	// to the declared variable type.
	Mismatched []string
}

// Empty reports whether the file applied cleanly.
func (d VarFileDiagnostics) Empty() bool {
	return len(d.Unknown) == 0 && len(d.Mismatched) == 0
}

// ReadTFVarsFileChecked parses a `.tfvars` file and returns the values for the
// declared variables, plus diagnostics. It reports undeclared names and type
// mismatches and skips both, rather than applying values that would be
// silently ignored or produce an invalid file. Used by `module add --var-file`
// and the TUI picker (ADR-0032).
func ReadTFVarsFileChecked(path string, vars []tfvars.Variable) (map[string]cty.Value, VarFileDiagnostics, error) {
	return readTFVarsFile(path, vars)
}

func readTFVarsFile(path string, vars []tfvars.Variable) (map[string]cty.Value, VarFileDiagnostics, error) {
	var diags VarFileDiagnostics
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]cty.Value{}, diags, nil
		}
		return nil, diags, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}

	parser := hclparse.NewParser()
	f, hclDiags := parser.ParseHCL(data, path)
	if hclDiags.HasErrors() {
		return nil, diags, fmt.Errorf("parse %s: %s", filepath.Base(path), hclDiags.Error())
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, diags, fmt.Errorf("parse %s: unexpected body type", filepath.Base(path))
	}

	var declared map[string]*tfvars.Variable
	if vars != nil {
		declared = make(map[string]*tfvars.Variable, len(vars))
		for i := range vars {
			declared[vars[i].Name] = &vars[i]
		}
	}

	out := map[string]cty.Value{}
	for name, attr := range body.Attributes {
		decl, known := declared[name]
		if declared != nil && !known {
			diags.Unknown = append(diags.Unknown, name)
			continue
		}
		val, dd := attr.Expr.Value(nil)
		if dd.HasErrors() {
			// Reference expressions are not valid in a .tfvars file; skip.
			continue
		}
		if decl != nil {
			converted, cerr := coerceToDeclaredType(decl, val)
			if cerr != nil {
				diags.Mismatched = append(diags.Mismatched, fmt.Sprintf("%s: %v", name, cerr))
				continue
			}
			val = converted
		}
		out[name] = val
	}
	sort.Strings(diags.Unknown)
	sort.Strings(diags.Mismatched)
	return out, diags, nil
}

// coerceToDeclaredType converts a parsed value to the variable's declared
// type, returning an error when it does not fit. Object and tuple types are
// left as-is: cty.Types built from Atelier's model lose optional-field
// metadata, so converting a legitimate partial object against them would
// produce false mismatches. Terraform catches nested-shape errors itself.
func coerceToDeclaredType(decl *tfvars.Variable, val cty.Value) (cty.Value, error) {
	if decl == nil || decl.Type == nil {
		return val, nil
	}
	switch decl.Type.Kind {
	case tftypes.KindAny, tftypes.KindObject, tftypes.KindTuple:
		return val, nil
	}
	return convert.Convert(val, tftypes.CtyType(decl.Type))
}
