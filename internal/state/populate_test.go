package state_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/state"
	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

func writeTestState(t *testing.T, dir string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), data, 0o644); err != nil {
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

func instAttrs(t *testing.T, m map[string]interface{}) map[string]interface{} {
	t.Helper()
	resources := m["resources"].([]interface{})
	res := resources[0].(map[string]interface{})
	return res["instances"].([]interface{})[0].(map[string]interface{})
}

func TestNormalizeNullAttributes_ReplacesNullWithDefault(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, []byte(`{
  "version": 4,
  "terraform_version": "1.15.8",
  "serial": 1,
  "lineage": "abc",
  "resources": [{
    "module": "module.cos_lite.module.alertmanager",
    "mode": "managed",
    "type": "juju_application",
    "name": "alertmanager",
    "provider": "provider[\"registry.terraform.io/juju/juju\"]",
    "instances": [{
      "schema_version": 1,
      "attributes": {
        "config": null,
        "constraints": "arch=amd64",
        "storage_directives": null,
        "name": "alertmanager"
      },
      "sensitive_attributes": [],
      "identity_schema_version": 0,
      "identity": {"id": "test-id"}
    }]
  }]
}`))

	defaults := map[string]interface{}{
		"config":             map[string]interface{}{},
		"storage_directives": map[string]interface{}{},
	}

	err := state.NormalizeNullAttributes(dir,
		[]string{"module.cos_lite.module.alertmanager.juju_application.alertmanager"},
		defaults)
	if err != nil {
		t.Fatal(err)
	}

	result := readTestState(t, dir)
	inst := instAttrs(t, result)
	attrs := inst["attributes"].(map[string]interface{})

	// config should now be {} instead of null
	if attrs["config"] == nil {
		t.Fatal("config should be {}, still null")
	}
	config := attrs["config"].(map[string]interface{})
	if len(config) != 0 {
		t.Errorf("config should be empty map, got %v", config)
	}

	// constraints unchanged
	if attrs["constraints"] != "arch=amd64" {
		t.Errorf("constraints = %v", attrs["constraints"])
	}

	// storage_directives should now be {} instead of null
	if attrs["storage_directives"] == nil {
		t.Fatal("storage_directives should be {}, still null")
	}
	sd := attrs["storage_directives"].(map[string]interface{})
	if len(sd) != 0 {
		t.Errorf("storage_directives should be empty map, got %v", sd)
	}

	// Verify schema_version, provider, identity preserved
	if inst["schema_version"] != float64(1) {
		t.Errorf("schema_version = %v, want 1", inst["schema_version"])
	}
	resources := result["resources"].([]interface{})
	res := resources[0].(map[string]interface{})
	if res["provider"] != `provider["registry.terraform.io/juju/juju"]` {
		t.Errorf("provider lost: %v", res["provider"])
	}
	ident := inst["identity"].(map[string]interface{})
	if ident["id"] != "test-id" {
		t.Errorf("identity lost: %v", inst["identity"])
	}
}

func TestNormalizeNullAttributes_SkipsNonNull(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, []byte(`{
  "version": 4,
  "resources": [{
    "module": "module.cos_lite.module.alertmanager",
    "mode": "managed",
    "type": "juju_application",
    "name": "alertmanager",
    "instances": [{
      "schema_version": 1,
      "attributes": {
        "config": {"key": "value"},
        "constraints": "arch=amd64"
      }
    }]
  }]
}`))

	defaults := map[string]interface{}{
		"config": map[string]interface{}{},
	}

	err := state.NormalizeNullAttributes(dir,
		[]string{"module.cos_lite.module.alertmanager.juju_application.alertmanager"},
		defaults)
	if err != nil {
		t.Fatal(err)
	}

	result := readTestState(t, dir)
	attrs := instAttrs(t, result)["attributes"].(map[string]interface{})

	config := attrs["config"].(map[string]interface{})
	if config["key"] != "value" {
		t.Errorf("non-null config was modified: %v", config)
	}
}

func TestNormalizeNullAttributes_SkipsUnimportedAddresses(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, []byte(`{
  "version": 4,
  "resources": [{
    "module": "module.cos_lite.module.alertmanager",
    "mode": "managed",
    "type": "juju_application",
    "name": "alertmanager",
    "instances": [{
      "schema_version": 1,
      "attributes": {"config": null}
    }]
  }]
}`))

	defaults := map[string]interface{}{
		"config": map[string]interface{}{},
	}

	err := state.NormalizeNullAttributes(dir,
		[]string{"module.cos_lite.juju_application.different"},
		defaults)
	if err != nil {
		t.Fatal(err)
	}

	result := readTestState(t, dir)
	attrs := instAttrs(t, result)["attributes"].(map[string]interface{})

	if attrs["config"] != nil {
		t.Error("config should still be null for non-imported resource")
	}
}

