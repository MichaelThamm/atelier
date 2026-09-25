package wrapper

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// gitignorePatterns are the ignore rules Atelier needs for its own artifacts.
// They are written as a fresh .gitignore at bootstrap, and any missing ones are
// appended to a .gitignore the user already has (see ensureGitignore).
var gitignorePatterns = []string{
	".atelier/",
	".terraform/",
	"terraform.tfstate",
	"terraform.tfstate.backup",
	"*.tfstate",
	"*.tfstate.backup",
}

// gitignoreHeader introduces Atelier's block in a .gitignore, whether the file
// is newly written or appended to.
const gitignoreHeader = "# Managed by Atelier — extend freely below."

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

	// TFVars selects the opt-in pass-through shape (ADR-0031): a mirrored
	// variables.tf plus a forwarding main.tf, with values in terraform.tfvars.
	TFVars bool

	// VariableBlocks are the verbatim `variable` block sources used to mirror
	// the module's input API into variables.tf when TFVars is set. Populated
	// from tfvars.Variable.Raw.
	VariableBlocks []string
}

// TFVar is the small interface bootstrap consumes from a tfvars.Variable —
// just enough to decide which placeholders to emit. Public so callers
// outside this package can produce []TFVar from their own types.
type TFVar interface {
	VarName() string
	VarIsRequired() bool
}

// Report describes what Bootstrap did, so the CLI can tell the user which
// files appeared in their directory and — more importantly — which writes were
// skipped because the directory already declared the same thing. A silent skip
// is how a bootstrap ends up producing a root that `terraform init` rejects.
type Report struct {
	// Created lists the base names of files Bootstrap wrote.
	Created []string
	// Notes lists human-readable explanations of skipped or partial writes.
	Notes []string
}

func (r *Report) created(name string) { r.Created = append(r.Created, name) }
func (r *Report) note(format string, args ...any) {
	r.Notes = append(r.Notes, fmt.Sprintf(format, args...))
}

// Bootstrap writes the initial wrapper files into dir. Files that already
// exist are not overwritten (SPEC §6.1: init preserves existing files
// alongside the new wrapper), and declarations the directory already makes are
// not duplicated (SPEC §6.6).
func Bootstrap(opts BootstrapOptions) (*Report, error) {
	rep := &Report{}
	if opts.Dir == "" {
		return rep, fmt.Errorf("bootstrap: Dir is required")
	}
	if opts.ModuleBlockName == "" {
		return rep, fmt.Errorf("bootstrap: ModuleBlockName is required")
	}
	if opts.Source == "" {
		return rep, fmt.Errorf("bootstrap: Source is required")
	}

	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return rep, fmt.Errorf("create wrapper dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(opts.Dir, AtelierDir), 0o755); err != nil {
		return rep, fmt.Errorf("create .atelier: %w", err)
	}

	// Read what the directory already declares before writing anything, so
	// every decision below sees the pre-bootstrap state.
	decls, err := ScanDeclarations(opts.Dir)
	if err != nil {
		return rep, fmt.Errorf("scan existing terraform files: %w", err)
	}

	if err := ensureGitignore(opts.Dir, rep); err != nil {
		return rep, err
	}
	readme := fmt.Sprintf(readmeTemplate, opts.ModuleBlockName, "```", "`")
	if err := writeIfMissing(filepath.Join(opts.Dir, ReadmeFile), []byte(readme), ReadmeFile, rep); err != nil {
		return rep, err
	}

	if err := bootstrapVersions(opts, decls, rep); err != nil {
		return rep, err
	}
	if err := bootstrapProviders(opts, decls, rep); err != nil {
		return rep, err
	}
	if err := bootstrapMain(opts, rep); err != nil {
		return rep, err
	}
	return rep, nil
}

func writeIfMissing(path string, data []byte, name string, rep *Report) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	rep.created(name)
	return nil
}

