package importer

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/MichaelThamm/atelier/internal/state"
)

// JujuNullNormalization replaces null state values with non-null defaults
// for imported resources. The Juju provider stores null for empty map
// attributes (config={}, storage_directives={}), but module variable defaults
// are {}. Terraform treats null in optional(T, {}) as "use default",
// creating a diff that triggers RequiresReplace.
type JujuNullNormalization struct{}

func (s *JujuNullNormalization) Name() string {
	return "Normalize null attributes"
}

func (s *JujuNullNormalization) Run(_ context.Context, pctx PostImportContext) error {
	if pctx.WrapperState == nil {
		return nil
	}
	defaults := state.ComputeNullDefaults(pctx.WrapperState)
	if len(defaults) == 0 {
		return nil
	}
	addrs := make([]string, 0, len(pctx.Imported))
	for _, r := range pctx.Imported {
		addrs = append(addrs, r.Address)
	}
	if err := state.NormalizeNullAttributes(pctx.Dir, addrs, defaults); err != nil {
		return fmt.Errorf("normalize state: %w", err)
	}
	fmt.Fprintln(os.Stderr, "\nNormalized state to match module defaults.")
	return nil
}

// JujuSchemaVersions ensures schema_version is set for Juju resource types
// that declare a non-zero Version but don't implement UpgradeState(). Without
// this, Terraform tries to upgrade from version 0 and fails.
type JujuSchemaVersions struct{}

func (s *JujuSchemaVersions) Name() string {
	return "Ensure schema versions"
}

func (s *JujuSchemaVersions) Run(_ context.Context, pctx PostImportContext) error {
	versions := map[string]int{
		"juju_application": 1,
	}
	return state.EnsureSchemaVersions(pctx.Dir, versions)
}

// JujuModelUUIDInjection injects the model UUID into the wrapper so
// terraform plan sees a concrete model_uuid instead of "(known after apply)".
// Without this, RequiresReplace on model_uuid triggers destroy-and-recreate
// for every juju_application resource.
type JujuModelUUIDInjection struct{}

func (s *JujuModelUUIDInjection) Name() string {
	return "Inject model UUID"
}

func (s *JujuModelUUIDInjection) Run(_ context.Context, pctx PostImportContext) error {
	if pctx.WrapperState == nil {
		return nil
	}
	st, err := state.Read(pctx.Dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not read state for UUID injection: %v\n", err)
		return nil
	}
	if st == nil {
		return nil
	}
	uuid := st.ExtractModelUUID()
	if uuid == "" {
		return nil
	}
	name := st.ExtractModelName()
	if pctx.WrapperState.InjectModelUUID(uuid, name) {
		if err := pctx.WrapperState.Write(); err != nil {
			return fmt.Errorf("write wrapper with model uuid: %w", err)
		}
		fmt.Fprintf(os.Stderr, "\nInjected model UUID %s into wrapper.\n", uuid)
	}
	return nil
}

// JujuOfferDefaults normalizes null offer attributes that the Juju provider
// stores as null but whose schema declares a non-null default (e.g.
// allow_force_destroy defaults to false). Without this, Terraform sees
// null→false on every plan and plans an in-place update.
type JujuOfferDefaults struct{}

func (s *JujuOfferDefaults) Name() string {
	return "Normalize offer defaults"
}

func (s *JujuOfferDefaults) Run(_ context.Context, pctx PostImportContext) error {
	if pctx.WrapperState == nil {
		return nil
	}
	defaults := map[string]interface{}{
		"allow_force_destroy": false,
	}
	addrs := make([]string, 0)
	for _, r := range pctx.Imported {
		if strings.Contains(r.Address, ".juju_offer.") {
			addrs = append(addrs, r.Address)
		}
	}
	if len(addrs) == 0 {
		return nil
	}
	if err := state.NormalizeNullAttributes(pctx.Dir, addrs, defaults); err != nil {
		return fmt.Errorf("normalize offer defaults: %w", err)
	}
	fmt.Fprintln(os.Stderr, "\nNormalized offer defaults (allow_force_destroy).")
	return nil
}

// JujuBuildImportID constructs the provider-specific import ID for a Juju
// resource. Format: <model_uuid>:<app_name> for juju_application,
// <model_uuid> for juju_model, and the live identity "id" string for
// juju_integration (<uuid>:<app1>:<ep1>:<app2>:<ep2>) and juju_offer
// (<offer_url>).
func JujuBuildImportID(m MatchedImport, config map[string]string) string {
	modelUUID := config["model_uuid"]
	switch m.ResourceType {
	case "juju_model":
		if modelUUID == "" {
			return ""
		}
		return modelUUID
	case "juju_integration":
		id, _ := m.Identity["id"].(string)
		return id
	case "juju_offer":
		id, _ := m.Identity["id"].(string)
		return id
	default:
		if modelUUID == "" {
			return ""
		}
		return modelUUID + ":" + m.Name
	}
}
