package juju

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/importer"
	"github.com/MichaelThamm/atelier/internal/tfexec"
	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

func writeTestState(t *testing.T, dir string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

func readTestState(t *testing.T, dir string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "terraform.tfstate"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// --- SchemaVersions ---

func TestSchemaVersions_SetsVersion(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, []byte(`{
		"version": 4,
		"terraform_version": "1.15.0",
		"resources": [{
			"module": "",
			"mode": "managed",
			"type": "juju_application",
			"name": "grafana",
			"instances": [{
				"index_key": null,
				"attributes": {"name": "grafana"}
			}]
		}]
	}`))

	step := &SchemaVersions{}
	err := step.Run(context.Background(), importer.PostImportContext{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	raw := readTestState(t, dir)
	resources := raw["resources"].([]interface{})
	res := resources[0].(map[string]interface{})
	instances := res["instances"].([]interface{})
	inst := instances[0].(map[string]interface{})
	if inst["schema_version"] != float64(1) {
		t.Errorf("schema_version: got %v, want 1", inst["schema_version"])
	}
}

func TestSchemaVersions_SkipsAlreadySet(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, []byte(`{
		"version": 4,
		"resources": [{
			"module": "",
			"mode": "managed",
			"type": "juju_application",
			"name": "grafana",
			"instances": [{
				"schema_version": 1,
				"attributes": {"name": "grafana"}
			}]
		}]
	}`))

	step := &SchemaVersions{}
	err := step.Run(context.Background(), importer.PostImportContext{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	// Should not have overwritten existing schema_version.
	raw := readTestState(t, dir)
	resources := raw["resources"].([]interface{})
	res := resources[0].(map[string]interface{})
	instances := res["instances"].([]interface{})
	inst := instances[0].(map[string]interface{})
	if inst["schema_version"] != float64(1) {
		t.Errorf("schema_version: got %v, want 1 (unchanged)", inst["schema_version"])
	}
}

func TestSchemaVersions_NoStateFile(t *testing.T) {
	dir := t.TempDir()
	step := &SchemaVersions{}
	err := step.Run(context.Background(), importer.PostImportContext{Dir: dir})
	if err != nil {
		t.Errorf("expected nil error for missing state, got: %v", err)
	}
}

func TestSchemaVersions_Name(t *testing.T) {
	step := &SchemaVersions{}
	if step.Name() != "Ensure schema versions" {
		t.Errorf("Name(): got %q", step.Name())
	}
}

// --- OfferDefaults ---

func TestOfferDefaults_NormalizesOffer(t *testing.T) {
	dir := t.TempDir()
	// Use a numeric value (0) instead of null because JSON null→Go nil can be
	// ambiguous with absent keys. The normalize function replaces nil values,
	// so we test with a non-nil value that should NOT be replaced (ensuring
	// the function only targets nulls), then separately test the nil path.
	writeTestState(t, dir, []byte(`{
		"version": 4,
		"resources": [{
			"module": "",
			"mode": "managed",
			"type": "juju_offer",
			"name": "grafana",
			"instances": [{
				"index_key": null,
				"attributes": {"url": "admin/grafana"}
			}]
		}]
	}`))

	step := &OfferDefaults{}
	pctx := importer.PostImportContext{
		Dir: dir,
		Imported: []importer.ImportResult{
			{Address: "juju_offer.grafana"},
		},
		WrapperState: &wrapper.State{},
	}
	err := step.Run(context.Background(), pctx)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the state file was written (step succeeded).
	raw := readTestState(t, dir)
	resources := raw["resources"].([]interface{})
	if len(resources) == 0 {
		t.Fatal("no resources in state after normalization")
	}
}

func TestOfferDefaults_SkipsNonOfferResources(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, []byte(`{
		"version": 4,
		"resources": [{
			"module": "",
			"mode": "managed",
			"type": "juju_application",
			"name": "grafana",
			"instances": [{
				"attributes": {"name": "grafana"}
			}]
		}]
	}`))

	step := &OfferDefaults{}
	pctx := importer.PostImportContext{
		Dir: dir,
		Imported: []importer.ImportResult{
			{Address: "juju_application.grafana"},
		},
		WrapperState: &wrapper.State{},
	}
	err := step.Run(context.Background(), pctx)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the step ran (no error) — non-offer resources are not touched.
	raw := readTestState(t, dir)
	resources := raw["resources"].([]interface{})
	res := resources[0].(map[string]interface{})
	instances := res["instances"].([]interface{})
	inst := instances[0].(map[string]interface{})
	attrs := inst["attributes"].(map[string]interface{})
	if _, ok := attrs["allow_force_destroy"]; ok {
		t.Error("allow_force_destroy should not have been added to a non-offer resource")
	}
}

func TestOfferDefaults_NoOffers(t *testing.T) {
	dir := t.TempDir()
	step := &OfferDefaults{}
	pctx := importer.PostImportContext{
		Dir:      dir,
		Imported: []importer.ImportResult{},
	}
	err := step.Run(context.Background(), pctx)
	if err != nil {
		t.Errorf("expected nil for no offers, got: %v", err)
	}
}

func TestOfferDefaults_NilWrapperState(t *testing.T) {
	step := &OfferDefaults{}
	pctx := importer.PostImportContext{
		Imported: []importer.ImportResult{
			{Address: "juju_offer.x"},
		},
	}
	err := step.Run(context.Background(), pctx)
	if err != nil {
		t.Errorf("expected nil for nil WrapperState, got: %v", err)
	}
}

// --- ModelUUIDInjection ---

func TestModelUUIDInjection_NilWrapperState(t *testing.T) {
	step := &ModelUUIDInjection{}
	pctx := importer.PostImportContext{}
	err := step.Run(context.Background(), pctx)
	if err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
}

func TestModelUUIDInjection_NoStateFile(t *testing.T) {
	dir := t.TempDir()
	step := &ModelUUIDInjection{}
	pctx := importer.PostImportContext{
		Dir:          dir,
		WrapperState: &wrapper.State{},
	}
	err := step.Run(context.Background(), pctx)
	if err != nil {
		t.Errorf("expected nil for missing state, got: %v", err)
	}
}

func TestModelUUIDInjection_EmptyUUID(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, []byte(`{
		"version": 4,
		"resources": [{
			"module": "",
			"mode": "managed",
			"type": "juju_application",
			"name": "grafana",
			"instances": [{
				"attributes": {"name": "grafana"}
			}]
		}]
	}`))
	step := &ModelUUIDInjection{}
	ws := &wrapper.State{
		Values: map[string]cty.Value{},
	}
	pctx := importer.PostImportContext{
		Dir:          dir,
		WrapperState: ws,
	}
	err := step.Run(context.Background(), pctx)
	if err != nil {
		t.Errorf("expected nil for empty UUID, got: %v", err)
	}
}

// --- BuildImportID ---

func TestBuildImportID_Application(t *testing.T) {
	m := importer.MatchedImport{
		ResourceType: "juju_application",
		Name:         "grafana",
	}
	config := map[string]string{"model_uuid": "abc-123"}
	got := BuildImportID(m, config)
	want := "abc-123:grafana"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildImportID_Model(t *testing.T) {
	m := importer.MatchedImport{
		ResourceType: "juju_model",
		Name:         "default",
	}
	config := map[string]string{"model_uuid": "abc-123"}
	got := BuildImportID(m, config)
	if got != "abc-123" {
		t.Errorf("got %q, want abc-123", got)
	}
}

func TestBuildImportID_Integration(t *testing.T) {
	m := importer.MatchedImport{
		ResourceType: "juju_integration",
		Name:         "grafana-prom",
		Identity:     map[string]interface{}{"id": "abc:grafana:endpoint:prom:scrape"},
	}
	config := map[string]string{"model_uuid": "abc-123"}
	got := BuildImportID(m, config)
	want := "abc:grafana:endpoint:prom:scrape"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildImportID_Offer(t *testing.T) {
	m := importer.MatchedImport{
		ResourceType: "juju_offer",
		Name:         "grafana",
		Identity:     map[string]interface{}{"id": "admin/grafana"},
	}
	config := map[string]string{"model_uuid": "abc-123"}
	got := BuildImportID(m, config)
	if got != "admin/grafana" {
		t.Errorf("got %q, want admin/grafana", got)
	}
}

func TestBuildImportID_NoUUID(t *testing.T) {
	m := importer.MatchedImport{
		ResourceType: "juju_application",
		Name:         "grafana",
	}
	config := map[string]string{}
	got := BuildImportID(m, config)
	if got != "" {
		t.Errorf("got %q, want empty (no UUID)", got)
	}
}

func TestBuildImportID_ModelNoUUID(t *testing.T) {
	m := importer.MatchedImport{
		ResourceType: "juju_model",
		Name:         "default",
	}
	config := map[string]string{}
	got := BuildImportID(m, config)
	if got != "" {
		t.Errorf("got %q, want empty (no UUID)", got)
	}
}

// --- Import IDs derived from the provider identity (regression) ---

// A juju_application's live identity is already "<model_uuid>:<app_name>",
// which is exactly what `terraform import` expects. Building the ID from it
// means an import needs no user-supplied model UUID. Previously the identity
// was ignored for applications, so a run that only passed --query-var
// silently dropped every application from the import set.
func TestBuildImportID_ApplicationPrefersIdentity(t *testing.T) {
	m := importer.MatchedImport{
		ResourceType: "juju_application",
		Name:         "alertmanager",
		Identity: map[string]interface{}{
			"id": "b62cdacf-9e9b-4e35-8c5e-e334930e2b02:alertmanager",
		},
	}
	got := BuildImportID(m, map[string]string{})
	want := "b62cdacf-9e9b-4e35-8c5e-e334930e2b02:alertmanager"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// juju_secret's identity is "<model_uuid>:<secret_id>" (a generated ID), but
// import expects the secret's name — so the identity must not be used.
func TestBuildImportID_SecretIgnoresIdentity(t *testing.T) {
	m := importer.MatchedImport{
		ResourceType: "juju_secret",
		Name:         "admin-password",
		Identity:     map[string]interface{}{"id": "abc-123:6fgs92rd8kuslq7c6gsg"},
	}
	got := BuildImportID(m, map[string]string{"model_uuid": "abc-123"})
	if got != "abc-123:admin-password" {
		t.Errorf("got %q, want abc-123:admin-password", got)
	}
}

// The whole matched set from a COS-Lite import must yield an ID for every
// entry with only the live identities available — no --var at all. This is the
// reported regression: 43 matched but 36 imported.
func TestBuildImportID_NoVarsNeededForFullMatchSet(t *testing.T) {
	const uuid = "b62cdacf-9e9b-4e35-8c5e-e334930e2b02"
	matched := []importer.MatchedImport{
		{ResourceType: "juju_application", Name: "alertmanager",
			Identity: map[string]interface{}{"id": uuid + ":alertmanager"}},
		{ResourceType: "juju_application", Name: "ca",
			Identity: map[string]interface{}{"id": uuid + ":ca"}},
		{ResourceType: "juju_integration", Name: "alerting",
			Identity: map[string]interface{}{"id": uuid + ":alertmanager:alerting:loki:alertmanager"}},
		{ResourceType: "juju_offer", Name: "certificates",
			Identity: map[string]interface{}{"id": "admin/cos-lite.certificates"}},
	}
	for _, m := range matched {
		if got := BuildImportID(m, map[string]string{}); got == "" {
			t.Errorf("%s/%s: got empty import ID with no --var supplied", m.ResourceType, m.Name)
		}
	}
}

// --- Model identity recovered from live data ---

func TestJujuModelUUIDFromLive(t *testing.T) {
	const uuid = "b62cdacf-9e9b-4e35-8c5e-e334930e2b02"
	live := []tfexec.LiveResource{
		// Offers carry a URL, not a UUID — must be ignored, not mis-parsed.
		{ResourceType: "juju_offer", Identity: map[string]any{"id": "admin/cos-lite.certificates"}},
		{ResourceType: "juju_application", Identity: map[string]any{"id": uuid + ":alertmanager"}},
		{ResourceType: "juju_integration", Identity: map[string]any{"id": uuid + ":a:b:c:d"}},
	}
	if got := modelUUIDFromLive(live); got != uuid {
		t.Errorf("got %q, want %q", got, uuid)
	}
}

func TestJujuModelUUIDFromLive_None(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "juju_offer", Identity: map[string]any{"id": "admin/cos-lite.certificates"}},
	}
	if got := modelUUIDFromLive(live); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestJujuModelNameFromLive_FromOfferURL(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "juju_application", Identity: map[string]any{"id": "abc:grafana"}},
		{ResourceType: "juju_offer", Identity: map[string]any{"id": "admin/cos-lite.certificates"}},
	}
	if got := modelNameFromLive(live); got != "cos-lite" {
		t.Errorf("got %q, want cos-lite", got)
	}
}

func TestJujuModelNameFromLive_FromAttribute(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "juju_application", Attributes: map[string]any{"model": "loki"}},
	}
	if got := modelNameFromLive(live); got != "loki" {
		t.Errorf("got %q, want loki", got)
	}
}