// ensureGitignore writes .gitignore when absent, and otherwise appends only the
// patterns the existing file is missing.
//
// Skipping the file entirely (the previous behaviour) left `.atelier/` and
// `.terraform/` untracked-but-unignored in any repository the user bootstrapped
// into, so Atelier's own scratch state showed up in their `git status` and,
// worse, in their next `git add .`. Appending is the least invasive fix that
// actually solves it: existing rules are untouched and ordering in a .gitignore
// carries no meaning for non-overlapping patterns.
func ensureGitignore(dir string, rep *Report) error {
	path := filepath.Join(dir, GitignoreFile)
	existing, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		var b strings.Builder
		b.WriteString(gitignoreHeader + "\n")
		for _, p := range gitignorePatterns {
			b.WriteString(p + "\n")
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			return err
		}
		rep.created(GitignoreFile)
		return nil
	}

	have := map[string]bool{}
	for _, line := range strings.Split(string(existing), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, p := range gitignorePatterns {
		if !have[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	var b strings.Builder
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n" + gitignoreHeader + "\n")
	for _, p := range missing {
		b.WriteString(p + "\n")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(b.String()); err != nil {
		return err
	}
	rep.note("appended %d pattern(s) to your existing %s: %s",
		len(missing), GitignoreFile, strings.Join(missing, " "))
	return nil
}

func bootstrapMain(opts BootstrapOptions, rep *Report) error {
	path := filepath.Join(opts.Dir, MainTF)
	if _, err := os.Stat(path); err == nil {
		// Don't overwrite a hand-edited main.tf. The init flow's caller
		// already validates that this case is the error path (SPEC §6.1).
		return nil
	}
	if opts.TFVars {
		return bootstrapTFVarsWrapper(opts, rep)
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
	if err := os.WriteFile(path, hclwrite.Format(file.Bytes()), 0o644); err != nil {
		return err
	}
	rep.created(MainTF)
	return nil
}

// bootstrapTFVarsWrapper writes the three files of the opt-in pass-through
// shape (ADR-0031): the mirrored variables.tf, the forwarding main.tf, and an
// empty terraform.tfvars header. main.tf is written here because the caller
// has already confirmed it does not exist.
func bootstrapTFVarsWrapper(opts BootstrapOptions, rep *Report) error {
	if err := os.WriteFile(
		filepath.Join(opts.Dir, VariablesTF),
		RenderVariablesTFFromBlocks(opts.VariableBlocks),
		0o644,
	); err != nil {
		return err
	}
	rep.created(VariablesTF)

	names := make([]string, 0, len(opts.Variables))
	for _, v := range opts.Variables {
		names = append(names, v.VarName())
	}
	if err := os.WriteFile(
		filepath.Join(opts.Dir, MainTF),
		RenderPassthroughMainFromNames(opts.ModuleBlockName, opts.Source, names),
		0o644,
	); err != nil {
		return err
	}
	rep.created(MainTF)

	tfVarsPath := filepath.Join(opts.Dir, TFVarsFile)
	if _, err := os.Stat(tfVarsPath); os.IsNotExist(err) {
		if err := os.WriteFile(tfVarsPath, []byte(tfVarsHeader), 0o644); err != nil {
			return err
		}
		rep.created(TFVarsFile)
	}
	return nil
}

// bootstrapVersions writes versions.tf unless the directory already declares a
// required_providers block. Terraform allows only one per module, so when one
// exists the correct action is to write nothing and tell the user which
// provider requirements they need to add themselves.
func bootstrapVersions(opts BootstrapOptions, decls Declarations, rep *Report) error {
	path := filepath.Join(opts.Dir, VersionsTF)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if len(opts.RequiredProviders) == 0 {
		return nil
	}
	if decls.HasRequiredProvidersBlock() {
		var missing []string
		for name := range opts.RequiredProviders {
			if _, declared := decls.RequiredProviders[name]; !declared {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			rep.note("%s already declares required_providers, and Terraform allows only one such block; "+
				"add the module's provider requirement(s) there yourself: %s",
				decls.RequiredProvidersFile, strings.Join(missing, ", "))
		}
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
	if err := os.WriteFile(path, hclwrite.Format(file.Bytes()), 0o644); err != nil {
		return err
	}
	rep.created(VersionsTF)
	return nil
}

// bootstrapProviders writes providers.tf, omitting any provider configuration
// the directory already declares. Two `provider "juju" {}` blocks in one module
// is a "Duplicate provider configuration" error, so a local name that is
// already configured must be left to the user's own block.
func bootstrapProviders(opts BootstrapOptions, decls Declarations, rep *Report) error {
	path := filepath.Join(opts.Dir, ProvidersTF)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if len(opts.Providers) == 0 {
		return nil
	}

	var write []ProviderBlock
	var skipped []string
	for _, p := range opts.Providers {
		if file, declared := decls.Providers[p.LocalName]; declared {
			skipped = append(skipped, fmt.Sprintf("%s (already configured in %s)", p.LocalName, file))
			continue
		}
		write = append(write, p)
	}
	if len(skipped) > 0 {
		sort.Strings(skipped)
		rep.note("kept your existing provider configuration for: %s", strings.Join(skipped, ", "))
	}
	if len(write) == 0 {
		return nil
	}

	file := hclwrite.NewEmptyFile()
	for _, p := range write {
		block := file.Body().AppendNewBlock("provider", []string{p.LocalName})
		body := block.Body()
		for _, attr := range p.Attributes {
			if !attr.Value.IsNull() && attr.Value.Type() != cty.NilType {
				body.SetAttributeValue(attr.Name, attr.Value)
			}
		}
	}
	if err := os.WriteFile(path, hclwrite.Format(file.Bytes()), 0o644); err != nil {
		return err
	}
	rep.created(ProvidersTF)
	return nil
}
