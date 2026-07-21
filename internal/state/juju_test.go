package state

import (
	"testing"
)

func TestExtractModelUUID(t *testing.T) {
	s := &State{
		Resources: []Resource{
			{
				Type: "juju_application",
				Attributes: map[string]interface{}{
					"name":       "alertmanager",
					"model_uuid": "64fa7ee4-f2e6-4f1a-8fe9-aeef8c082578",
				},
			},
			{
				Type: "juju_application",
				Attributes: map[string]interface{}{
					"name":       "grafana",
					"model_uuid": "64fa7ee4-f2e6-4f1a-8fe9-aeef8c082578",
				},
			},
		},
	}
	got := s.ExtractModelUUID()
	if got != "64fa7ee4-f2e6-4f1a-8fe9-aeef8c082578" {
		t.Errorf("ExtractModelUUID = %q, want 64fa7ee4-f2e6-4f1a-8fe9-aeef8c082578", got)
	}
}

func TestExtractModelUUID_NilState(t *testing.T) {
	var s *State
	if got := s.ExtractModelUUID(); got != "" {
		t.Errorf("nil state should return empty, got %q", got)
	}
}

func TestExtractModelUUID_NoApplications(t *testing.T) {
	s := &State{
		Resources: []Resource{
			{
				Type:       "juju_model",
				Attributes: map[string]interface{}{"name": "cos"},
			},
		},
	}
	if got := s.ExtractModelUUID(); got != "" {
		t.Errorf("no juju_application resources should return empty, got %q", got)
	}
}

func TestExtractModelUUID_EmptyModelUUID(t *testing.T) {
	s := &State{
		Resources: []Resource{
			{
				Type: "juju_application",
				Attributes: map[string]interface{}{
					"name":       "alertmanager",
					"model_uuid": "",
				},
			},
		},
	}
	if got := s.ExtractModelUUID(); got != "" {
		t.Errorf("empty model_uuid should return empty, got %q", got)
	}
}

func TestExtractModelName(t *testing.T) {
	s := &State{
		Resources: []Resource{
			{
				Type: "juju_model",
				Attributes: map[string]interface{}{
					"name": "cos-lite",
					"uuid": "64fa7ee4-f2e6-4f1a-8fe9-aeef8c082578",
				},
			},
		},
	}
	got := s.ExtractModelName()
	if got != "cos-lite" {
		t.Errorf("ExtractModelName = %q, want cos-lite", got)
	}
}

func TestExtractModelName_NilState(t *testing.T) {
	var s *State
	if got := s.ExtractModelName(); got != "" {
		t.Errorf("nil state should return empty, got %q", got)
	}
}

func TestExtractModelName_NoJujuModel(t *testing.T) {
	s := &State{
		Resources: []Resource{
			{
				Type: "juju_application",
				Attributes: map[string]interface{}{
					"name": "alertmanager",
				},
			},
		},
	}
	if got := s.ExtractModelName(); got != "" {
		t.Errorf("no juju_model resources should return empty, got %q", got)
	}
}