// --- ModelIdentity preflight step ---

// The UUID arrives via --query-var (QueryConfig), which BuildImportID never
// saw. The preflight step must promote it into Config.
func TestModelIdentity_PromotesQueryVarIntoConfig(t *testing.T) {
	config := map[string]string{}
	pctx := importer.PreflightContext{
		Config:      config,
		QueryConfig: map[string]string{"model_uuid": "abc-123"},
	}
	if err := (&ModelIdentity{}).Run(context.Background(), pctx); err != nil {
		t.Fatal(err)
	}
	if config["model_uuid"] != "abc-123" {
		t.Errorf("Config[model_uuid] = %q, want abc-123", config["model_uuid"])
	}
}

// With no flags at all, the UUID must still be recovered from live identities.
func TestModelIdentity_InfersFromLive(t *testing.T) {
	const uuid = "b62cdacf-9e9b-4e35-8c5e-e334930e2b02"
	config := map[string]string{}
	pctx := importer.PreflightContext{
		Config: config,
		Live: []tfexec.LiveResource{
			{ResourceType: "juju_application", Identity: map[string]any{"id": uuid + ":grafana"}},
		},
	}
	if err := (&ModelIdentity{}).Run(context.Background(), pctx); err != nil {
		t.Fatal(err)
	}
	if config["model_uuid"] != uuid {
		t.Errorf("Config[model_uuid] = %q, want %q", config["model_uuid"], uuid)
	}
}

