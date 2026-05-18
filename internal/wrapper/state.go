// Package wrapper manages the Terraform wrapper Atelier writes to the user's
// current working directory: main.tf, versions.tf, providers.tf,
// secrets.auto.tfvars, .gitignore, README.md, plus the .atelier/ internal
// state directory.
//
// The package's responsibilities split into three areas:
//
//   - State (this file) — the in-memory model of a wrapper.
//   - Sparse-write rule (sparse.go) — the ADR-0007 rule that decides which
//     variable values appear in main.tf.
//   - File IO (write.go, read.go, bootstrap.go) — turning State to/from disk.
package wrapper

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// State is Atelier's in-memory model of the wrapper. It does not embed file
// contents (that lives in main.tf etc.); instead it holds the values Atelier
// understands, plus pointers back to the parsed HCL for round-trip
// preservation.
type State struct {
	// Dir is the wrapper directory (the user's CWD).
	Dir string

	// ModuleBlockName is the HCL block name, e.g. `module "cos_lite"` →
	// "cos_lite". Defaults to a sanitised version of the candidate directory
	// basename.
	ModuleBlockName string

	// Source is the literal value of the wrapper's `source =` attribute,
	// including any `?ref=...` suffix.
	Source string

	// Vars is the module's declared variables, in declaration order. Atelier
	// reads this directly from the cloned module's variables.tf.
	Vars []tfvars.Variable

	// Values holds the user's current values, keyed by variable name. Missing
	// entries mean "use the declared default" (or "unset" for required vars).
	Values map[string]cty.Value

	// SecretValues holds values for sensitive wrapper-declared variables —
	// the ones that back sensitive provider attributes via the variable
	// indirection described in SPEC §12.1.
	SecretValues map[string]cty.Value

	// Providers is the list of provider blocks Atelier renders into
	// providers.tf. Populated from the chosen module's required_providers.
	Providers []ProviderBlock

	// RequiredProviders is the module's terraform { required_providers {
	// ... } } map, replicated in versions.tf so that `terraform init` in the
	// wrapper picks up the right plugin versions.
	RequiredProviders map[string]RequiredProvider

	// OutputNames lists the module's declared output names (sorted
	// alphabetically). Atelier forwards these in the wrapper's outputs.tf.
	OutputNames []string

	// UnknownAttrs holds any attributes inside the module {} block Atelier
	// didn't recognise (count, for_each, providers, depends_on, etc.). They
	// are preserved verbatim across saves (ADR-0007 §10.2).
	UnknownAttrs []RawAttr
}

// ProviderBlock describes one `provider "X" {}` block in providers.tf.
type ProviderBlock struct {
	Name       string
	LocalName  string // Often equal to Name; differs only for aliased declarations (out of scope v1).
	Attributes []ProviderAttr
}

// ProviderAttr describes one attribute inside a provider block.
type ProviderAttr struct {
	Name      string
	Sensitive bool
	Required  bool
	Value     cty.Value // The current value (may be NilVal if unset).
	// VariableRef, when non-empty, indicates the attribute is rendered as
	// `<name> = var.<VariableRef>` rather than an inline value. Used for
	// sensitive attributes (ADR-0009).
	VariableRef string
}

// RequiredProvider mirrors a Terraform required_providers entry.
type RequiredProvider struct {
	Source  string
	Version string
}

// RawAttr is a verbatim copy of an attribute Atelier doesn't manage. It is
// stored as the formatted bytes of the original source so the writer can
// re-emit it unchanged.
type RawAttr struct {
	Name string
	Raw  []byte
}

// VariableValue returns the current value for a variable, falling back to
// the declared default if Atelier has no override for it.
func (s *State) VariableValue(name string) (cty.Value, bool) {
	if v, ok := s.Values[name]; ok {
		return v, true
	}
	for _, decl := range s.Vars {
		if decl.Name == name {
			if decl.HasDefault {
				return decl.Default, true
			}
			return cty.NilVal, false
		}
	}
	return cty.NilVal, false
}

// FindVar returns the declaration for a variable name, or nil if not found.
func (s *State) FindVar(name string) *tfvars.Variable {
	for i := range s.Vars {
		if s.Vars[i].Name == name {
			return &s.Vars[i]
		}
	}
	return nil
}
