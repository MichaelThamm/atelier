package wrapper

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// gitignoreContent is the .gitignore Atelier writes at bootstrap. Listed
// verbatim from SPEC §10.3.
const gitignoreContent = `# Managed by Atelier — extend freely below.
.atelier/
.terraform/
terraform.tfstate
terraform.tfstate.backup
*.tfstate
*.tfstate.backup
secrets.auto.tfvars
`

// readmeTemplate is the README.md scaffold. Plain enough to read; the user
// is free to overwrite or extend. The sensitive-values note (readmeSecretsNote)
// is appended only when the wrapper can actually hold secrets, so a wrapper
// with no sensitive fields carries no scary caveat.
const readmeTemplate = `# %s wrapper

This directory is a Terraform wrapper authored with [Atelier](https://github.com/MichaelThamm/atelier).

## Usage

%[2]sshell
terraform init
terraform plan
terraform apply
%[2]s

Atelier's internal state lives in %[3]s.atelier/%[3]s and is regenerable; the rest of
this directory is a normal Terraform project that runs without Atelier.
`

// readmeSecretsNote is appended to the README only when the wrapper has
// sensitive fields (sensitive variables, or providers that may need sensitive
// configuration). It is self-contained and actionable — no version string, no
// dangling "see the project README" pointer.
const readmeSecretsNote = "\n> Note: sensitive values are stored in `secrets.auto.tfvars` in plaintext and\n" +
	"> kept out of version control by `.gitignore`. Its only protection is your\n" +
	"> filesystem permissions — do not commit or share it. To keep a secret off\n" +
	"> disk, source it from an environment variable (`TF_VAR_<name>`) and remove\n" +
	"> its entry from that file.\n"

// BootstrapOptions captures the inputs to a fresh wrapper. The caller is the
// init flow (CLI / TUI launcher).
type BootstrapOptions struct {
	Dir               string
	ModuleBlockName   string
	Source            string
	ModuleDir         string // candidate path within the cloned repo (for the README only)
	RequiredProviders map[string]RequiredProvider
	Providers         []ProviderBlock
	Variables         []TFVar // tfvars.Variable satisfies this interface.
}

// TFVar is the small interface bootstrap consumes from a tfvars.Variable —
// just enough to decide which placeholders to emit. Public so callers
// outside this package can produce []TFVar from their own types.
type TFVar interface {
	VarName() string
	VarIsRequired() bool
	VarIsSensitive() bool
}

// hasSecrets reports whether the wrapper can hold sensitive values, and thus
// whether the README should carry the secrets-handling note. It is true when a
// module variable is declared sensitive, a provider attribute is sensitive, or
// the module configures any provider at all — the last because provider
// configuration commonly includes sensitive attributes and the provider schema
// (which would confirm this) is not yet fetched at bootstrap time, so we
// conservatively include the note whenever providers are present. A wrapper
// with no sensitive variables and no providers gets a clean README.
func (o BootstrapOptions) hasSecrets() bool {
	for _, v := range o.Variables {
		if v.VarIsSensitive() {
			return true
		}
	}
	for _, p := range o.Providers {
		for _, a := range p.Attributes {
			if a.Sensitive {
				return true
			}
		}
	}
	return len(o.RequiredProviders) > 0
}

// Bootstrap writes the initial wrapper files into dir. Files that already
// exist are not overwritten (SPEC §6.1: init preserves existing files
// alongside the new wrapper).
func Bootstrap(opts BootstrapOptions) error {
	if opts.Dir == "" {
		return fmt.Errorf("bootstrap: Dir is required")
	}
	if opts.ModuleBlockName == "" {
		return fmt.Errorf("bootstrap: ModuleBlockName is required")
	}
	if opts.Source == "" {
		return fmt.Errorf("bootstrap: Source is required")
	}

	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return fmt.Errorf("create wrapper dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(opts.Dir, AtelierDir), 0o755); err != nil {
		return fmt.Errorf("create .atelier: %w", err)
	}

	if err := writeIfMissing(filepath.Join(opts.Dir, GitignoreFile), []byte(gitignoreContent)); err != nil {
		return err
	}
	readme := fmt.Sprintf(readmeTemplate, opts.ModuleBlockName, "```", "`")
	if opts.hasSecrets() {
		readme += readmeSecretsNote
	}
	if err := writeIfMissing(filepath.Join(opts.Dir, ReadmeFile), []byte(readme)); err != nil {
		return err
	}

	if err := bootstrapVersions(opts); err != nil {
		return err
	}
	if err := bootstrapProviders(opts); err != nil {
		return err
	}
	if err := bootstrapMain(opts); err != nil {
		return err
	}
	if err := bootstrapVariablesTF(opts); err != nil {
		return err
	}
	return nil
}

