package importer

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/MichaelThamm/atelier/internal/state"
	"github.com/MichaelThamm/atelier/internal/tfexec"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

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
	// Safety net only: JujuModelIdentity normally does this before the plan,
	// which is the point at which it matters. Reaching here with something to
	// write means the pre-plan step could not determine the UUID (and already
	// warned), so stay quiet about the "no variable" case rather than repeating
	// the warning.
	if pctx.WrapperState.InjectModelUUID(uuid, name) == wrapper.ModelUUIDInjected {
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

// JujuModelIdentity derives the Juju model UUID (and name) from the live
// deployment and folds it into the wrapper *before* the module is planned.
//
// This is what lets `atelier import juju` work without the user having to
// hand-supply the model on the command line. The UUID is already implied by
// the run: the provider's list resources require it, so it arrives via
// --query-var, and failing that every live object's identity is prefixed with
// it. Both sources are internal to the run, so asking the user for the same
// value a second time (as a module input) is redundant.
//
// Ordering matters. Modules commonly branch on whether the model UUID is set —
// COS-Lite's `local.create_model = var.model.uuid == null` decides whether
// juju_model is a managed resource or a data source, and feeds every
// application's model_uuid. Planning with the UUID unset therefore produces
// both the wrong addresses and an unknown model_uuid, which makes Terraform
// plan a replace for every application on the next run. Running this before
// the plan avoids both.
type JujuModelIdentity struct{}

func (s *JujuModelIdentity) Name() string { return "Derive model identity" }

func (s *JujuModelIdentity) Run(_ context.Context, pctx PreflightContext) error {
	// The model the live objects actually came from is the ground truth: it is
	// what every import ID encodes and what lands in state. A requested value is
	// only a fallback for when no live identity carries a UUID (e.g. a model
	// whose only listable objects are offers, whose identity is a URL).
	//
	// QueryConfig outranks Config because the query merges them with QueryConfig
	// winning, so it is the value that actually selected the model.
	uuid := firstNonEmpty(
		jujuModelUUIDFromLive(pctx.Live),
		pctx.QueryConfig["model_uuid"],
		pctx.Config["model_uuid"],
	)
	if uuid == "" {
		return nil
	}

	// Make the UUID reachable by JujuBuildImportID even when it was only ever
	// supplied as a query variable or inferred from live identities.
	pctx.Config["model_uuid"] = uuid

	if pctx.WrapperState == nil {
		return nil
	}

	switch pctx.WrapperState.InjectModelUUID(uuid, jujuModelNameFromLive(pctx.Live)) {
	case wrapper.ModelUUIDInjected:
		if err := pctx.WrapperState.Write(); err != nil {
			return fmt.Errorf("write wrapper with model uuid: %w", err)
		}
		fmt.Fprintf(os.Stderr, "\nDerived model UUID %s from the live deployment.\n", uuid)
	case wrapper.ModelUUIDNoVariable:
		// The UUID is known but there is nowhere recognised to put it. Import
		// IDs still work (they come from each live object's identity), but the
		// plan runs with the UUID unset — and modules that branch on it will
		// then produce different resource addresses than the deployment has.
		// Say so rather than let the consequence show up as mystery diffs.
		fmt.Fprintf(os.Stderr,
			"\nwarning: model UUID %s could not be placed in the wrapper.\n"+
				"  Looked for a variable named `model` with a `uuid` field, or `model_uuid`.\n"+
				"  Planning with it unset. If this module branches on the model UUID, set it\n"+
				"  yourself (in the TUI, or --var <name>=...) and re-run; --dry-run shows the\n"+
				"  effect without touching state.\n", uuid)
	case wrapper.ModelUUIDAlreadySet:
		// Expected on a re-run or after the user set it; nothing to report.
	}
	return nil
}

// uuidPrefix matches a Juju model UUID at the start of an identity string,
// i.e. the "<model_uuid>:" prefix shared by application, integration and
// secret identities.
var uuidPrefix = regexp.MustCompile(`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})(:|$)`)

// jujuModelUUIDFromLive recovers the model UUID from the live objects'
// identities. Every model-scoped Juju resource identity is prefixed with it,
// so the most frequently seen prefix is the model being imported. Returns ""
// when no identity carries a UUID (e.g. only offers were found, whose
// identity is a URL).
func jujuModelUUIDFromLive(live []tfexec.LiveResource) string {
	counts := map[string]int{}
	for _, lr := range live {
		id, _ := lr.Identity["id"].(string)
		if m := uuidPrefix.FindStringSubmatch(id); m != nil {
			counts[m[1]]++
		}
	}
	// Deterministic winner: highest count, ties broken lexicographically.
	best, bestN := "", 0
	for uuid, n := range counts {
		if n > bestN || (n == bestN && uuid < best) {
			best, bestN = uuid, n
		}
	}
	return best
}

// jujuModelNameFromLive recovers the model name from the live objects. Offer
// identities are URLs of the form "<user>/<model>.<offer>", which is the only
// place the model name reliably appears; otherwise an application's "model"
// attribute is used when the provider exposes one.
func jujuModelNameFromLive(live []tfexec.LiveResource) string {
	for _, lr := range live {
		if lr.ResourceType != "juju_offer" {
			continue
		}
		url, _ := lr.Identity["id"].(string)
		slash := strings.Index(url, "/")
		dot := strings.LastIndex(url, ".")
		if slash >= 0 && dot > slash+1 {
			return url[slash+1 : dot]
		}
	}
	for _, lr := range live {
		if name, ok := lr.Attributes["model"].(string); ok && name != "" {
			return name
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// jujuIdentityNotImportID lists resource types whose provider-declared
// identity is *not* the string `terraform import` expects, so the identity
// must not be used verbatim as an import ID.
//
// juju_secret is the known exception: its identity is
// "<model_uuid>:<secret_id>" (e.g. "…:coj8mulh8b41e8nv6p90"), while import
// expects the secret's *name*, not its generated ID.
var jujuIdentityNotImportID = map[string]bool{
	"juju_secret": true,
}

// JujuBuildImportID constructs the provider-specific import ID for a Juju
// resource.
//
// The provider-declared resource identity is preferred whenever it is
// available: for the Juju provider it already *is* the documented import ID
// (e.g. "<model_uuid>:alertmanager" for juju_application, the offer URL for
// juju_offer, "<model_uuid>:<app1>:<ep1>:<app2>:<ep2>" for juju_integration).
// Using it means an import needs no user-supplied model UUID at all, and it
// cannot drift from the provider's own format.
//
// The name-based fallback — "<model_uuid>:<name>", or bare "<model_uuid>" for
// juju_model — is used only for resources whose identity is absent or is known
// to differ from the import ID (see jujuIdentityNotImportID).
func JujuBuildImportID(m MatchedImport, config map[string]string) string {
	if !jujuIdentityNotImportID[m.ResourceType] {
		if id, ok := m.Identity["id"].(string); ok && id != "" {
			return id
		}
	}
	modelUUID := config["model_uuid"]
	if modelUUID == "" {
		return ""
	}
	if m.ResourceType == "juju_model" {
		return modelUUID
	}
	return modelUUID + ":" + m.Name
}

// JujuModelConsistency refuses a run whose planned configuration targets a
// different Juju model than the live resources came from.
//
// model_uuid forces replacement on every Juju resource, so importing model A's
// resources under a configuration that names model B leaves the next apply
// destroying everything just imported and recreating it in B. Measured against a
// live deployment of 43 resources: "Plan: 46 to add, 0 to change, 43 to destroy".
//
// The check reads the *planned* model_uuid rather than a wrapper variable. That
// matters for coverage: without --source there is no wrapper, and the user's root
// may carry the UUID in a tfvars file, a -var, an environment variable or a
// literal. Terraform has already resolved all of those by the time it produces a
// plan, so the plan is the one place the answer is always available.
type JujuModelConsistency struct{}

func (c *JujuModelConsistency) Name() string { return "Check model consistency" }

func (c *JujuModelConsistency) Check(_ context.Context, pctx PlanCheckContext) error {
	live := firstNonEmpty(
		jujuModelUUIDFromLive(pctx.Live),
		pctx.QueryConfig["model_uuid"],
		pctx.Config["model_uuid"],
	)
	if live == "" {
		return nil
	}
	planned, addr := jujuPlannedModelUUID(pctx.Planned, live)
	if planned == "" {
		// Either nothing declares a model_uuid, or every value is unknown —
		// which is the "UUID unset" case the preflight step already warns about,
		// not a mismatch.
		return nil
	}
	source := "the planned configuration"
	if pctx.WrapperState != nil && pctx.WrapperState.ModelUUID() == planned {
		source = "the wrapper"
	}
	return fmt.Errorf(
		"model mismatch: %s targets model %s but the live resources came from model %s\n"+
			"  First seen at %s.\n"+
			"  Importing would leave state pointing at %s while the configuration says %s.\n"+
			"  Because model_uuid forces replacement, the next apply would DESTROY every\n"+
			"  imported resource and recreate it in %s.\n"+
			"  Fix by pointing both at one model: re-run with --query-var model_uuid=%s,\n"+
			"  or change the configuration to adopt %s.",
		source, planned, live, addr, live, planned, planned, planned, live)
}

// jujuPlannedModelUUID returns the first concrete planned model_uuid that
// differs from want, with the address it was found at. Unknown and empty values
// are skipped: they mean "not yet determined", not "a different model".
func jujuPlannedModelUUID(planned []PlannedResource, want string) (uuid, addr string) {
	for _, p := range planned {
		v, ok := p.PlannedAttrs["model_uuid"].(string)
		if !ok || v == "" || v == want {
			continue
		}
		return v, p.Address
	}
	return "", ""
}
