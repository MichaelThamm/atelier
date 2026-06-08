package wrapper

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// Files we manage in the wrapper.
const (
	MainTF       = "main.tf"
	VersionsTF   = "versions.tf"
	ProvidersTF  = "providers.tf"
	VariablesTF  = "variables.tf"
	SecretsAuto  = "secrets.auto.tfvars"
	GitignoreFile = ".gitignore"
	ReadmeFile   = "README.md"
	AtelierDir   = ".atelier"
)

// Write reflects the State to disk. It writes main.tf using the
// sparse-plus-required rule, and refreshes secrets.auto.tfvars from
// SecretValues. providers.tf, versions.tf, and the housekeeping files are
// only generated at bootstrap time; subsequent writes leave them alone (the
// user may have edited them).
func (s *State) Write() error {
	if err := s.writeMain(); err != nil {
		return fmt.Errorf("write main.tf: %w", err)
	}
	if err := s.writeSecrets(); err != nil {
		return fmt.Errorf("write secrets.auto.tfvars: %w", err)
	}
	return nil
}

func (s *State) writeMain() error {
	mainPath := filepath.Join(s.Dir, MainTF)

	var file *hclwrite.File
	if existing, err := os.ReadFile(mainPath); err == nil {
		var diags hcl.Diagnostics
		file, diags = hclwrite.ParseConfig(existing, mainPath, hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() {
			return fmt.Errorf("parse existing main.tf: %s", diags.Error())
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		file = hclwrite.NewEmptyFile()
	}

	block := s.findOrCreateModuleBlock(file)
	body := block.Body()

	// source attribute — always written.
	body.SetAttributeValue("source", cty.StringVal(s.Source))

	// Index any user-authored attributes Atelier can't represent as a
	// cty.Value — references (data.x.y, var.z, module.m.o), function calls,
	// interpolations, etc. The reader stores these verbatim in UnknownAttrs.
	// They must be preserved across saves rather than clobbered by the
	// value-based logic below (which would otherwise delete them because
	// there is no cty value to write). See ADR-0007 §10.2.
	rawByName := make(map[string][]byte, len(s.UnknownAttrs))
	for _, ra := range s.UnknownAttrs {
		rawByName[ra.Name] = ra.RawExpr
	}

	// Iterate variables in declaration order so the output is stable and
	// matches the user's mental model of the module.
	for i := range s.Vars {
		v := &s.Vars[i]

		// If the user set this variable to an expression Atelier couldn't
		// evaluate (and has not since overridden it with a concrete value via
		// the TUI), re-emit the original expression verbatim. We write it
		// explicitly from the stored expression bytes so the value survives
		// even on a from-scratch write, rather than depending on hclwrite
		// passthrough of an existing attribute.
		if rawExpr, isRaw := rawByName[v.Name]; isRaw {
			if _, hasConcrete := s.Values[v.Name]; !hasConcrete {
				if toks := exprTokens(rawExpr); toks != nil {
					body.SetAttributeRaw(v.Name, toks)
				}
				// If the expression couldn't be re-lexed (empty/corrupt
				// bytes), fall back to leaving any existing attribute in place.
				continue
			}
		}

		current, _ := s.VariableValue(v.Name)
		if !ShouldEmit(v, current) {
			body.RemoveAttribute(v.Name)
			continue
		}
		writeVal := SparseValue(v, current)
		if writeVal == cty.NilVal {
			body.RemoveAttribute(v.Name)
			continue
		}
		// For required-but-unset variables we emit a placeholder with a
		// loud TODO comment so the user can see in the file what's missing.
		if !v.HasDefault && current == cty.NilVal {
			body.SetAttributeRaw(v.Name, todoTokens(v.Type.String()))
			continue
		}
		// If the value is a string that looks like an HCL expression
		// reference (module.X.Y, var.X, local.X, data.X.Y["k"]), write it as
		// raw tokens so it's emitted unquoted as an expression.
		if writeVal.Type() == cty.String && isExpressionRef(writeVal.AsString()) {
			if toks := exprTokens([]byte(writeVal.AsString())); toks != nil {
				body.SetAttributeRaw(v.Name, toks)
				continue
			}
		}
		body.SetAttributeValue(v.Name, writeVal)
	}

	// Prune attributes orphaned by a schema change. When a ref switch moves the
	// module to a revision that dropped a variable (e.g. switching
	// observability-stack from track/2 to main removes `model_uuid`), the old
	// argument still sits in the parsed main.tf. The loop above only visits
	// variables in the *new* schema, so it never touches the orphan and the
	// stale line survives — making `terraform init` fail with "An argument
	// named X is not expected here." Remove any attribute that isn't the
	// source, a Terraform meta-argument, a currently-declared variable, or a
	// preserved user expression (UnknownAttrs).
	keep := make(map[string]bool, len(s.Vars)+len(s.UnknownAttrs)+8)
	keep["source"] = true
	for _, meta := range []string{"version", "count", "for_each", "providers", "depends_on"} {
		keep[meta] = true
	}
	for i := range s.Vars {
		keep[s.Vars[i].Name] = true
	}
	for _, ra := range s.UnknownAttrs {
		keep[ra.Name] = true
	}
	for name := range body.Attributes() {
		if !keep[name] {
			body.RemoveAttribute(name)
		}
	}

	// Write to a temporary file and rename for atomicity — important because
	// the file watcher (`terraform validate` in the TUI) may race the write.
	return writeAtomic(mainPath, hclwrite.Format(file.Bytes()), 0o644)
}

// findOrCreateModuleBlock locates the `module "<name>"` block (creating it if
// absent). Currently scoped to v1's single-instance-per-wrapper invariant —
// if there are multiple module blocks, the first one with a matching name
// wins; if none match, a new block is appended.
func (s *State) findOrCreateModuleBlock(file *hclwrite.File) *hclwrite.Block {
	for _, b := range file.Body().Blocks() {
		if b.Type() != "module" {
			continue
		}
		labels := b.Labels()
		if len(labels) == 1 && labels[0] == s.ModuleBlockName {
			return b
		}
	}
	return file.Body().AppendNewBlock("module", []string{s.ModuleBlockName})
}

// todoTokens returns a token sequence representing a TODO placeholder for a
// required variable the user hasn't filled in yet. The placeholder is an
// invalid HCL expression (a bare identifier `TODO`) followed by a comment;
// Terraform plan will error helpfully, and the user sees in main.tf exactly
// which variable is missing.
func todoTokens(typeHint string) hclwrite.Tokens {
	return hclwrite.Tokens{
		{Type: hclsyntax.TokenIdent, Bytes: []byte("null")},
		{Type: hclsyntax.TokenComment, Bytes: []byte(" # TODO: required (" + typeHint + ")\n"), SpacesBefore: 1},
	}
}

// isExpressionRef reports whether a string value should be emitted as an
// unquoted HCL expression rather than a quoted string literal. To avoid
// mistaking ordinary string values (e.g. "stable") for references, the value
// must begin with a known reference prefix (module./var./local./data.) AND
// parse as a valid HCL expression that references at least one symbol. This
// correctly handles dotted references, index access (data.x.y["k"]) and
// nested traversals.
func isExpressionRef(s string) bool {
	if !hasRefPrefix(s) {
		return false
	}
	expr, diags := hclsyntax.ParseExpression([]byte(s), "atelier-ref", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return false
	}
	return len(expr.Variables()) > 0
}

// hasRefPrefix reports whether s begins with one of Terraform's reference
// scopes followed by a dot.
func hasRefPrefix(s string) bool {
	for _, p := range []string{"module.", "var.", "local.", "data."} {
		if len(s) > len(p) && s[:len(p)] == p {
			return true
		}
	}
	return false
}

// exprTokens lexes raw HCL expression source (e.g. a reference such as
// `data.vault_generic_secret.s3.data["endpoint_url"]`, an index expression, or
// a function call) into hclwrite tokens suitable for SetAttributeRaw. It reuses
// hclwrite's own lexer by parsing a synthetic `_ = <expr>` assignment and
// extracting the expression tokens, so arbitrary expressions round-trip
// faithfully. Returns nil if the source is empty or can't be parsed.
func exprTokens(src []byte) hclwrite.Tokens {
	if len(src) == 0 {
		return nil
	}
	synthetic := append([]byte("_ = "), src...)
	f, diags := hclwrite.ParseConfig(synthetic, "atelier-rawexpr", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil
	}
	attr := f.Body().GetAttribute("_")
	if attr == nil {
		return nil
	}
	return attr.Expr().BuildTokens(nil)
}

// writeAtomic writes data to path via a sibling temp file and rename, which
// is atomic on POSIX filesystems. The perm argument sets the file mode on
// the temp file before rename so the target inherits the intended permissions.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".atelier-write-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// writeSecrets writes secrets.auto.tfvars containing one assignment per
// sensitive variable backing a sensitive provider attribute. Order is
// alphabetical for stable output.
func (s *State) writeSecrets() error {
	if len(s.SecretValues) == 0 {
		// Nothing to write; if a stale file exists, leave it alone (the user
		// may have hand-added entries).
		return nil
	}
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	names := make([]string, 0, len(s.SecretValues))
	for n := range s.SecretValues {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v := s.SecretValues[n]
		if v.IsNull() {
			continue
		}
		body.SetAttributeValue(n, v)
	}
	return writeAtomic(filepath.Join(s.Dir, SecretsAuto), file.Bytes(), 0o600)
}

// VariableDeclByName is a tiny helper exposing State's variable slice as a
// lookup function. Useful for callers that want to write isolated parts of
// the state without exposing the slice directly.
func (s *State) VariableDeclByName(name string) (*tfvars.Variable, bool) {
	v := s.FindVar(name)
	return v, v != nil
}