func writeIfMissing(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, data, 0o644)
}

func bootstrapMain(opts BootstrapOptions) error {
	path := filepath.Join(opts.Dir, MainTF)
	if _, err := os.Stat(path); err == nil {
		// Don't overwrite a hand-edited main.tf. The init flow's caller
		// already validates that this case is the error path (SPEC §6.1).
		return nil
	}
	file := hclwrite.NewEmptyFile()
	block := file.Body().AppendNewBlock("module", []string{opts.ModuleBlockName})
	body := block.Body()
	body.SetAttributeValue("source", cty.StringVal(opts.Source))
	// Required variables get TODO placeholders so the user immediately sees
	// what needs filling.
	for _, v := range opts.Variables {
		if v.VarIsRequired() {
			body.SetAttributeValue(v.VarName(), cty.NullVal(cty.DynamicPseudoType))
		}
	}
	return os.WriteFile(path, hclwrite.Format(file.Bytes()), 0o644)
}

func bootstrapVersions(opts BootstrapOptions) error {
	path := filepath.Join(opts.Dir, VersionsTF)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if len(opts.RequiredProviders) == 0 {
		return nil
	}
	file := hclwrite.NewEmptyFile()
	tf := file.Body().AppendNewBlock("terraform", nil)
	rp := tf.Body().AppendNewBlock("required_providers", nil)
	rpBody := rp.Body()

	names := make([]string, 0, len(opts.RequiredProviders))
	for n := range opts.RequiredProviders {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := opts.RequiredProviders[n]
		fields := map[string]cty.Value{}
		if p.Source != "" {
			fields["source"] = cty.StringVal(p.Source)
		}
		if p.Version != "" {
			fields["version"] = cty.StringVal(p.Version)
		}
		if len(fields) == 0 {
			continue
		}
		rpBody.SetAttributeValue(n, cty.ObjectVal(fields))
	}
	return os.WriteFile(path, hclwrite.Format(file.Bytes()), 0o644)
}

func bootstrapProviders(opts BootstrapOptions) error {
	path := filepath.Join(opts.Dir, ProvidersTF)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if len(opts.Providers) == 0 {
		return nil
	}
	file := hclwrite.NewEmptyFile()
	for _, p := range opts.Providers {
		block := file.Body().AppendNewBlock("provider", []string{p.LocalName})
		body := block.Body()
		for _, attr := range p.Attributes {
			if attr.Sensitive {
				ref := attr.VariableRef
				if ref == "" {
					ref = providerVarName(p.LocalName, attr.Name)
				}
				body.SetAttributeTraversal(attr.Name, hcl.Traversal{
					hcl.TraverseRoot{Name: "var"},
					hcl.TraverseAttr{Name: ref},
				})
				continue
			}
			if !attr.Value.IsNull() && attr.Value.Type() != cty.NilType {
				body.SetAttributeValue(attr.Name, attr.Value)
			}
		}
	}
	return os.WriteFile(path, hclwrite.Format(file.Bytes()), 0o644)
}

func bootstrapVariablesTF(opts BootstrapOptions) error {
	// Emit variables.tf with one variable declaration per sensitive provider
	// attribute (the wrapper-level variables that back the var.<name>
	// references in providers.tf).
	var sensitives []ProviderAttr
	for _, p := range opts.Providers {
		for _, a := range p.Attributes {
			if a.Sensitive {
				attrCopy := a
				if attrCopy.VariableRef == "" {
					attrCopy.VariableRef = providerVarName(p.LocalName, a.Name)
				}
				sensitives = append(sensitives, attrCopy)
			}
		}
	}
	if len(sensitives) == 0 {
		return nil
	}
	path := filepath.Join(opts.Dir, VariablesTF)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	file := hclwrite.NewEmptyFile()
	for _, a := range sensitives {
		block := file.Body().AppendNewBlock("variable", []string{a.VariableRef})
		body := block.Body()
		body.SetAttributeTraversal("type", hcl.Traversal{hcl.TraverseRoot{Name: "string"}})
		body.SetAttributeValue("sensitive", cty.True)
	}
	return os.WriteFile(path, hclwrite.Format(file.Bytes()), 0o644)
}

// providerVarName composes the variable name backing a sensitive provider
// attribute. Example: provider "juju", attribute "password" → "juju_password".
func providerVarName(provider, attr string) string {
	prov := strings.ReplaceAll(provider, "-", "_")
	return prov + "_" + attr
}
