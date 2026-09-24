// Package providers is the extension point for `atelier import`: the
// registry of Terraform providers Atelier knows how to import from. The
// importer pipeline itself is provider-agnostic; every provider-specific
// behaviour — preflight steps, plan checks, post-import normalization,
// import-ID construction — lives behind the Provider interface in this
// package, so adding a provider never touches the core.
package providers

import (
	"github.com/MichaelThamm/atelier/internal/importer"
	"github.com/MichaelThamm/atelier/internal/importer/providers/juju"
)

// Provider is the contract a Terraform provider must implement to be
// importable via `atelier import [name]`. Each Provider packages the
// knowledge that is specific to one Terraform provider; the importer core
// invokes the returned steps at the fixed points of its pipeline.
type Provider interface {
	// Name is the short name users pass as the PROVIDER argument, e.g.
	// "juju", and the name reported in diagnostics.
	Name() string

	// Detect reports whether this provider's import support applies to the
	// detected set: the PROVIDER argument plus the provider source addresses
	// declared in the target directory (e.g. "juju/juju"). The first
	// registered provider whose Detect matches is selected for the run.
	Detect(detected []string) bool

	// PreflightSteps run after the live query but before `terraform plan` —
	// the only window in which a step can still influence the plan (e.g. by
	// folding a value derived from the live deployment into the wrapper).
	PreflightSteps() []importer.PreflightStep

	// PlanChecks run after the module is planned and before anything is
	// imported. They are how a run is refused because its plan contradicts
	// the live deployment.
	PlanChecks() []importer.PlanCheck

	// PostImportSteps normalize the wrapper or state after `terraform
	// import` completes, so the user's first plan/apply is clean.
	PostImportSteps() []importer.PostImportStep

	// BuildImportID constructs the provider-specific import ID for a matched
	// resource. An ID that cannot be built returns "" and the match is
	// reported unresolved rather than imported.
	BuildImportID() importer.ImportIDFunc
}

// All returns every registered import provider, in registration order.
func All() []Provider {
	return []Provider{&juju.Provider{}}
}

// For returns the first registered provider whose Detect matches the
// detected sources, or nil when none does.
func For(detected []string) Provider {
	for _, p := range All() {
		if p.Detect(detected) {
			return p
		}
	}
	return nil
}
