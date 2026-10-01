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
// bundles (ADR-0031). Shared with the TUI's save-preset flow.
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
// renamed or its type changes (ADR-0031).
type VarFileDiagnostics struct {
	// Unknown lists attribute names the module does not declare. From
	// LintTFVars it also carries keys nested inside object values as dotted
	// paths (e.g. "worker.resources").
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
// and the TUI picker (ADR-0031).
func ReadTFVarsFileChecked(path string, vars []tfvars.Variable) (map[string]cty.Value, VarFileDiagnostics, error) {
	return readTFVarsFile(path, vars)
}

func readTFVarsFile(path string, vars []tfvars.Variable) (map[string]cty.Value, VarFileDiagnostics, error) {
	var diags VarFileDiagnostics
	raw, err := rawTFVarsValues(path)
	if err != nil {
		return nil, diags, err
	}

	var declared map[string]*tfvars.Variable
	if vars != nil {
		declared = make(map[string]*tfvars.Variable, len(vars))
		for i := range vars {
			declared[vars[i].Name] = &vars[i]
		}
	}

	out := map[string]cty.Value{}
	for name, val := range raw {
		decl, known := declared[name]
		if declared != nil && !known {
			diags.Unknown = append(diags.Unknown, name)
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

// rawTFVarsValues parses a `.tfvars` file and evaluates each attribute to a
// concrete cty value, without consulting a module schema. Reference
// expressions are skipped: `.tfvars` is constants-only. The apply path
// (readTFVarsFile) then coerces each value to its declared type; the lint path
// (LintTFVars) walks the raw value against the declared type instead, so it
// keeps the optional() metadata that coercion discards.
func rawTFVarsValues(path string) (map[string]cty.Value, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]cty.Value{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}

	parser := hclparse.NewParser()
	f, hclDiags := parser.ParseHCL(data, path)
	if hclDiags.HasErrors() {
		return nil, fmt.Errorf("parse %s: %s", filepath.Base(path), hclDiags.Error())
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("parse %s: unexpected body type", filepath.Base(path))
	}

	out := map[string]cty.Value{}
	for name, attr := range body.Attributes {
		val, dd := attr.Expr.Value(nil)
		if dd.HasErrors() {
			continue
		}
		out[name] = val
	}
	return out, nil
}

// LintTFVars checks a `.tfvars` bundle against a module's declared variables
// and reports what Terraform would reject: attribute names the module does not
// declare, keys nested inside object values that the object type does not
// declare, and values that do not fit a scalar (or scalar-collection) type.
// Unknown nested keys are reported as dotted paths (e.g. "worker.resources").
//
// Nested object values are checked for key existence only, never for type:
// Atelier's cty view drops optional() metadata, so shape-checking a legitimate
// partial object would produce false mismatches (ADR-0031). Object-containing
// types are walked for undeclared keys and left to Terraform for shape errors.
// Used by `atelier presets lint` to keep a committed bundle from rotting when a
// field is renamed.
func LintTFVars(path string, vars []tfvars.Variable) (VarFileDiagnostics, error) {
	var diags VarFileDiagnostics
	if _, err := os.Stat(path); err != nil {
		return diags, fmt.Errorf("var-file %s: %w", path, err)
	}
	raw, err := rawTFVarsValues(path)
	if err != nil {
		return diags, err
	}

	declared := make(map[string]*tfvars.Variable, len(vars))
	for i := range vars {
		declared[vars[i].Name] = &vars[i]
	}
	for name, val := range raw {
		decl, known := declared[name]
		if !known {
			diags.Unknown = append(diags.Unknown, name)
			continue
		}
		if !hasComplexShape(decl.Type) {
			if _, cerr := coerceToDeclaredType(decl, val); cerr != nil {
				diags.Mismatched = append(diags.Mismatched, fmt.Sprintf("%s: %v", name, cerr))
				continue
			}
		}
		lintValueAgainstType(name, val, decl.Type, &diags)
	}
	sort.Strings(diags.Unknown)
	sort.Strings(diags.Mismatched)
	return diags, nil
}

// hasComplexShape reports whether a declared type contains an object or tuple
// anywhere. Atelier deliberately does not type-check those (ADR-0031): the
// apply path skips cty conversion for them, and the lint path checks key
// existence instead.
func hasComplexShape(t *tftypes.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case tftypes.KindObject, tftypes.KindTuple:
		return true
	case tftypes.KindList, tftypes.KindSet, tftypes.KindMap:
		return hasComplexShape(t.Element)
	}
	return false
}

// lintValueAgainstType walks a parsed value against the declared type and
// records object keys the type does not declare. It descends through objects,
// maps, lists, sets, and tuples, so a mistyped field is caught at any depth;
// scalar and any types are leaves.
func lintValueAgainstType(path string, val cty.Value, typ *tftypes.Type, diags *VarFileDiagnostics) {
	if typ == nil || val == cty.NilVal || val.IsNull() || !val.IsKnown() {
		return
	}
	switch typ.Kind {
	case tftypes.KindObject:
		if typ.Attributes == nil || !isObjectish(val) {
			return
		}
		for k, v := range val.AsValueMap() {
			attr, ok := typ.Attributes[k]
			if !ok {
				diags.Unknown = append(diags.Unknown, path+"."+k)
				continue
			}
			lintValueAgainstType(path+"."+k, v, attr.Type, diags)
		}
	case tftypes.KindMap:
		if !isObjectish(val) {
			return
		}
		for k, v := range val.AsValueMap() {
			lintValueAgainstType(path+"."+k, v, typ.Element, diags)
		}
	case tftypes.KindList, tftypes.KindSet, tftypes.KindTuple:
		if !val.CanIterateElements() {
			return
		}
		i := 0
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			el := typ.Element
			if typ.Kind == tftypes.KindTuple {
				if i >= len(typ.Tuple) {
					break
				}
				el = typ.Tuple[i]
			}
			lintValueAgainstType(fmt.Sprintf("%s[%d]", path, i), v, el, diags)
			i++
		}
	}
}

// coerceToDeclaredType converts a parsed value to the variable's declared
// type, returning an error when it does not fit. Types containing an object or
// tuple are left as-is: cty.Types built from Atelier's model lose optional-field
// metadata, so converting a legitimate partial object — including one nested in
// a list/set/map — would produce false mismatches. Terraform catches
// nested-shape errors itself.
func coerceToDeclaredType(decl *tfvars.Variable, val cty.Value) (cty.Value, error) {
	if decl == nil || decl.Type == nil {
		return val, nil
	}
	if decl.Type.Kind == tftypes.KindAny || hasComplexShape(decl.Type) {
		return val, nil
	}
	return convert.Convert(val, tftypes.CtyType(decl.Type))
}
