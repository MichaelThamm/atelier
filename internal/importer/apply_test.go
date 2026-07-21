package importer

import (
	"testing"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/MichaelThamm/atelier/internal/tfexec"
)

func TestPlannedCreates(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		{Address: "module.cos.juju_application.alertmanager", Type: "juju_application",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}}},
		{Address: "module.cos.juju_model.cos", Type: "juju_model",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}}},
		{Address: "module.cos.juju_application.already_there", Type: "juju_application",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionNoop}}},
		{Address: "module.cos.juju_application.being_replaced", Type: "juju_application",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionDelete, tfjson.ActionCreate}}},
	}}
	creates := PlannedCreates(plan)
	if len(creates) != 2 {
		t.Fatalf("expected 2 planned creates, got %d: %v", len(creates), creates)
	}
	if creates[0].Address != "module.cos.juju_application.alertmanager" {
		t.Errorf("unexpected first create: %s", creates[0].Address)
	}
}

func TestPlannedCreatesFiltersUnimportableTypes(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		{Address: "module.cos.juju_application.alertmanager", Type: "juju_application",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}}},
		{Address: "module.cos.terraform_data.replace_triggers", Type: "terraform_data",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}}},
		{Address: "module.cos.terraform_data.interface", Type: "terraform_data",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}}},
	}}
	creates := PlannedCreates(plan)
	if len(creates) != 1 {
		t.Fatalf("expected 1 planned create (terraform_data filtered), got %d: %v", len(creates), creates)
	}
	if creates[0].Type != "juju_application" {
		t.Errorf("expected juju_application, got %s", creates[0].Type)
	}
}

func TestPlannedCreatesNil(t *testing.T) {
	if got := PlannedCreates(nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestMatchByName(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "juju_application", DisplayName: "alertmanager"},
		{ResourceType: "juju_application", DisplayName: "prometheus"},
		{ResourceType: "juju_space", DisplayName: "alpha"},
	}
	planned := []PlannedResource{
		{Address: "module.cos.juju_application.alertmanager", Type: "juju_application"},
		{Address: "module.cos.juju_application.prometheus", Type: "juju_application"},
		{Address: "module.cos.juju_model.cos", Type: "juju_model"},
	}
	matched, unmatchedPlanned, unmatchedLive := Match(live, planned, false)

	if len(matched) != 2 {
		t.Fatalf("expected 2 matched, got %d", len(matched))
	}
	if len(unmatchedPlanned) != 1 || unmatchedPlanned[0].Type != "juju_model" {
		t.Errorf("expected 1 unmatched planned (juju_model), got %v", unmatchedPlanned)
	}
	if len(unmatchedLive) != 1 || unmatchedLive[0].DisplayName != "alpha" {
		t.Errorf("expected 1 unmatched live (juju_space/alpha), got %v", unmatchedLive)
	}
}

func TestMatchAmbiguous(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "juju_application", DisplayName: "a"},
		{ResourceType: "juju_application", DisplayName: "a"}, // duplicate name
	}
	planned := []PlannedResource{
		{Address: "module.cos.juju_application.a", Type: "juju_application"},
	}
	_, unmatchedPlanned, _ := Match(live, planned, false)
	if len(unmatchedPlanned) != 1 {
		t.Fatalf("expected 1 unmatched planned (ambiguous), got 0")
	}
}

func TestBuildImportID(t *testing.T) {
	config := map[string]string{"model_uuid": "test-uuid"}
	m := MatchedImport{
		Address:      "module.cos.juju_application.alertmanager",
		ResourceType: "juju_application",
		Name:         "alertmanager",
	}
	id := JujuBuildImportID(m, config)
	if id != "test-uuid:alertmanager" {
		t.Errorf("expected test-uuid:alertmanager, got %s", id)
	}
}

func TestBuildImportIDNoModelUUID(t *testing.T) {
	config := map[string]string{}
	m := MatchedImport{
		Address:      "module.cos.juju_application.alertmanager",
		ResourceType: "juju_application",
		Name:         "alertmanager",
	}
	id := JujuBuildImportID(m, config)
	if id != "" {
		t.Errorf("expected empty for missing model_uuid, got %s", id)
	}
}

