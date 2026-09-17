package wrapper

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// Declarations summarises the Terraform declarations already present in a
// directory's top-level *.tf files.
//
// Bootstrap needs this because its collision safety used to be keyed on
// filename alone: it skipped writing versions.tf if a file called versions.tf
// existed, and wrote one otherwise. A directory that declares
// `required_providers` inside main.tf or terraform.tf therefore received a
// *second* declaration, which Terraform rejects outright ("Duplicate required
// providers configuration") — turning a helpful bootstrap into a broken root.
// The same applies to `provider` blocks and "Duplicate provider
// configuration". Deciding by content instead of filename is the only way to
// get this right.
//
// The scan is deliberately non-recursive and ignores parse errors in
// individual files: it informs a safety decision, so an unreadable file is
// treated as declaring nothing rather than aborting the operation.
type Declarations struct {
	// RequiredProviders maps a provider local name to the file declaring it.
	RequiredProviders map[string]string
	// RequiredProvidersFile names a file containing a `terraform {
	// required_providers {} }` block, or "" if none does. Terraform permits
	// only one such block per module, so its mere existence — not just the
	// entries within it — blocks writing another.
	RequiredProvidersFile string
	// Providers maps a provider configuration's local name to the file
	// declaring it. Aliased configurations are keyed "name.alias".
	Providers map[string]string
	// ModuleBlocks maps a module block's label to the file declaring it.
	ModuleBlocks map[string]string
	// ModuleSources maps a module block's label to its literal source string
	// ("" when the source is absent or not a literal).
	ModuleSources map[string]string
	// OtherBlockTypes lists block types present that Atelier never authors
	// (resource, data, locals, output, variable, ...), sorted and deduplicated.
	// A non-empty list is strong evidence the directory is a hand-authored
	// Terraform root rather than an Atelier wrapper.
	OtherBlockTypes []string
	// Files lists the top-level *.tf files scanned, sorted.
	Files []string
}

// atelierAuthoredBlockTypes are the block types Atelier itself writes into a
// wrapper. Anything else in a .tf file was put there by the user.
var atelierAuthoredBlockTypes = map[string]bool{
	"module":    true,
	"terraform": true,
	"provider":  true,
}

// ScanDeclarations parses the top-level *.tf files in dir and reports what is
// already declared. A missing directory yields an empty result and no error,
// so callers can treat "nothing there" and "nothing declared" alike.
func ScanDeclarations(dir string) (Declarations, error) {
	d := Declarations{
		RequiredProviders: map[string]string{},
		Providers:         map[string]string{},
		ModuleBlocks:      map[string]string{},
		ModuleSources:     map[string]string{},
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return d, nil
		}
		return d, err
	}

	others := map[string]bool{}
	parser := hclparse.NewParser()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		name := e.Name()
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		d.Files = append(d.Files, name)
		f, diags := parser.ParseHCL(data, name)
		if diags.HasErrors() {
			continue
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		for _, block := range body.Blocks {
			switch block.Type {
			case "terraform":
				for _, sub := range block.Body.Blocks {
					if sub.Type != "required_providers" {
						continue
					}
					if d.RequiredProvidersFile == "" {
						d.RequiredProvidersFile = name
					}
					for local := range sub.Body.Attributes {
						if _, seen := d.RequiredProviders[local]; !seen {
							d.RequiredProviders[local] = name
						}
					}
				}
			case "provider":
				if len(block.Labels) != 1 {
					continue
				}
				key := block.Labels[0]
				if alias, ok := literalString(block.Body, "alias"); ok && alias != "" {
					key += "." + alias
				}
				if _, seen := d.Providers[key]; !seen {
					d.Providers[key] = name
				}
			case "module":
				if len(block.Labels) != 1 {
					continue
				}
				label := block.Labels[0]
				if _, seen := d.ModuleBlocks[label]; !seen {
					d.ModuleBlocks[label] = name
					src, _ := literalString(block.Body, "source")
					d.ModuleSources[label] = src
				}
			}
			if !atelierAuthoredBlockTypes[block.Type] {
				others[block.Type] = true
			}
		}
	}

	for t := range others {
		d.OtherBlockTypes = append(d.OtherBlockTypes, t)
	}
	sort.Strings(d.OtherBlockTypes)
	sort.Strings(d.Files)
	return d, nil
}

// literalString reads a string-literal attribute from a block body. It returns
// ok=false for absent attributes and for expressions that need evaluation
// context (references, interpolations), which callers treat as "unknown".
func literalString(body *hclsyntax.Body, name string) (string, bool) {
	attr, ok := body.Attributes[name]
	if !ok {
		return "", false
	}
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() || val.IsNull() || val.Type() != cty.String {
		return "", false
	}
	return val.AsString(), true
}

// HasRequiredProvidersBlock reports whether dir already declares a
// `terraform { required_providers {} }` block anywhere in its top-level .tf
// files.
func (d Declarations) HasRequiredProvidersBlock() bool {
	return d.RequiredProvidersFile != ""
}

// LooksHandAuthored reports whether the directory's Terraform looks written by
// hand rather than by Atelier.
//
// Atelier only ever authors `module` blocks with a remote source, a
// `terraform` block, and `provider` blocks. A root containing resources, data
// sources, locals, outputs, or a module pointed at a local path is therefore
// someone's own project — and appending to its main.tf deserves a
// confirmation, not silence.
func (d Declarations) LooksHandAuthored() bool {
	if len(d.OtherBlockTypes) > 0 {
		return true
	}
	for _, src := range d.ModuleSources {
		if src == "" {
			continue
		}
		if strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") || strings.HasPrefix(src, "/") {
			return true
		}
	}
	return false
}
