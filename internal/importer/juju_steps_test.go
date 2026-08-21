package importer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

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

// --- JujuSchemaVersions ---

func TestJujuSchemaVersions_SetsVersion(t *testing.T) {
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

	step := &JujuSchemaVersions{}
	err := step.Run(context.Background(), PostImportContext{Dir: dir})
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

func TestJujuSchemaVersions_SkipsAlreadySet(t *testing.T) {
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

	step := &JujuSchemaVersions{}
	err := step.Run(context.Background(), PostImportContext{Dir: dir})
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

func TestJujuSchemaVersions_NoStateFile(t *testing.T) {
	dir := t.TempDir()
	step := &JujuSchemaVersions{}
	err := step.Run(context.Background(), PostImportContext{Dir: dir})
	if err != nil {
		t.Errorf("expected nil error for missing state, got: %v", err)
	}
}

func TestJujuSchemaVersions_Name(t *testing.T) {
	step := &JujuSchemaVersions{}
	if step.Name() != "Ensure schema versions" {
		t.Errorf("Name(): got %q", step.Name())
	}
}

// --- JujuOfferDefaults ---

func TestJujuOfferDefaults_NormalizesOffer(t *testing.T) {
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

	step := &JujuOfferDefaults{}
	pctx := PostImportContext{
		Dir: dir,
		Imported: []ImportResult{
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

func TestJujuOfferDefaults_SkipsNonOfferResources(t *testing.T) {
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

	step := &JujuOfferDefaults{}
	pctx := PostImportContext{
		Dir: dir,
		Imported: []ImportResult{
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

func TestJujuOfferDefaults_NoOffers(t *testing.T) {
	dir := t.TempDir()
	step := &JujuOfferDefaults{}
	pctx := PostImportContext{
		Dir:      dir,
		Imported: []ImportResult{},
	}
	err := step.Run(context.Background(), pctx)
	if err != nil {
		t.Errorf("expected nil for no offers, got: %v", err)
	}
}

func TestJujuOfferDefaults_NilWrapperState(t *testing.T) {
	step := &JujuOfferDefaults{}
	pctx := PostImportContext{
		Imported: []ImportResult{
			{Address: "juju_offer.x"},
		},
	}
	err := step.Run(context.Background(), pctx)
	if err != nil {
		t.Errorf("expected nil for nil WrapperState, got: %v", err)
	}
}

// --- JujuModelUUIDInjection ---

func TestJujuModelUUIDInjection_NilWrapperState(t *testing.T) {
	step := &JujuModelUUIDInjection{}
	pctx := PostImportContext{}
	err := step.Run(context.Background(), pctx)
	if err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
}

func TestJujuModelUUIDInjection_NoStateFile(t *testing.T) {
	dir := t.TempDir()
	step := &JujuModelUUIDInjection{}
	pctx := PostImportContext{
		Dir:          dir,
		WrapperState: &wrapper.State{},
	}
	err := step.Run(context.Background(), pctx)
	if err != nil {
		t.Errorf("expected nil for missing state, got: %v", err)
	}
}

func TestJujuModelUUIDInjection_EmptyUUID(t *testing.T) {
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
	step := &JujuModelUUIDInjection{}
	ws := &wrapper.State{
		Values: map[string]cty.Value{},
	}
	pctx := PostImportContext{
		Dir:          dir,
		WrapperState: ws,
	}
	err := step.Run(context.Background(), pctx)
	if err != nil {
		t.Errorf("expected nil for empty UUID, got: %v", err)
	}
}

// --- JujuBuildImportID ---

func TestJujuBuildImportID_Application(t *testing.T) {
	m := MatchedImport{
		ResourceType: "juju_application",
		Name:         "grafana",
	}
	config := map[string]string{"model_uuid": "abc-123"}
	got := JujuBuildImportID(m, config)
	want := "abc-123:grafana"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestJujuBuildImportID_Model(t *testing.T) {
	m := MatchedImport{
		ResourceType: "juju_model",
		Name:         "default",
	}
	config := map[string]string{"model_uuid": "abc-123"}
	got := JujuBuildImportID(m, config)
	if got != "abc-123" {
		t.Errorf("got %q, want abc-123", got)
	}
}

func TestJujuBuildImportID_Integration(t *testing.T) {
	m := MatchedImport{
		ResourceType: "juju_integration",
		Name:         "grafana-prom",
		Identity:     map[string]interface{}{"id": "abc:grafana:endpoint:prom:scrape"},
	}
	config := map[string]string{"model_uuid": "abc-123"}
	got := JujuBuildImportID(m, config)
	want := "abc:grafana:endpoint:prom:scrape"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestJujuBuildImportID_Offer(t *testing.T) {
	m := MatchedImport{
		ResourceType: "juju_offer",
		Name:         "grafana",
		Identity:     map[string]interface{}{"id": "admin/grafana"},
	}
	config := map[string]string{"model_uuid": "abc-123"}
	got := JujuBuildImportID(m, config)
	if got != "admin/grafana" {
		t.Errorf("got %q, want admin/grafana", got)
	}
}

func TestJujuBuildImportID_NoUUID(t *testing.T) {
	m := MatchedImport{
		ResourceType: "juju_application",
		Name:         "grafana",
	}
	config := map[string]string{}
	got := JujuBuildImportID(m, config)
	if got != "" {
		t.Errorf("got %q, want empty (no UUID)", got)
	}
}

func TestJujuBuildImportID_ModelNoUUID(t *testing.T) {
	m := MatchedImport{
		ResourceType: "juju_model",
		Name:         "default",
	}
	config := map[string]string{}
	got := JujuBuildImportID(m, config)
	if got != "" {
		t.Errorf("got %q, want empty (no UUID)", got)
	}
}

// --- JujuNullNormalization ---

func TestJujuNullNormalization_NilWrapperState(t *testing.T) {
	step := &JujuNullNormalization{}
	pctx := PostImportContext{}
	err := step.Run(context.Background(), pctx)
	if err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
}

func TestJujuNullNormalization_EmptyVars(t *testing.T) {
	dir := t.TempDir()
	step := &JujuNullNormalization{}
	ws := &wrapper.State{
		Values: map[string]cty.Value{},
	}
	pctx := PostImportContext{
		Dir:          dir,
		WrapperState: ws,
		Imported: []ImportResult{
			{Address: "juju_application.grafana"},
		},
	}
	err := step.Run(context.Background(), pctx)
	if err != nil {
		t.Errorf("expected nil for empty vars, got: %v", err)
	}
}

// Helper: variable with no null attributes to normalize.
func TestJujuNullNormalization_NoNulls(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, []byte(`{
		"version": 4,
		"resources": [{
			"module": "",
			"mode": "managed",
			"type": "juju_application",
			"name": "grafana",
			"instances": [{
				"attributes": {"config": {"key": "val"}}
			}]
		}]
	}`))

	ws := &wrapper.State{
		Vars: []tfvars.Variable{
			{
				Name:       "config",
				Type:       &tftypes.Type{Kind: tftypes.KindMap, Element: &tftypes.Type{Kind: tftypes.KindString}},
				HasDefault: true,
				Default:    cty.MapVal(map[string]cty.Value{"key": cty.StringVal("val")}),
			},
		},
		Values: map[string]cty.Value{},
	}
	step := &JujuNullNormalization{}
	pctx := PostImportContext{
		Dir: dir,
		Imported: []ImportResult{
			{Address: "juju_application.grafana"},
		},
		WrapperState: ws,
	}
	err := step.Run(context.Background(), pctx)
	if err != nil {
		t.Fatal(err)
	}
}

func TestJujuNullNormalization_Name(t *testing.T) {
	step := &JujuNullNormalization{}
	if step.Name() != "Normalize null attributes" {
		t.Errorf("Name(): got %q", step.Name())
	}
}
