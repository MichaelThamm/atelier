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

	"github.com/canonical/atelier/internal/tfvars"
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

	// Iterate variables in declaration order so the output is stable and
	// matches the user's mental model of the module.
	for i := range s.Vars {
		v := &s.Vars[i]
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
		body.SetAttributeValue(v.Name, writeVal)
	}

	// Write to a temporary file and rename for atomicity — important because
	// the file watcher (`terraform validate` in the TUI) may race the write.
	return writeAtomic(mainPath, hclwrite.Format(file.Bytes()))
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

// writeAtomic writes data to path via a sibling temp file and rename, which
// is atomic on POSIX filesystems.
func writeAtomic(path string, data []byte) error {
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
	return writeAtomic(filepath.Join(s.Dir, SecretsAuto), file.Bytes())
}

// VariableDeclByName is a tiny helper exposing State's variable slice as a
// lookup function. Useful for callers that want to write isolated parts of
// the state without exposing the slice directly.
func (s *State) VariableDeclByName(name string) (*tfvars.Variable, bool) {
	v := s.FindVar(name)
	return v, v != nil
}