func TestBuildImportIDJujuModel(t *testing.T) {
	config := map[string]string{"model_uuid": "test-uuid"}
	m := MatchedImport{
		Address:      "module.cos.juju_model.cos",
		ResourceType: "juju_model",
		Name:         "cos",
	}
	id := JujuBuildImportID(m, config)
	if id != "test-uuid" {
		t.Errorf("expected test-uuid, got %s", id)
	}
}

func TestBuildImportIDUsesDeployedName(t *testing.T) {
	config := map[string]string{"model_uuid": "test-uuid"}
	m := MatchedImport{
		Address:      "module.cos_lite.module.ssc[0].juju_application.self-signed-certificates",
		ResourceType: "juju_application",
		Name:         "ca",
	}
	id := JujuBuildImportID(m, config)
	if id != "test-uuid:ca" {
		t.Errorf("expected test-uuid:ca (deployed app name), got %s", id)
	}
}

func TestBuildImportIDJujuIntegration(t *testing.T) {
	config := map[string]string{"model_uuid": "test-uuid"}
	m := MatchedImport{
		Address:      "module.cos.juju_integration.alertmanager_loki",
		ResourceType: "juju_integration",
		Name:         "integration1",
		Identity:     map[string]any{"id": "test-uuid:alertmanager:alerting:loki:loki"},
	}
	id := JujuBuildImportID(m, config)
	if id != "test-uuid:alertmanager:alerting:loki:loki" {
		t.Errorf("expected composite integration ID, got %s", id)
	}
}

func TestBuildImportIDJujuOffer(t *testing.T) {
	config := map[string]string{"model_uuid": "test-uuid"}
	m := MatchedImport{
		Address:      "module.cos.juju_offer.loki",
		ResourceType: "juju_offer",
		Name:         "offer1",
		Identity:     map[string]any{"id": "admin/model.loki-offer"},
	}
	id := JujuBuildImportID(m, config)
	if id != "admin/model.loki-offer" {
		t.Errorf("expected offer URL, got %s", id)
	}
}

func TestPlannedCreatesExtractsPlannedName(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		{
			Address: "module.cos.ssc.juju_application.self-signed-certificates",
			Type:    "juju_application",
			Change: &tfjson.Change{
				Actions: tfjson.Actions{tfjson.ActionCreate},
				After:   map[string]any{"name": "ca", "units": float64(1)},
			},
		},
	}}
	creates := PlannedCreates(plan)
	if len(creates) != 1 {
		t.Fatalf("expected 1 planned create, got %d", len(creates))
	}
	if creates[0].PlannedName != "ca" {
		t.Errorf("expected PlannedName = %q, got %q", "ca", creates[0].PlannedName)
	}
}

func TestMatchByPlannedName(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "juju_application", DisplayName: "alertmanager"},
		{ResourceType: "juju_application", DisplayName: "ca"},
	}
	planned := []PlannedResource{
		{Address: "module.cos.juju_application.alertmanager", Type: "juju_application"},
		{Address: "module.cos.ssc.juju_application.self-signed-certificates", Type: "juju_application", PlannedName: "ca"},
	}
	matched, unmatchedPlanned, unmatchedLive := Match(live, planned, false)

	if len(matched) != 2 {
		t.Fatalf("expected 2 matched, got %d: matched=%v unmatched=%v", len(matched), matched, unmatchedPlanned)
	}
	if len(unmatchedLive) != 0 {
		t.Errorf("expected 0 unmatched live, got %v", unmatchedLive)
	}

	// Verify self-signed-certificates matched via PlannedName.
	for _, m := range matched {
		if m.Address == "module.cos.ssc.juju_application.self-signed-certificates" && m.Name != "ca" {
			t.Errorf("expected Name=ca for self-signed-certificates, got %q", m.Name)
		}
	}
}

