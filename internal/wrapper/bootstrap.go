package wrapper

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// gitignoreContent is the .gitignore Atelier writes at bootstrap.
const gitignoreContent = `# Managed by Atelier — extend freely below.
.atelier/
.terraform/
terraform.tfstate
terraform.tfstate.backup
*.tfstate
*.tfstate.backup
`

// readmeTemplate is the README.md scaffold. Plain enough to read; the user
// is free to overwrite or extend.
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
			if !attr.Value.IsNull() && attr.Value.Type() != cty.NilType {
				body.SetAttributeValue(attr.Name, attr.Value)
			}
		}
	}
	return os.WriteFile(path, hclwrite.Format(file.Bytes()), 0o644)
}
