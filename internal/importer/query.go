// Package importer implements `atelier import`: assisting the native
// `terraform query` bulk-import flow so a running deployment can be pulled
// under Terraform management.
//
// The design is deliberately provider-agnostic (ADR-0027): the set of
// importable resource types and their per-type config arguments come entirely
// from the provider's own schema (`terraform providers schema -json`, exposed
// as list_resource_schemas), never from a hardcoded list of resource names.
//
// This package covers the mechanical, schema-derived work: discover which
// list resources a provider offers, generate a `*.tfquery.hcl` for the
// selected types, and drive `terraform query -generate-config-out`. It does
// NOT cross-reference the generated resources or prune provider defaults —
// those require provider-specific knowledge and are left to the user
// (ADR-0028).
package importer

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"
)

// ListResource describes a single importable list resource discovered from a
// provider's schema.
type ListResource struct {
	// Type is the resource type, e.g. "juju_application".
	Type string
	// ProviderKey is the schema map key (the provider source address), e.g.
	// "registry.terraform.io/juju/juju".
	ProviderKey string
	// ProviderLocal is the provider's local name used in the `provider = <x>`
	// argument of a list block, e.g. "juju".
	ProviderLocal string
	// ConfigAttrs are the names of the config arguments the list block accepts
	// (e.g. "model_uuid"), sorted. Empty if the schema does not describe them.
	ConfigAttrs []ConfigAttr
}

// ConfigAttr is a single config argument a list resource accepts.
type ConfigAttr struct {
	Name     string
	Required bool
}

// HasConfigAttr reports whether the list resource declares a config argument
// with the given name.
func (l ListResource) HasConfigAttr(name string) bool {
	for _, a := range l.ConfigAttrs {
		if a.Name == name {
			return true
		}
	}
	return false
}