func TestPlannedCreatesExtractsIdentity(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		{
			Address: "module.cos.aws_instance.web",
			Type:    "aws_instance",
			Change: &tfjson.Change{
				Actions:       tfjson.Actions{tfjson.ActionCreate},
				After:         map[string]any{"ami": "abc-123", "instance_type": "t2.micro"},
				AfterIdentity: map[string]any{"id": "i-0123456789abcdef0"},
			},
		},
	}}
	creates := PlannedCreates(plan)
	if len(creates) != 1 {
		t.Fatalf("expected 1 planned create, got %d", len(creates))
	}
	if creates[0].Identity == nil {
		t.Fatal("expected Identity to be extracted")
	}
	if creates[0].Identity["id"] != "i-0123456789abcdef0" {
		t.Errorf("expected Identity.id = i-0123456789abcdef0, got %v", creates[0].Identity["id"])
	}
}

func TestPlannedCreatesNoIdentity(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		{
			Address: "module.cos.juju_application.web",
			Type:    "juju_application",
			Change: &tfjson.Change{
				Actions: tfjson.Actions{tfjson.ActionCreate},
				After:   map[string]any{"name": "web"},
			},
		},
	}}
	creates := PlannedCreates(plan)
	if len(creates) != 1 {
		t.Fatalf("expected 1 planned create, got %d", len(creates))
	}
	if creates[0].Identity != nil {
		t.Errorf("expected nil Identity when AfterIdentity is absent, got %v", creates[0].Identity)
	}
}

func TestMatchByIdentity(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "aws_instance", DisplayName: "web", Identity: map[string]any{"id": "i-aaa"}},
		{ResourceType: "aws_instance", DisplayName: "web", Identity: map[string]any{"id": "i-bbb"}},
	}
	planned := []PlannedResource{
		{
			Address:  "module.cos.aws_instance.web",
			Type:     "aws_instance",
			Identity: map[string]any{"id": "i-bbb"},
		},
	}
	matched, unmatchedPlanned, unmatchedLive := Match(live, planned, false)

	if len(matched) != 1 {
		t.Fatalf("expected 1 matched by identity, got %d: matched=%v unmatched=%v", len(matched), matched, unmatchedPlanned)
	}
	if matched[0].Address != "module.cos.aws_instance.web" {
		t.Errorf("expected matched address=module.cos.aws_instance.web, got %q", matched[0].Address)
	}
	if len(unmatchedLive) != 1 {
		t.Errorf("expected 1 unmatched live, got %d", len(unmatchedLive))
	}
}

func TestMatchByIdentityFallsBackToName(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "aws_instance", DisplayName: "web", Identity: map[string]any{"id": "i-aaa"}},
	}
	planned := []PlannedResource{
		{
			Address:  "module.cos.aws_instance.web",
			Type:     "aws_instance",
			Identity: map[string]any{"id": "i-nonexistent"},
		},
	}
	matched, unmatchedPlanned, _ := Match(live, planned, false)

	if len(matched) != 1 {
		t.Fatalf("expected fallback to name match, got %d matched: unmatched=%v", len(matched), unmatchedPlanned)
	}
	if matched[0].Name != "web" {
		t.Errorf("expected name-based match, got %q", matched[0].Name)
	}
}

func TestMatchByIdentityAmbiguousFallsBackToName(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "aws_instance", DisplayName: "web", Identity: map[string]any{"id": "i-shared"}},
		{ResourceType: "aws_instance", DisplayName: "web", Identity: map[string]any{"id": "i-shared"}},
	}
	planned := []PlannedResource{
		{
			Address:  "module.cos.aws_instance.web",
			Type:     "aws_instance",
			Identity: map[string]any{"id": "i-shared"},
		},
	}
	matched, _, _ := Match(live, planned, false)

	// Identity finds 2 (ambiguous, same id), falls back to name which also finds 2.
	if len(matched) != 0 {
		t.Errorf("expected 0 matched (ambiguous both ways), got %d", len(matched))
	}
}

