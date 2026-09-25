package wrapper

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// Files and markers for the opt-in pass-through wrapper shape (ADR-0031).
const (
	// TFVarsFile is the wrapper-local values file Atelier reads and writes in
	// TFVarsMode. It is a standard Terraform variable file.
	TFVarsFile = "terraform.tfvars"

	// PresetsDir is the directory Atelier walks up looking for personal
	// `.tfvars` bundles (ADR-0032). Shared with the TUI's save-preset flow.
	PresetsDir = "atelier.presets"

	// TFVarsModeMarker is written at the top of a generated pass-through
	// main.tf. Its presence is how Atelier detects the mode on a later open,
	// so it must survive a deleted .atelier/ directory (which is regenerable).
	TFVarsModeMarker = "# atelier:tfvars — generated pass-through wrapper; edit values in terraform.tfvars."

	// tfVarsModeSentinel is the stable substring used for detection, kept
	// separate from the human-readable marker so the prose can change freely.
	tfVarsModeSentinel = "atelier:tfvars"

	// tfVarsHeader introduces the generated terraform.tfvars, both at bootstrap
	// and on every TUI save, so the file's provenance survives edits.
	tfVarsHeader = "# terraform.tfvars — Atelier-managed values for this wrapper.\n" +
		"# Only values that differ from the module's defaults appear here; delete a\n" +
		"# line to fall back to the declared default. Edit by hand or in the Atelier TUI.\n"
)

// writeTFVarsMode writes the three derived files of the pass-through shape:
// the sparse values file, the mirrored variables interface, and the generated
// forwarding main.tf. All are atomically written because the TUI's
// `terraform validate` watcher can race a save.
func (s *State) writeTFVarsMode() error {
	if err := writeAtomic(filepath.Join(s.Dir, TFVarsFile), s.RenderTFVars(), 0o644); err != nil {
		return fmt.Errorf("write terraform.tfvars: %w", err)
	}
	if err := writeAtomic(filepath.Join(s.Dir, VariablesTF), s.RenderVariablesTF(), 0o644); err != nil {
		return fmt.Errorf("write variables.tf: %w", err)
	}
	if err := writeAtomic(filepath.Join(s.Dir, MainTF), s.RenderPassthroughMain(), 0o644); err != nil {
		return fmt.Errorf("write main.tf: %w", err)
	}
	return nil
}

// RenderVariablesTF returns the mirrored root variables.tf: the module's own
// `variable` blocks, verbatim, concatenated. Re-emitting the raw blocks is
// what preserves type constraints, optional() defaults, validation,
// sensitive, and nullable exactly rather than approximating them.
func (s *State) RenderVariablesTF() []byte {
	blocks := make([]string, 0, len(s.Vars))
	for i := range s.Vars {
		if raw := strings.TrimSpace(s.Vars[i].Raw); raw != "" {
			blocks = append(blocks, raw)
		}
	}
	return RenderVariablesTFFromBlocks(blocks)
}

// RenderVariablesTFFromBlocks formats verbatim variable block sources into a
// variables.tf. Exposed for bootstrap, which has block sources but not a
// fully-assembled State.
func RenderVariablesTFFromBlocks(blocks []string) []byte {
	var b strings.Builder
	for _, raw := range blocks {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		b.WriteString(raw)
		b.WriteString("\n\n")
	}
	return hclwrite.Format([]byte(b.String()))
}

// RenderPassthroughMain returns the generated forwarding main.tf. Every
// declared variable becomes `name = var.name`, except where the user has a
// preserved expression (a wired reference Atelier can't model as a value),
// which is re-emitted verbatim so it overrides the forward. Meta-arguments
// (count, for_each, providers, ...) from UnknownAttrs are preserved too.
func (s *State) RenderPassthroughMain() []byte {
	names := make([]string, 0, len(s.Vars))
	rawByName := make(map[string][]byte, len(s.UnknownAttrs))
	for _, ra := range s.UnknownAttrs {
		rawByName[ra.Name] = ra.RawExpr
	}
	for i := range s.Vars {
		names = append(names, s.Vars[i].Name)
	}

	meta := make([]RawAttr, 0, len(s.UnknownAttrs))
	for _, ra := range s.UnknownAttrs {
		if s.FindVar(ra.Name) == nil {
			meta = append(meta, ra)
		}
	}
	return renderPassthroughMain(s.ModuleBlockName, s.Source, names, rawByName, meta)
}

// RenderPassthroughMainFromNames is the bootstrap-time entry point: it has
// the module's variable names but not a State. No expressions exist at
// bootstrap, so every variable simply forwards from its root counterpart.
func RenderPassthroughMainFromNames(blockName, source string, names []string) []byte {
	return renderPassthroughMain(blockName, source, names, nil, nil)
}