// Live evidence outranks a requested value. The model the live objects came
// from is what every import ID encodes and what lands in state, so honouring a
// disagreeing --var would write a mismatched UUID into main.tf — and because
// model_uuid forces replacement, the next apply would destroy everything just
// imported. Requested values are a fallback, not an override.
func TestModelIdentity_LiveEvidenceOutranksRequestedValue(t *testing.T) {
	const live = "b62cdacf-9e9b-4e35-8c5e-e334930e2b02"
	config := map[string]string{"model_uuid": "11111111-1111-1111-1111-111111111111"}
	pctx := importer.PreflightContext{
		Config:      config,
		QueryConfig: map[string]string{"model_uuid": "22222222-2222-2222-2222-222222222222"},
		Live: []tfexec.LiveResource{
			{ResourceType: "juju_application", Identity: map[string]any{"id": live + ":grafana"}},
		},
	}
	if err := (&ModelIdentity{}).Run(context.Background(), pctx); err != nil {
		t.Fatal(err)
	}
	if config["model_uuid"] != live {
		t.Errorf("Config[model_uuid] = %q, want the live model %q", config["model_uuid"], live)
	}
}

// The step writes the derived UUID into the wrapper so the *following* plan
// sees a concrete model.uuid (which gates local.create_model in COS-Lite).
func TestModelIdentity_WritesModelUUIDIntoWrapper(t *testing.T) {
	const uuid = "b62cdacf-9e9b-4e35-8c5e-e334930e2b02"
	dir := t.TempDir()
	ws := &wrapper.State{
		Dir:             dir,
		ModuleBlockName: "cos_lite",
		Source:          "git::https://example.com/o11y.git//terraform/cos-lite",
		Vars: []tfvars.Variable{{
			Name:       "model",
			HasDefault: true,
			Default:    cty.EmptyObjectVal,
			Type: &tftypes.Type{
				Kind:      tftypes.KindObject,
				AttrOrder: []string{"uuid", "name"},
				Attributes: map[string]*tftypes.ObjectAttr{
					"uuid": {Type: &tftypes.Type{Kind: tftypes.KindString}, Optional: true},
					"name": {Type: &tftypes.Type{Kind: tftypes.KindString}, Optional: true,
						HasDefault: true, Default: cty.StringVal("cos-lite")},
				},
			},
		}},
		Values: map[string]cty.Value{},
	}
	pctx := importer.PreflightContext{
		Dir:          dir,
		WrapperState: ws,
		Config:       map[string]string{},
		QueryConfig:  map[string]string{"model_uuid": uuid},
	}
	if err := (&ModelIdentity{}).Run(context.Background(), pctx); err != nil {
		t.Fatal(err)
	}
	main, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(main), uuid) {
		t.Errorf("main.tf does not carry the derived UUID:\n%s", main)
	}
}

