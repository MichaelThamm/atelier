// Package juju implements the import provider for the Juju Terraform
// provider (source "juju/juju"). It is the canonical example of the
// provider contract in internal/importer/providers: everything Juju knows
// about turning a live deployment into Terraform state lives here.
package juju

import (
	"strings"

	"github.com/MichaelThamm/atelier/internal/importer"
)

// Provider is Atelier's import support for the Juju provider.
type Provider struct{}

// Name is the short name users pass as the PROVIDER argument: `atelier
// import juju`.
func (*Provider) Name() string { return "juju" }

// Detect reports whether the Juju provider's import support applies. It
// matches the provider's source address ("juju/juju") wherever it appears
// in the detected set.
func (*Provider) Detect(detected []string) bool {
	for _, s := range detected {
		if strings.Contains(s, "juju") {
			return true
		}
	}
	return false
}

// PreflightSteps derives the model identity before the plan so modules that
// branch on the model UUID produce the right resource addresses.
func (*Provider) PreflightSteps() []importer.PreflightStep {
	return []importer.PreflightStep{&ModelIdentity{}}
}

// PlanChecks refuses a run whose planned configuration targets a different
// model than the live resources came from.
func (*Provider) PlanChecks() []importer.PlanCheck {
	return []importer.PlanCheck{&ModelConsistency{}}
}

// PostImportSteps normalize state and wrapper after import so the first
// plan/apply is clean.
func (*Provider) PostImportSteps() []importer.PostImportStep {
	return []importer.PostImportStep{
		// Generic: null-versus-empty is a provider-SDK quirk, not a Juju one.
		&importer.NullEmptyNormalization{},
		&SchemaVersions{},
		&OfferDefaults{},
		&ModelUUIDInjection{},
	}
}

// BuildImportID constructs the import ID from each matched resource's live
// identity (e.g. "<model_uuid>:alertmanager" for juju_application).
func (*Provider) BuildImportID() importer.ImportIDFunc { return BuildImportID }