func TestNormalizeNullAttributes_EmptyDefaults(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, []byte(`{"version":4,"resources":[]}`))

	err := state.NormalizeNullAttributes(dir, []string{"x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeNullAttributes_IndexedResource(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, []byte(`{
  "version": 4,
  "resources": [{
    "module": "module.cos_lite.module.traefik",
    "mode": "managed",
    "type": "juju_application",
    "name": "traefik",
    "instances": [{
      "index_key": 0,
      "schema_version": 1,
      "attributes": {
        "config": null,
        "storage_directives": null
      }
    }]
  }]
}`))

	defaults := map[string]interface{}{
		"config":             map[string]interface{}{},
		"storage_directives": map[string]interface{}{},
	}

	err := state.NormalizeNullAttributes(dir,
		[]string{"module.cos_lite.module.traefik.juju_application.traefik[0]"},
		defaults)
	if err != nil {
		t.Fatal(err)
	}

	result := readTestState(t, dir)
	attrs := instAttrs(t, result)["attributes"].(map[string]interface{})

	if attrs["config"] == nil {
		t.Error("config should be {}, still null")
	}
	if attrs["storage_directives"] == nil {
		t.Error("storage_directives should be {}, still null")
	}
}

func TestComputeNullDefaults(t *testing.T) {
	ws := &wrapper.State{
		Vars: []tfvars.Variable{
			{
				Name: "alertmanager",
				Type: &tftypes.Type{
					Kind: tftypes.KindObject,
					Attributes: map[string]*tftypes.ObjectAttr{
						"config": {
							Type:       &tftypes.Type{Kind: tftypes.KindMap, Element: &tftypes.Type{Kind: tftypes.KindString}},
							Optional:   true,
							HasDefault: true,
							Default:    cty.MapValEmpty(cty.String),
						},
						"constraints": {
							Type:       &tftypes.Type{Kind: tftypes.KindString},
							Optional:   true,
							HasDefault: true,
							Default:    cty.StringVal("arch=amd64"),
						},
						"storage_directives": {
							Type:       &tftypes.Type{Kind: tftypes.KindMap, Element: &tftypes.Type{Kind: tftypes.KindString}},
							Optional:   true,
							HasDefault: true,
							Default:    cty.MapValEmpty(cty.String),
						},
					},
					AttrOrder: []string{"config", "constraints", "storage_directives"},
				},
			},
			{
				Name: "model_uuid",
				Type: &tftypes.Type{Kind: tftypes.KindString},
			},
		},
	}

	defaults := state.ComputeNullDefaults(ws)
	if defaults == nil {
		t.Fatal("expected non-nil defaults")
	}

	if _, ok := defaults["config"]; !ok {
		t.Error("config default missing")
	}
	if _, ok := defaults["storage_directives"]; !ok {
		t.Error("storage_directives default missing")
	}
	if v, ok := defaults["constraints"]; !ok {
		t.Error("constraints default missing")
	} else if v != "arch=amd64" {
		t.Errorf("constraints default = %v", v)
	}
	if _, ok := defaults["model_uuid"]; ok {
		t.Error("model_uuid should not be in defaults")
	}
}

func TestComputeNullDefaults_NilState(t *testing.T) {
	if d := state.ComputeNullDefaults(nil); d != nil {
		t.Errorf("expected nil, got %v", d)
	}
}

func TestEnsureSchemaVersions_SetsVersion(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, []byte(`{
  "version": 4,
  "resources": [{
    "mode": "managed",
    "type": "juju_application",
    "name": "alertmanager",
    "instances": [{
      "attributes": {"name": "alertmanager"}
    }]
  }]
}`))

	err := state.EnsureSchemaVersions(dir, map[string]int{"juju_application": 1})
	if err != nil {
		t.Fatal(err)
	}

	result := readTestState(t, dir)
	inst := instAttrs(t, result)
	if inst["schema_version"] != float64(1) {
		t.Errorf("schema_version = %v, want 1", inst["schema_version"])
	}
}

func TestEnsureSchemaVersions_SkipsAlreadySet(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, []byte(`{
  "version": 4,
  "resources": [{
    "mode": "managed",
    "type": "juju_application",
    "name": "alertmanager",
    "instances": [{
      "schema_version": 1,
      "attributes": {"name": "alertmanager"}
    }]
  }]
}`))

	err := state.EnsureSchemaVersions(dir, map[string]int{"juju_application": 1})
	if err != nil {
		t.Fatal(err)
	}

	result := readTestState(t, dir)
	inst := instAttrs(t, result)
	if inst["schema_version"] != float64(1) {
		t.Errorf("schema_version = %v, want 1", inst["schema_version"])
	}
}

func TestEnsureSchemaVersions_SkipsNonMatchingTypes(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, []byte(`{
  "version": 4,
  "resources": [{
    "mode": "managed",
    "type": "juju_model",
    "name": "my_model",
    "instances": [{
      "attributes": {"name": "my_model"}
    }]
  }]
}`))

	err := state.EnsureSchemaVersions(dir, map[string]int{"juju_application": 1})
	if err != nil {
		t.Fatal(err)
	}

	result := readTestState(t, dir)
	inst := instAttrs(t, result)
	if _, ok := inst["schema_version"]; ok {
		t.Error("schema_version should not be set for non-matching type")
	}
}

func TestEnsureSchemaVersions_EmptyVersions(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, []byte(`{"version": 4, "resources": []}`))

	err := state.EnsureSchemaVersions(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
}