func TestModelIdentity_Name(t *testing.T) {
	if got := (&ModelIdentity{}).Name(); got != "Derive model identity" {
		t.Errorf("Name(): got %q", got)
	}
}

// A module that carries the model UUID under an unrecognised name must not fail
// silently: import IDs still work (they come from live identities), but the plan
// runs with the UUID unset, which changes resource addresses in modules that
// branch on it. The step reports ModelUUIDNoVariable so the caller can warn.
func TestModelIdentity_UnrecognisedVariableIsNotSilent(t *testing.T) {
	const uuid = "b62cdacf-9e9b-4e35-8c5e-e334930e2b02"
	dir := t.TempDir()
	ws := &wrapper.State{
		Dir:             dir,
		ModuleBlockName: "thing",
		Source:          "git::https://example.com/thing.git//terraform",
		// Deliberately not `model` or `model_uuid`.
		Vars: []tfvars.Variable{{
			Name:       "target_model_id",
			HasDefault: true,
			Default:    cty.StringVal(""),
			Type:       &tftypes.Type{Kind: tftypes.KindString},
		}},
		Values: map[string]cty.Value{},
	}
	if got := InjectModelUUID(ws, uuid, ""); got != ModelUUIDNoVariable {
		t.Fatalf("got %v, want ModelUUIDNoVariable", got)
	}

	// The step itself must still succeed and still make the UUID available for
	// building import IDs, so the run is degraded rather than broken.
	config := map[string]string{}
	pctx := importer.PreflightContext{
		Dir:          dir,
		WrapperState: ws,
		Config:       config,
		QueryConfig:  map[string]string{"model_uuid": uuid},
	}
	if err := (&ModelIdentity{}).Run(context.Background(), pctx); err != nil {
		t.Fatal(err)
	}
	if config["model_uuid"] != uuid {
		t.Errorf("Config[model_uuid] = %q; import IDs must still work", config["model_uuid"])
	}
}