// DiscoverListResources enumerates every list resource across all providers in
// the schema, sorted by type. This is the provider-agnostic heart of import:
// whatever a provider declares as a list resource is what Atelier offers.
func DiscoverListResources(schemas *tfjson.ProviderSchemas) []ListResource {
	if schemas == nil {
		return nil
	}
	var out []ListResource
	for provKey, ps := range schemas.Schemas {
		if ps == nil {
			continue
		}
		for typ, sch := range ps.ListResourceSchemas {
			lr := ListResource{
				Type:          typ,
				ProviderKey:   provKey,
				ProviderLocal: localProviderName(provKey),
			}
			if sch != nil && sch.Block != nil {
				for name, a := range sch.Block.Attributes {
					required := a != nil && a.Required
					lr.ConfigAttrs = append(lr.ConfigAttrs, ConfigAttr{Name: name, Required: required})
				}
				sort.Slice(lr.ConfigAttrs, func(i, j int) bool {
					return lr.ConfigAttrs[i].Name < lr.ConfigAttrs[j].Name
				})
			}
			out = append(out, lr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// localProviderName derives a provider's local name from its source address,
// e.g. "registry.terraform.io/juju/juju" -> "juju". This matches Terraform's
// default local-name convention (the last path segment); aliased or renamed
// providers are out of scope for v1 (SPEC §2).
func localProviderName(providerKey string) string {
	parts := strings.Split(providerKey, "/")
	return parts[len(parts)-1]
}

// SelectByType returns the subset of resources whose type is in `types`,
// along with the list of requested types that were not found. Passing an
// empty `types` selects everything (with no missing).
func SelectByType(all []ListResource, types []string) (selected []ListResource, missing []string) {
	if len(types) == 0 {
		return all, nil
	}
	byType := make(map[string]ListResource, len(all))
	for _, lr := range all {
		byType[lr.Type] = lr
	}
	for _, t := range types {
		if lr, ok := byType[t]; ok {
			selected = append(selected, lr)
		} else {
			missing = append(missing, t)
		}
	}
	return selected, missing
}

// RenderQueryFile produces the contents of a `*.tfquery.hcl` file that lists
// the selected resources. For each config key in `config`, a top-level
// `variable "<key>"` is declared and referenced (`<key> = var.<key>`) from
// every selected list block that accepts it — this threads a shared value
// (e.g. model_uuid) to all blocks, mirroring the upstream Juju export guide.
//
// The value the user supplies for each key is passed at query time via -var
// (see the engine), not embedded here, so the query file is reusable.
//
// existingVars names variables already declared in the root module (from *.tf
// files); those declarations are skipped to avoid duplicates.
func RenderQueryFile(selected []ListResource, config map[string]string, existingVars ...string) []byte {
	skip := make(map[string]bool, len(existingVars))
	for _, v := range existingVars {
		skip[v] = true
	}

	f := hclwrite.NewEmptyFile()
	body := f.Body()

	// Declare one variable per shared config key, in sorted order.
	// Skip keys that already exist in the root module.
	keys := sortedKeys(config)
	declared := 0
	for _, k := range keys {
		if skip[k] {
			continue
		}
		vb := body.AppendNewBlock("variable", []string{k})
		vb.Body().SetAttributeRaw("type", stringTypeToken())
		declared++
	}
	if declared > 0 {
		body.AppendNewline()
	}

	for i, lr := range selected {
		blk := body.AppendNewBlock("list", []string{lr.Type, lr.Type})
		bb := blk.Body()
		bb.SetAttributeRaw("provider", tokensTraversal(lr.ProviderLocal))
		bb.SetAttributeValue("include_resource", cty.True)

		// Only thread config keys the resource actually accepts. When the
		// schema does not describe any config attrs, fall back to emitting all
		// provided keys (robustness: some providers may not surface list
		// config in the schema, and the upstream flow still expects them).
		emit := configKeysFor(lr, keys)
		if len(emit) > 0 {
			cfg := bb.AppendNewBlock("config", nil)
			for _, k := range emit {
				cfg.Body().SetAttributeRaw(k, tokensTraversal("var", k))
			}
		}
		if i < len(selected)-1 {
			body.AppendNewline()
		}
	}
	return f.Bytes()
}

// configKeysFor returns which of the provided keys should be emitted in a
// resource's config block.
func configKeysFor(lr ListResource, keys []string) []string {
	if len(lr.ConfigAttrs) == 0 {
		return keys // schema silent on config; emit what the user provided
	}
	var out []string
	for _, k := range keys {
		if lr.HasConfigAttr(k) {
			out = append(out, k)
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// tokensTraversal renders a dotted traversal like `juju` or `var.model_uuid`
// as raw HCL tokens (an unquoted expression rather than a string literal).
func tokensTraversal(root string, attrs ...string) hclwrite.Tokens {
	tr := hcl.Traversal{hcl.TraverseRoot{Name: root}}
	for _, a := range attrs {
		tr = append(tr, hcl.TraverseAttr{Name: a})
	}
	return hclwrite.TokensForTraversal(tr)
}

// stringTypeToken renders the bare `string` type keyword.
func stringTypeToken() hclwrite.Tokens {
	return hclwrite.TokensForTraversal(hcl.Traversal{hcl.TraverseRoot{Name: "string"}})
}

// ExistingVars scans all *.tf files in dir (non-recursive) and returns the
// names of declared top-level variable blocks. Used by Generate to avoid
// re-declaring variables already present in the root module.
func ExistingVars(dir string) []string {
	glob, err := filepath.Glob(filepath.Join(dir, "*.tf"))
	if err != nil || len(glob) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, path := range glob {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		f, diags := hclparse.NewParser().ParseHCL(data, filepath.Base(path))
		if diags.HasErrors() {
			continue
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		for _, b := range body.Blocks {
			if b.Type == "variable" && len(b.Labels) > 0 {
				name := b.Labels[0]
				if !seen[name] {
					seen[name] = true
					out = append(out, name)
				}
			}
		}
	}
	return out
}
