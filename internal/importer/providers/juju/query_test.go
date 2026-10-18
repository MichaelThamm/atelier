package juju

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/wrapper"
)

const (
	wrapperModel = "b62cdacf-9e9b-4e35-8c5e-e334930e2b02"
	stateModel   = "7e1cf453-84c4-4ee5-8c1e-cfd007970c5c"
)

func wrapperWithModel(t *testing.T, uuid string) *wrapper.State {
	t.Helper()
	s := newCosLiteState(t, t.TempDir())
	s.Values["model"] = cty.ObjectVal(map[string]cty.Value{"uuid": cty.StringVal(uuid)})
	return s
}

func writeStateModel(t *testing.T, dir string, uuids ...string) {
	t.Helper()
	resources := make([]string, len(uuids))
	for i, uuid := range uuids {
		resources[i] = `{"mode":"managed","type":"juju_application","name":"app` + string(rune('a'+i)) + `","instances":[{"attributes":{"model_uuid":"` + uuid + `"}}]}`
	}
	data := `{"version":4,"resources":[` + strings.Join(resources, ",") + `]}`
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestQueryDefaults_WrapperModelObject(t *testing.T) {
	got, err := (&Provider{}).QueryDefaults(nil, wrapperWithModel(t, wrapperModel), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got["model_uuid"] != wrapperModel {
		t.Errorf("model_uuid = %q, want %q", got["model_uuid"], wrapperModel)
	}
}

func TestQueryDefaults_StateModel(t *testing.T) {
	dir := t.TempDir()
	writeStateModel(t, dir, stateModel)
	got, err := (&Provider{}).QueryDefaults(nil, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got["model_uuid"] != stateModel {
		t.Errorf("model_uuid = %q, want %q", got["model_uuid"], stateModel)
	}
}

func TestQueryDefaults_WrapperAndStateAgree(t *testing.T) {
	dir := t.TempDir()
	writeStateModel(t, dir, wrapperModel)
	if _, err := (&Provider{}).QueryDefaults(nil, wrapperWithModel(t, wrapperModel), dir); err != nil {
		t.Errorf("agreeing wrapper and state must pass: %v", err)
	}
}

// A wrapper may compose modules from more than one model, so state that also
// holds another model does not contradict the wrapper's own one.
func TestQueryDefaults_WrapperModelAmongSeveralStateModels(t *testing.T) {
	dir := t.TempDir()
	writeStateModel(t, dir, wrapperModel, stateModel)
	got, err := (&Provider{}).QueryDefaults(nil, wrapperWithModel(t, wrapperModel), dir)
	if err != nil {
		t.Fatalf("a multi-model wrapper must not be treated as a mismatch: %v", err)
	}
	if got["model_uuid"] != wrapperModel {
		t.Errorf("model_uuid = %q, want %q", got["model_uuid"], wrapperModel)
	}
}

// With no wrapper model, several state models have no single answer: the user
// has to say which one this run enumerates.
func TestQueryDefaults_StateMultipleModels(t *testing.T) {
	dir := t.TempDir()
	writeStateModel(t, dir, wrapperModel, stateModel)
	_, err := (&Provider{}).QueryDefaults(nil, nil, dir)
	if err == nil {
		t.Fatal("expected an error asking for --query-var")
	}
	for _, want := range []string{wrapperModel, stateModel, "--query-var"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// An explicit choice is allowed even when state spans several models: the user
// is selecting which one to import.
func TestQueryDefaults_ExplicitSelectsSecondaryModel(t *testing.T) {
	dir := t.TempDir()
	writeStateModel(t, dir, wrapperModel, stateModel)
	if _, err := (&Provider{}).QueryDefaults(
		map[string]string{"model_uuid": stateModel},
		wrapperWithModel(t, wrapperModel),
		dir,
	); err != nil {
		t.Errorf("an explicit model recorded in state must be allowed: %v", err)
	}
}

func TestQueryDefaults_ExplicitContradictsStateOnly(t *testing.T) {
	dir := t.TempDir()
	writeStateModel(t, dir, stateModel)
	_, err := (&Provider{}).QueryDefaults(map[string]string{"model_uuid": wrapperModel}, nil, dir)
	if err == nil {
		t.Fatal("expected a conflict error")
	}
	if !strings.Contains(err.Error(), "Terraform state") {
		t.Errorf("error should attribute the recorded model to state, got: %v", err)
	}
}

// A wrapper that names one model while state holds resources from another is
// the destructive case: adopting the wrapper's model would leave state pointing
// at the other, and model_uuid forces replacement.
func TestQueryDefaults_RefusesWrapperStateConflict(t *testing.T) {
	dir := t.TempDir()
	writeStateModel(t, dir, stateModel)
	_, err := (&Provider{}).QueryDefaults(nil, wrapperWithModel(t, wrapperModel), dir)
	if err == nil {
		t.Fatal("expected a conflict error")
	}
	for _, want := range []string{wrapperModel, stateModel, "model mismatch"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestQueryDefaults_ExplicitConflict(t *testing.T) {
	_, err := (&Provider{}).QueryDefaults(
		map[string]string{"model_uuid": stateModel},
		wrapperWithModel(t, wrapperModel),
		t.TempDir(),
	)
	if err == nil {
		t.Fatal("expected a conflict error")
	}
	for _, want := range []string{wrapperModel, stateModel, "--query-var"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestQueryDefaults_ExplicitAgrees(t *testing.T) {
	got, err := (&Provider{}).QueryDefaults(
		map[string]string{"model_uuid": wrapperModel},
		wrapperWithModel(t, wrapperModel),
		t.TempDir(),
	)
	if err != nil {
		t.Fatalf("agreeing explicit value must pass: %v", err)
	}
	if got["model_uuid"] != wrapperModel {
		t.Errorf("model_uuid = %q, want %q", got["model_uuid"], wrapperModel)
	}
}

func TestQueryDefaults_NoIdentity(t *testing.T) {
	got, err := (&Provider{}).QueryDefaults(nil, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}
