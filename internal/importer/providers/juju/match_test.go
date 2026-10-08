package juju

import (
	"testing"

	"github.com/MichaelThamm/atelier/internal/importer"
	"github.com/MichaelThamm/atelier/internal/tfexec"
)

// --- endpoint-pair extraction / comparison ---

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

func TestExtractIntegrationEndpointPairs_NoApplication(t *testing.T) {
	if got := extractIntegrationEndpointPairs(map[string]any{"other": "x"}); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestExtractIntegrationEndpointPairs_EmptyApps(t *testing.T) {
	if got := extractIntegrationEndpointPairs(map[string]any{"application": []any{}}); len(got) != 0 {
		t.Errorf("got %d, want 0", len(got))
	}
}

func TestParseIntegrationIDEndpointPairs(t *testing.T) {
	identity := map[string]any{"id": "uuid-1234:alertmanager:alerting:loki:loki"}
	pairs := parseIntegrationIDEndpointPairs(identity)
	if len(pairs) != 2 || pairs[0] != "alertmanager:alerting" || pairs[1] != "loki:loki" {
		t.Errorf("parseIntegrationIDEndpointPairs = %v, want [alertmanager:alerting loki:loki]", pairs)
	}
}

func TestParseIntegrationIDEndpointPairs_NilIdentity(t *testing.T) {
	if got := parseIntegrationIDEndpointPairs(nil); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestParseIntegrationIDEndpointPairs_Malformed(t *testing.T) {
	cases := []struct {
		name     string
		identity map[string]any
		wantLen  int
	}{
		{"nil", nil, 0},
		{"too few parts", map[string]any{"id": "uuid:app1"}, 0},
		{"non-string", map[string]any{"id": 123}, 0},
		{"wrong part count", map[string]any{"id": "only:three"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseIntegrationIDEndpointPairs(tc.identity); len(got) != tc.wantLen {
				t.Errorf("returned %d elements, want %d", len(got), tc.wantLen)
			}
		})
	}
}

func TestEndpointPairSetEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"equal", []string{"app1:ep1", "app2:ep2"}, []string{"app1:ep1", "app2:ep2"}, true},
		{"both nil", nil, nil, true},
		{"different length", []string{"a:b"}, []string{"a:b", "c:d"}, false},
		{"different content", []string{"a:b"}, []string{"a:x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := endpointPairSetEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("endpointPairSetEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestOfferURLFromIdentity(t *testing.T) {
	if got := offerURLFromIdentity(map[string]any{"id": "admin/model:offer"}); got != "admin/model:offer" {
		t.Errorf("got %q", got)
	}
	if got := offerURLFromIdentity(nil); got != "" {
		t.Errorf("nil: got %q, want empty", got)
	}
	if got := offerURLFromIdentity(map[string]any{"other": "value"}); got != "" {
		t.Errorf("no id: got %q, want empty", got)
	}
}

// TestMatchOfferByName exercises the fallback through the core Match, proving
// the provider hook is wired: importer.Match with the Juju fallback resolves
// offers by name, which the generic identity/name phases cannot.
func TestMatchOfferByName(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "juju_offer", DisplayName: "offer1", Identity: map[string]any{"id": "admin/model.loki-offer"}},
		{ResourceType: "juju_offer", DisplayName: "offer2", Identity: map[string]any{"id": "admin/model.prometheus-offer"}},
	}
	planned := []importer.PlannedResource{
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
	matched, unmatchedPlanned, unmatchedLive := importer.Match(live, planned, matchFallback, false)
	if len(matched) != 2 {
		t.Fatalf("expected 2 matched by offer name, got %d: matched=%v unmatched=%v", len(matched), matched, unmatchedPlanned)
	}
	if len(unmatchedLive) != 0 {
		t.Errorf("expected 0 unmatched live, got %d: %v", len(unmatchedLive), unmatchedLive)
	}
}

func TestMatchIntegrationByCompositeID(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "juju_integration", DisplayName: "rel1", Identity: map[string]any{"id": "uuid-1234:alertmanager:alerting:loki:loki"}},
		{ResourceType: "juju_integration", DisplayName: "rel2", Identity: map[string]any{"id": "uuid-1234:grafana:dashboards:prometheus:prometheus"}},
	}
	planned := []importer.PlannedResource{
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
	matched, unmatchedPlanned, unmatchedLive := importer.Match(live, planned, matchFallback, false)

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
	planned := []importer.PlannedResource{
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
	matched, unmatchedPlanned, _ := importer.Match(live, planned, matchFallback, false)

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
	planned := []importer.PlannedResource{
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
	matched, unmatchedPlanned, unmatchedLive := importer.Match(live, planned, matchFallback, false)

	if len(matched) != 2 {
		t.Fatalf("expected 2 matched (distinct endpoints), got %d: matched=%v unmatched=%v live=%v", len(matched), matched, unmatchedPlanned, unmatchedLive)
	}
}

// An ambiguous offer — two live URLs containing the application name, and no
// exact planned-name match — must stay unmatched, not resolve arbitrarily to
// one of them.
func TestMatchOfferAmbiguousStaysUnmatched(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "juju_offer", DisplayName: "a", Identity: map[string]any{"id": "admin/m.loki-extra"}},
		{ResourceType: "juju_offer", DisplayName: "b", Identity: map[string]any{"id": "admin/m.loki-more"}},
	}
	planned := []importer.PlannedResource{{
		Address:      "module.cos.juju_offer.x",
		Type:         "juju_offer",
		PlannedAttrs: map[string]any{"name": "loki-offer", "application_name": "loki"},
	}}
	matched, unmatchedPlanned, _ := importer.Match(live, planned, matchFallback, false)
	if len(matched) != 0 {
		t.Errorf("ambiguous offer matched uniquely: %+v", matched)
	}
	if len(unmatchedPlanned) != 1 {
		t.Errorf("want 1 unmatched planned, got %d", len(unmatchedPlanned))
	}
}

// A managed juju_model is reported unmatched by the generic phases because the
// live display name is the UUID while the plan carries the model name. The
// Juju fallback must reconnect them, or an existing model shows up as an
// unmatched resource that needs no action.
func TestMatchModelByUUID(t *testing.T) {
	const uuid = "b9663312-d3c5-4ff3-81e5-ad85cb518ffc"
	live := []tfexec.LiveResource{{
		ResourceType: "juju_model",
		DisplayName:  uuid,
		Identity:     map[string]any{"id": uuid},
		Attributes:   map[string]any{"name": "cos", "uuid": uuid},
	}}
	planned := []importer.PlannedResource{{
		Address:      "module.cos.juju_model.cos[0]",
		Type:         "juju_model",
		PlannedAttrs: map[string]any{"name": "cos", "uuid": uuid},
	}}
	matched, unmatchedPlanned, unmatchedLive := importer.Match(live, planned, matchFallback, false)
	if len(matched) != 1 {
		t.Fatalf("expected 1 matched model, got %d: unmatched=%v", len(matched), unmatchedPlanned)
	}
	if matched[0].Address != "module.cos.juju_model.cos[0]" {
		t.Errorf("matched %q, want the model address", matched[0].Address)
	}
	if len(unmatchedLive) != 0 {
		t.Errorf("expected 0 unmatched live, got %v", unmatchedLive)
	}
}

// On a create the plan does not yet know the model UUID, so the fallback falls
// back to the planned name against the live resource_object's "name".
func TestMatchModelByNameWhenUUIDUnknown(t *testing.T) {
	live := []tfexec.LiveResource{{
		ResourceType: "juju_model",
		DisplayName:  "b9663312-d3c5-4ff3-81e5-ad85cb518ffc",
		Identity:     map[string]any{"id": "b9663312-d3c5-4ff3-81e5-ad85cb518ffc"},
		Attributes:   map[string]any{"name": "cos"},
	}}
	planned := []importer.PlannedResource{{
		Address:      "module.cos.juju_model.cos",
		Type:         "juju_model",
		PlannedAttrs: map[string]any{"name": "cos"},
	}}
	matched, _, _ := importer.Match(live, planned, matchFallback, false)
	if len(matched) != 1 {
		t.Fatalf("expected 1 matched model by name, got %d", len(matched))
	}
}

func TestMatchModelNoIdentity(t *testing.T) {
	live := []tfexec.LiveResource{{ResourceType: "juju_model", DisplayName: "x"}}
	planned := []importer.PlannedResource{{Address: "module.cos.juju_model.cos", Type: "juju_model", PlannedAttrs: map[string]any{}}}
	matched, _, _ := importer.Match(live, planned, matchFallback, false)
	if len(matched) != 0 {
		t.Errorf("a model with no planned name or uuid must not match: %+v", matched)
	}
}