func TestIdentityMatch(t *testing.T) {
	cases := []struct {
		name    string
		planned map[string]any
		live    map[string]any
		want    bool
	}{
		{
			name:    "exact match",
			planned: map[string]any{"id": "x"},
			live:    map[string]any{"id": "x"},
			want:    true,
		},
		{
			name:    "extra keys in live ignored",
			planned: map[string]any{"id": "x"},
			live:    map[string]any{"id": "x", "region": "us-east-1"},
			want:    true,
		},
		{
			name:    "missing key in live",
			planned: map[string]any{"id": "x", "region": "us-east-1"},
			live:    map[string]any{"id": "x"},
			want:    false,
		},
		{
			name:    "value mismatch",
			planned: map[string]any{"id": "x"},
			live:    map[string]any{"id": "y"},
			want:    false,
		},
		{
			name:    "empty planned",
			planned: map[string]any{},
			live:    map[string]any{"id": "x"},
			want:    false,
		},
		{
			name:    "nil live",
			planned: map[string]any{"id": "x"},
			live:    nil,
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := identityMatch(tc.planned, tc.live)
			if got != tc.want {
				t.Errorf("identityMatch(%v, %v) = %v, want %v", tc.planned, tc.live, got, tc.want)
			}
		})
	}
}

func TestMatchIntegrationByCompositeID(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "juju_integration", DisplayName: "rel1", Identity: map[string]any{"id": "uuid-1234:alertmanager:alerting:loki:loki"}},
		{ResourceType: "juju_integration", DisplayName: "rel2", Identity: map[string]any{"id": "uuid-1234:grafana:dashboards:prometheus:prometheus"}},
	}
	planned := []PlannedResource{
		{
			Address: "module.cos.juju_integration.alertmanager_loki",
			Type:    "juju_integration",
			PlannedAttrs: map[string]any{
				"application": []any{
					map[string]any{"name": "loki", "endpoint": "loki"},
					map[string]any{"name": "alertmanager", "endpoint": "alerting"},
				},
			},
		},
		{
			Address: "module.cos.juju_integration.grafana_prometheus",
			Type:    "juju_integration",
			PlannedAttrs: map[string]any{
				"application": []any{
					map[string]any{"name": "grafana", "endpoint": "dashboards"},
					map[string]any{"name": "prometheus", "endpoint": "prometheus"},
				},
			},
		},
	}
	matched, unmatchedPlanned, unmatchedLive := Match(live, planned, false)

	if len(matched) != 2 {
		t.Fatalf("expected 2 matched by composite ID, got %d: matched=%v unmatched=%v", len(matched), matched, unmatchedPlanned)
	}
	if len(unmatchedLive) != 0 {
		t.Errorf("expected 0 unmatched live, got %d: %v", len(unmatchedLive), unmatchedLive)
	}
}

func TestMatchIntegrationNoMatchWhenAppsDiffer(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "juju_integration", DisplayName: "rel1", Identity: map[string]any{"id": "uuid-1234:alertmanager:alerting:loki:loki"}},
	}
	planned := []PlannedResource{
		{
			Address: "module.cos.juju_integration.alertmanager_prometheus",
			Type:    "juju_integration",
			PlannedAttrs: map[string]any{
				"application": []any{
					map[string]any{"name": "alertmanager", "endpoint": "alerting"},
					map[string]any{"name": "prometheus", "endpoint": "prometheus"},
				},
			},
		},
	}
	matched, unmatchedPlanned, _ := Match(live, planned, false)

	if len(matched) != 0 {
		t.Errorf("expected 0 matched (apps differ), got %d: %v", len(matched), matched)
	}
	if len(unmatchedPlanned) != 1 {
		t.Errorf("expected 1 unmatched planned, got %d", len(unmatchedPlanned))
	}
}