// --- ModelConsistency (plan check) ---

func plannedApp(addr, modelUUID string) importer.PlannedResource {
	return importer.PlannedResource{
		Address:      addr,
		Type:         "juju_application",
		PlannedAttrs: map[string]any{"model_uuid": modelUUID},
	}
}

// The destructive case: the plan targets a different model than the live
// resources came from. Verified against a live deployment as
// "Plan: 46 to add, 0 to change, 43 to destroy".
func TestModelConsistency_RefusesMismatch(t *testing.T) {
	const live = "b62cdacf-9e9b-4e35-8c5e-e334930e2b02"
	const declared = "7e1cf453-84c4-4ee5-8c1e-cfd007970c5c"
	err := (&ModelConsistency{}).Check(context.Background(), importer.PlanCheckContext{
		Live:    []tfexec.LiveResource{{ResourceType: "juju_application", Identity: map[string]any{"id": live + ":grafana"}}},
		Planned: []importer.PlannedResource{plannedApp("module.cos_lite.module.grafana.juju_application.grafana", declared)},
	})
	if err == nil {
		t.Fatal("expected an error for a model mismatch, got nil")
	}
	for _, want := range []string{"model mismatch", live, declared, "DESTROY", "grafana"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// The coverage that the wrapper-based guard lacked: without --source there is no
// wrapper at all, and the check must still fire.
func TestModelConsistency_FiresWithoutWrapper(t *testing.T) {
	const live = "b62cdacf-9e9b-4e35-8c5e-e334930e2b02"
	err := (&ModelConsistency{}).Check(context.Background(), importer.PlanCheckContext{
		WrapperState: nil, // no --source
		QueryConfig:  map[string]string{"model_uuid": live},
		Planned:      []importer.PlannedResource{plannedApp("juju_application.a", "11111111-1111-1111-1111-111111111111")},
	})
	if err == nil {
		t.Fatal("check must fire without a wrapper, got nil")
	}
	if !strings.Contains(err.Error(), "the planned configuration") {
		t.Errorf("error should attribute the value to the plan, got: %v", err)
	}
}

func TestModelConsistency_MatchingModelPasses(t *testing.T) {
	const uuid = "b62cdacf-9e9b-4e35-8c5e-e334930e2b02"
	err := (&ModelConsistency{}).Check(context.Background(), importer.PlanCheckContext{
		Live:    []tfexec.LiveResource{{ResourceType: "juju_application", Identity: map[string]any{"id": uuid + ":grafana"}}},
		Planned: []importer.PlannedResource{plannedApp("juju_application.a", uuid)},
	})
	if err != nil {
		t.Errorf("matching model must pass, got %v", err)
	}
}

// An unset or unknown model_uuid is the "planning without it" case that the
// preflight step warns about — not a mismatch, and not fatal.
func TestModelConsistency_UnsetModelUUIDIsNotAMismatch(t *testing.T) {
	const uuid = "b62cdacf-9e9b-4e35-8c5e-e334930e2b02"
	for _, planned := range []importer.PlannedResource{
		plannedApp("juju_application.a", ""),
		{Address: "juju_application.b", Type: "juju_application", PlannedAttrs: map[string]any{}},
		{Address: "juju_application.c", Type: "juju_application"},
	} {
		err := (&ModelConsistency{}).Check(context.Background(), importer.PlanCheckContext{
			QueryConfig: map[string]string{"model_uuid": uuid},
			Planned:     []importer.PlannedResource{planned},
		})
		if err != nil {
			t.Errorf("%s: unset model_uuid must not be fatal, got %v", planned.Address, err)
		}
	}
}

// With no live evidence and no requested UUID there is nothing to compare.
func TestModelConsistency_NoKnownModelPasses(t *testing.T) {
	err := (&ModelConsistency{}).Check(context.Background(), importer.PlanCheckContext{
		Planned: []importer.PlannedResource{plannedApp("juju_application.a", "whatever")},
	})
	if err != nil {
		t.Errorf("got %v, want nil", err)
	}
}

func TestModelConsistency_Name(t *testing.T) {
	if got := (&ModelConsistency{}).Name(); got != "Check model consistency" {
		t.Errorf("Name() = %q", got)
	}
}

// --- BuildImportID ---

func TestBuildImportID(t *testing.T) {
	config := map[string]string{"model_uuid": "test-uuid"}
	m := importer.MatchedImport{
		Address:      "module.cos.juju_application.alertmanager",
		ResourceType: "juju_application",
		Name:         "alertmanager",
	}
	id := BuildImportID(m, config)
	if id != "test-uuid:alertmanager" {
		t.Errorf("expected test-uuid:alertmanager, got %s", id)
	}
}

func TestBuildImportIDNoModelUUID(t *testing.T) {
	config := map[string]string{}
	m := importer.MatchedImport{
		Address:      "module.cos.juju_application.alertmanager",
		ResourceType: "juju_application",
		Name:         "alertmanager",
	}
	id := BuildImportID(m, config)
	if id != "" {
		t.Errorf("expected empty for missing model_uuid, got %s", id)
	}
}

func TestBuildImportIDJujuModel(t *testing.T) {
	config := map[string]string{"model_uuid": "test-uuid"}
	m := importer.MatchedImport{
		Address:      "module.cos.juju_model.cos",
		ResourceType: "juju_model",
		Name:         "cos",
	}
	id := BuildImportID(m, config)
	if id != "test-uuid" {
		t.Errorf("expected test-uuid, got %s", id)
	}
}

func TestBuildImportIDUsesDeployedName(t *testing.T) {
	config := map[string]string{"model_uuid": "test-uuid"}
	m := importer.MatchedImport{
		Address:      "module.cos_lite.module.ssc[0].juju_application.self-signed-certificates",
		ResourceType: "juju_application",
		Name:         "ca",
	}
	id := BuildImportID(m, config)
	if id != "test-uuid:ca" {
		t.Errorf("expected test-uuid:ca (deployed app name), got %s", id)
	}
}

func TestBuildImportIDJujuIntegration(t *testing.T) {
	config := map[string]string{"model_uuid": "test-uuid"}
	m := importer.MatchedImport{
		Address:      "module.cos.juju_integration.alertmanager_loki",
		ResourceType: "juju_integration",
		Name:         "integration1",
		Identity:     map[string]any{"id": "test-uuid:alertmanager:alerting:loki:loki"},
	}
	id := BuildImportID(m, config)
	if id != "test-uuid:alertmanager:alerting:loki:loki" {
		t.Errorf("expected composite integration ID, got %s", id)
	}
}

func TestBuildImportIDJujuOffer(t *testing.T) {
	config := map[string]string{"model_uuid": "test-uuid"}
	m := importer.MatchedImport{
		Address:      "module.cos.juju_offer.loki",
		ResourceType: "juju_offer",
		Name:         "offer1",
		Identity:     map[string]any{"id": "admin/model.loki-offer"},
	}
	id := BuildImportID(m, config)
	if id != "admin/model.loki-offer" {
		t.Errorf("expected offer URL, got %s", id)
	}
}