func renderPassthroughMain(blockName, source string, names []string, rawByName map[string][]byte, meta []RawAttr) []byte {
	file := hclwrite.NewEmptyFile()
	file.Body().AppendUnstructuredTokens(hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenComment, Bytes: []byte(TFVarsModeMarker + "\n\n")},
	})
	body := file.Body().AppendNewBlock("module", []string{blockName}).Body()
	body.SetAttributeValue("source", cty.StringVal(source))

	for _, name := range names {
		if rawExpr, ok := rawByName[name]; ok && len(bytes.TrimSpace(rawExpr)) > 0 {
			if toks := exprTokens(bytes.TrimSpace(rawExpr)); toks != nil {
				body.SetAttributeRaw(name, toks)
				continue
			}
		}
		if toks := exprTokens([]byte("var." + name)); toks != nil {
			body.SetAttributeRaw(name, toks)
		}
	}
	for _, ra := range meta {
		if toks := exprTokens(bytes.TrimSpace(ra.RawExpr)); toks != nil {
			body.SetAttributeRaw(ra.Name, toks)
		}
	}
	return hclwrite.Format(file.Bytes())
}

// RenderTFVars returns the sparse terraform.tfvars: only variables whose
// current value differs from their declared default (ADR-0007's rule, now
// applied to the values file). Values are constants only — reference
// expressions cannot live in a .tfvars file, so they stay in main.tf.
func (s *State) RenderTFVars() []byte {
	file := hclwrite.NewEmptyFile()
	file.Body().AppendUnstructuredTokens(hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenComment, Bytes: []byte(tfVarsHeader + "\n")},
	})
	body := file.Body()
	for i := range s.Vars {
		v := &s.Vars[i]
		current, _ := s.VariableValue(v.Name)
		if !ShouldEmit(v, current) {
			continue
		}
		writeVal := SparseValue(v, current)
		if writeVal == cty.NilVal {
			continue
		}
		body.SetAttributeValue(v.Name, writeVal)
	}
	return hclwrite.Format(file.Bytes())
}

// RenderTFVarsValues renders a sparse `.tfvars` body from concrete values:
// declared variables in declaration order, only those present in values, and
// only those that differ from their default (ADR-0007's ShouldEmit/SparseValue
// rule). Unlike RenderTFVars it has no managed-file header, so it is suitable
// for authoring a user's personal bundle. Reference expressions are not
// supported (`.tfvars` is constants-only).
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

// ReadTFVars parses terraform.tfvars in dir and returns the values for
// declared variables. Diagnostics are discarded; callers that want them use
// ReadTFVarsFileChecked. A missing file yields an empty map.
func ReadTFVars(dir string, vars []tfvars.Variable) (map[string]cty.Value, error) {
	vals, _, err := readTFVarsFile(filepath.Join(dir, TFVarsFile), vars)
	return vals, err
}

// ReadTFVarsFile parses an arbitrary Terraform variable file at path and
// returns the evaluable values for the declared variables, discarding
// diagnostics. Used for the managed terraform.tfvars.
func ReadTFVarsFile(path string, vars []tfvars.Variable) (map[string]cty.Value, error) {
	vals, _, err := readTFVarsFile(path, vars)
	return vals, err
}

// ReadTFVarsFileChecked is ReadTFVarsFile with diagnostics: it reports
// undeclared names and type mismatches, and skips both rather than writing
// values that would be silently ignored or produce an invalid .tfvars. Used
// by `module add --var-file` (ADR-0032).
func ReadTFVarsFileChecked(path string, vars []tfvars.Variable) (map[string]cty.Value, VarFileDiagnostics, error) {
	return readTFVarsFile(path, vars)
}

// ReadTFVarsAll parses terraform.tfvars without a schema filter, returning
// every evaluable attribute. Used in the degraded (unresolvable-ref) path,
// where the module's variable list is unknown but the user's values must be
// preserved across a ref switch.
func ReadTFVarsAll(dir string) (map[string]cty.Value, error) {
	vals, _, err := readTFVarsFile(filepath.Join(dir, TFVarsFile), nil)
	return vals, err
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

	// A nil variable list means "no schema" (degraded path): accept every
	// evaluable attribute without checking names or types.
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

// IsTFVarsMode reports whether dir holds a pass-through wrapper, detected
// from the marker in main.tf. Reading the marker (rather than a file under
// .atelier/) keeps detection working after .atelier/ is deleted, which SPEC
// §4 permits at any time.
func IsTFVarsMode(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, MainTF))
	if err != nil {
		return false
	}
	return bytes.Contains(data, []byte(tfVarsModeSentinel))
}

// FilterPassthroughAttrs drops the generated `name = var.name` forwarding
// attributes from a parsed main.tf, leaving only meta-arguments and genuine
// wired expressions. Without this, opening a pass-through wrapper would treat
// every variable as "wired to a reference", since the forwarding expression
// can't be evaluated without a Terraform context.
func FilterPassthroughAttrs(unknown []RawAttr, vars []tfvars.Variable) []RawAttr {
	declared := make(map[string]bool, len(vars))
	for i := range vars {
		declared[vars[i].Name] = true
	}
	out := unknown[:0]
	for _, ra := range unknown {
		if declared[ra.Name] && strings.TrimSpace(string(ra.RawExpr)) == "var."+ra.Name {
			continue
		}
		out = append(out, ra)
	}
	return out
}
