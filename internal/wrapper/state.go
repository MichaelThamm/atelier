// Package wrapper manages the Terraform wrapper Atelier writes to the user's
// current working directory: main.tf, versions.tf, providers.tf,
// .gitignore, README.md, plus the .atelier/ internal
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
	"slices"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// metaArguments are the module-block attributes Terraform interprets itself
// instead of passing to the module. A module cannot receive them as inputs, so
// they are never stale variables and must survive a change of schema.
var metaArguments = []string{"version", "count", "for_each", "providers", "depends_on"}

// IsMetaArgument reports whether name is a Terraform meta-argument — an
// attribute of a module block that Terraform interprets itself instead of
// passing to the module — rather than one of the module's inputs. A module
// cannot receive one as an argument, so it is never a stale variable.
func IsMetaArgument(name string) bool {
	return slices.Contains(metaArguments, name)
}

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

	// Providers is the list of provider blocks Atelier renders into
	// providers.tf. Populated from the chosen module's required_providers.
	Providers []ProviderBlock

	// RequiredProviders is the module's terraform { required_providers {
	// ... } } map, replicated in versions.tf so that `terraform init` in the
	// wrapper picks up the right plugin versions.
	RequiredProviders map[string]RequiredProvider

	// UnknownAttrs holds attributes inside the module {} block whose value
	// Atelier cannot represent as a cty.Value. Two kinds land here:
	//
	//   1. Attributes that aren't declared variables (count, for_each,
	//      providers, depends_on, ...).
	//   2. Declared variables the user wired to an expression Atelier can't
	//      evaluate to a constant — references (data.x.y, var.z, module.m.o),
	//      index access, function calls, interpolations, etc.
	//
	// In both cases the original source is preserved verbatim across saves
	// (ADR-0007 §10.2): writeMain re-emits the stored expression rather than
	// reconstructing the attribute from a value it doesn't have.
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
	// Raw is the whole attribute, i.e. `name = expr` bytes.
	Raw []byte
	// RawExpr is just the right-hand-side expression bytes (`expr`). The
	// writer uses this to deterministically re-emit the value via
	// SetAttributeRaw, rather than relying on incidental hclwrite passthrough.
	RawExpr []byte
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

// ClearUnknownAttr drops any preserved raw attribute for the given variable
// name. Call this when the user supplies a concrete value or resets the
// variable through the TUI, so a stale reference expression doesn't resurface
// on the next save.
func (s *State) ClearUnknownAttr(name string) {
	if len(s.UnknownAttrs) == 0 {
		return
	}
	out := s.UnknownAttrs[:0]
	for _, ra := range s.UnknownAttrs {
		if ra.Name == name {
			continue
		}
		out = append(out, ra)
	}
	s.UnknownAttrs = out
}

// WiredExpression returns the verbatim HCL expression a variable is currently
// wired to (e.g. a data-source reference such as
// `data.vault_generic_secret.s3.data["endpoint_url"]`), when Atelier preserved
// one and the user has not overridden it with a concrete value. The bool is
// false when the variable has a concrete value in Values or no preserved
// expression. Used by the TUI to surface references it can't model as values.
func (s *State) WiredExpression(name string) (string, bool) {
	if _, hasConcrete := s.Values[name]; hasConcrete {
		return "", false
	}
	for _, ra := range s.UnknownAttrs {
		if ra.Name != name {
			continue
		}
		expr := strings.TrimSpace(string(ra.RawExpr))
		if expr == "" {
			// Fall back to the whole `name = expr` form when expression-only
			// bytes are unavailable (e.g. older persisted state).
			expr = strings.TrimSpace(string(ra.Raw))
		}
		return expr, expr != ""
	}
	return "", false
}

// AdoptPrior folds a previous declaration's user input into s, keeping only
// what s's schema still declares: priorValues land in s.Values, and s's carried
// expressions become exactly the surviving priorAttrs. An input the new schema
// dropped is discarded rather than written back, because RenderMain would prune
// it as an unrecognised argument and `terraform init` would reject the block.
//
// It is the one carry-over rule for a schema change — a ref switch, or
// `atelier apply` re-pointing a block — and it is idempotent, so a caller that
// has already carried the same prior over may call it again.
//
// A meta-argument (`depends_on`, `count`, …) is not carried over, because it is
// not a module input and Atelier holds no opinion on how modules compose. It
// needs no help: writes are AST-backed on the existing main.tf and RenderMain
// keeps those names unconditionally, so a hand-written `depends_on` survives any
// rewrite untouched. Carrying it here would only mean modelling it.
func (s *State) AdoptPrior(priorValues map[string]cty.Value, priorAttrs []RawAttr) {
	s.EnsureValues()
	keep := make(map[string]bool, len(s.Vars))
	for i := range s.Vars {
		keep[s.Vars[i].Name] = true
	}
	for name, val := range priorValues {
		if keep[name] {
			s.Values[name] = val
		}
	}
	s.UnknownAttrs = nil
	for _, ra := range priorAttrs {
		if keep[ra.Name] {
			s.UnknownAttrs = append(s.UnknownAttrs, ra)
		}
	}
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

// EnsureValues initializes the Values map if nil.
func (s *State) EnsureValues() {
	if s.Values == nil {
		s.Values = map[string]cty.Value{}
	}
}