func TestMatchIntegrationNoMatchWhenEndpointDiffers(t *testing.T) {
	// Same apps (alertmanager, grafana) but different endpoints → must NOT match
	live := []tfexec.LiveResource{
		{ResourceType: "juju_integration", DisplayName: "rel1", Identity: map[string]any{"id": "uuid-1234:alertmanager:grafana_dashboard:grafana:grafana_dashboard"}},
		{ResourceType: "juju_integration", DisplayName: "rel2", Identity: map[string]any{"id": "uuid-1234:alertmanager:grafana_source:grafana:grafana_source"}},
	}
	planned := []PlannedResource{
		{
			Address: "module.cos.juju_integration.grafana_dashboards_alertmanager",
			Type:    "juju_integration",
			PlannedAttrs: map[string]any{
				"application": []any{
					map[string]any{"name": "alertmanager", "endpoint": "grafana_dashboard"},
					map[string]any{"name": "grafana", "endpoint": "grafana_dashboard"},
				},
			},
		},
		{
			Address: "module.cos.juju_integration.grafana_sources_alertmanager",
			Type:    "juju_integration",
			PlannedAttrs: map[string]any{
				"application": []any{
					map[string]any{"name": "alertmanager", "endpoint": "grafana_source"},
					map[string]any{"name": "grafana", "endpoint": "grafana_source"},
				},
			},
		},
	}
	matched, unmatchedPlanned, unmatchedLive := Match(live, planned, false)

	if len(matched) != 2 {
		t.Fatalf("expected 2 matched (distinct endpoints), got %d: matched=%v unmatched=%v live=%v", len(matched), matched, unmatchedPlanned, unmatchedLive)
	}
}

func TestExtractIntegrationEndpointPairs(t *testing.T) {
	attrs := map[string]any{
		"application": []any{
			map[string]any{"name": "loki", "endpoint": "loki"},
			map[string]any{"name": "alertmanager", "endpoint": "alerting"},
		},
	}
	pairs := extractIntegrationEndpointPairs(attrs)
	if len(pairs) != 2 || pairs[0] != "alertmanager:alerting" || pairs[1] != "loki:loki" {
		t.Errorf("extractIntegrationEndpointPairs = %v, want [alertmanager:alerting loki:loki]", pairs)
	}
}

func TestParseIntegrationIDEndpointPairs(t *testing.T) {
	identity := map[string]any{"id": "uuid-1234:alertmanager:alerting:loki:loki"}
	pairs := parseIntegrationIDEndpointPairs(identity)
	if len(pairs) != 2 || pairs[0] != "alertmanager:alerting" || pairs[1] != "loki:loki" {
		t.Errorf("parseIntegrationIDEndpointPairs = %v, want [alertmanager:alerting loki:loki]", pairs)
	}
}

func TestParseIntegrationIDEndpointPairsMalformed(t *testing.T) {
	cases := []struct {
		name     string
		identity map[string]any
		wantLen  int
	}{
		{"nil", nil, 0},
		{"too few parts", map[string]any{"id": "uuid:app1"}, 0},
		{"non-string", map[string]any{"id": 123}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseIntegrationIDEndpointPairs(tc.identity)
			if len(got) != tc.wantLen {
				t.Errorf("parseIntegrationIDEndpointPairs returned %d elements, want %d", len(got), tc.wantLen)
			}
		})
	}
}

func TestMatchOfferByName(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "juju_offer", DisplayName: "offer1", Identity: map[string]any{"id": "admin/model.loki-offer"}},
		{ResourceType: "juju_offer", DisplayName: "offer2", Identity: map[string]any{"id": "admin/model.prometheus-offer"}},
	}
	planned := []PlannedResource{
		{
			Address: "module.cos.juju_offer.loki",
			Type:    "juju_offer",
			PlannedAttrs: map[string]any{
				"name":             "loki-offer",
				"application_name": "loki",
			},
		},
		{
			Address: "module.cos.juju_offer.prometheus",
			Type:    "juju_offer",
			PlannedAttrs: map[string]any{
				"name":             "prometheus-offer",
				"application_name": "prometheus",
			},
		},
	}
	matched, unmatchedPlanned, unmatchedLive := Match(live, planned, false)

	if len(matched) != 2 {
		t.Fatalf("expected 2 matched by offer name, got %d: matched=%v unmatched=%v", len(matched), matched, unmatchedPlanned)
	}
	if len(unmatchedLive) != 0 {
		t.Errorf("expected 0 unmatched live, got %d: %v", len(unmatchedLive), unmatchedLive)
	}
}
