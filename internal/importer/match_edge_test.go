package importer

import (
	"testing"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/MichaelThamm/atelier/internal/tfexec"
)

// --- PlannedCreates edge cases ---

func TestPlannedCreates_NilPlan(t *testing.T) {
	got := PlannedCreates(nil)
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestPlannedCreates_NilResourceChange(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		nil,
		{Address: "juju_application.ok", Type: "juju_application",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}}},
	}}
	creates := PlannedCreates(plan)
	if len(creates) != 1 {
		t.Fatalf("got %d, want 1", len(creates))
	}
	if creates[0].Address != "juju_application.ok" {
		t.Errorf("got %q", creates[0].Address)
	}
}

func TestPlannedCreates_NilChange(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		{Address: "juju_application.nil_change", Type: "juju_application", Change: nil},
	}}
	creates := PlannedCreates(plan)
	if len(creates) != 0 {
		t.Errorf("got %d, want 0", len(creates))
	}
}

func TestPlannedCreates_SkipsImporting(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		{Address: "juju_application.importing", Type: "juju_application",
			Change: &tfjson.Change{
				Actions:  tfjson.Actions{tfjson.ActionCreate},
				Importing: &tfjson.Importing{ID: "some-existing-id"},
			}},
		{Address: "juju_application.normal", Type: "juju_application",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}}},
	}}
	creates := PlannedCreates(plan)
	if len(creates) != 1 {
		t.Fatalf("got %d, want 1", len(creates))
	}
	if creates[0].Address != "juju_application.normal" {
		t.Errorf("got %q, want juju_application.normal", creates[0].Address)
	}
}

func TestPlannedCreates_UnimportableType(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		{Address: "terraform_data.replace", Type: "terraform_data",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}}},
		{Address: "juju_application.ok", Type: "juju_application",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}}},
	}}
	creates := PlannedCreates(plan)
	if len(creates) != 1 {
		t.Fatalf("got %d, want 1", len(creates))
	}
	if creates[0].Address != "juju_application.ok" {
		t.Errorf("got %q", creates[0].Address)
	}
}

func TestPlannedCreates_NonMapAfter(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		{Address: "juju_application.computed", Type: "juju_application",
			Change: &tfjson.Change{
				Actions: tfjson.Actions{tfjson.ActionCreate},
				After:   "some_string_value",
			}},
	}}
	creates := PlannedCreates(plan)
	if len(creates) != 1 {
		t.Fatalf("got %d, want 1", len(creates))
	}
	if creates[0].PlannedName != "" {
		t.Errorf("PlannedName: got %q, want empty for non-map After", creates[0].PlannedName)
	}
	if creates[0].PlannedAttrs != nil {
		t.Errorf("PlannedAttrs: got %v, want nil for non-map After", creates[0].PlannedAttrs)
	}
}

func TestPlannedCreates_WithIdentity(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		{Address: "juju_application.id", Type: "juju_application",
			Change: &tfjson.Change{
				Actions: tfjson.Actions{tfjson.ActionCreate},
				After: map[string]any{"name": "app"},
				AfterIdentity: map[string]any{"id": "uuid-123"},
			}},
	}}
	creates := PlannedCreates(plan)
	if len(creates) != 1 {
		t.Fatalf("got %d", len(creates))
	}
	if creates[0].Identity["id"] != "uuid-123" {
		t.Errorf("identity: got %v", creates[0].Identity)
	}
}

// --- shortName ---

func TestShortName(t *testing.T) {
	for _, tc := range []struct {
		addr, want string
	}{
		{"module.cos.juju_application.alertmanager", "alertmanager"},
		{"juju_application.grafana", "grafana"},
		{"single", "single"},
		{"", ""},
		{"a.b.c.d", "d"},
	} {
		got := shortName(tc.addr)
		if got != tc.want {
			t.Errorf("shortName(%q) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

// --- identityMatch ---

func TestIdentityMatch_Equal(t *testing.T) {
	if !identityMatch(map[string]any{"id": "abc"}, map[string]any{"id": "abc"}) {
		t.Error("expected true")
	}
}

func TestIdentityMatch_ExtraLiveKeys(t *testing.T) {
	if !identityMatch(map[string]any{"id": "abc"}, map[string]any{"id": "abc", "extra": "x"}) {
		t.Error("expected true (extra live keys ignored)")
	}
}

func TestIdentityMatch_DifferentValues(t *testing.T) {
	if identityMatch(map[string]any{"id": "abc"}, map[string]any{"id": "xyz"}) {
		t.Error("expected false")
	}
}

func TestIdentityMatch_MissingLiveKey(t *testing.T) {
	if identityMatch(map[string]any{"id": "abc", "name": "x"}, map[string]any{"id": "abc"}) {
		t.Error("expected false (live missing 'name' key)")
	}
}

func TestIdentityMatch_EmptyPlanned(t *testing.T) {
	if identityMatch(map[string]any{}, map[string]any{"id": "abc"}) {
		t.Error("expected false for empty planned")
	}
}

func TestIdentityMatch_EmptyLive(t *testing.T) {
	if identityMatch(map[string]any{"id": "abc"}, map[string]any{}) {
		t.Error("expected false for empty live")
	}
}

// --- endpointPairSetEqual ---

func TestEndpointPairSetEqual_Equal(t *testing.T) {
	a := []string{"app1:ep1", "app2:ep2"}
	b := []string{"app1:ep1", "app2:ep2"}
	if !endpointPairSetEqual(a, b) {
		t.Error("expected true")
	}
}

func TestEndpointPairSetEqual_DifferentLength(t *testing.T) {
	if endpointPairSetEqual([]string{"a:b"}, []string{"a:b", "c:d"}) {
		t.Error("expected false")
	}
}

func TestEndpointPairSetEqual_DifferentContent(t *testing.T) {
	if endpointPairSetEqual([]string{"a:b"}, []string{"a:x"}) {
		t.Error("expected false")
	}
}

func TestEndpointPairSetEqual_BothEmpty(t *testing.T) {
	if !endpointPairSetEqual(nil, nil) {
		t.Error("expected true for both nil")
	}
}

// --- offerURLFromIdentity ---

func TestOfferURLFromIdentity_Normal(t *testing.T) {
	got := offerURLFromIdentity(map[string]any{"id": "admin/model:offer"})
	if got != "admin/model:offer" {
		t.Errorf("got %q", got)
	}
}

func TestOfferURLFromIdentity_Nil(t *testing.T) {
	got := offerURLFromIdentity(nil)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestOfferURLFromIdentity_NoID(t *testing.T) {
	got := offerURLFromIdentity(map[string]any{"other": "value"})
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// --- Match empty inputs ---

func TestMatch_EmptyInputs(t *testing.T) {
	matched, unmatchedP, unmatchedL := Match(nil, nil, false)
	if len(matched) != 0 {
		t.Errorf("matched: got %d, want 0", len(matched))
	}
	if len(unmatchedP) != 0 {
		t.Errorf("unmatchedPlanned: got %d, want 0", len(unmatchedP))
	}
	if len(unmatchedL) != 0 {
		t.Errorf("unmatchedLive: got %d, want 0", len(unmatchedL))
	}
}

func TestMatch_NoLive(t *testing.T) {
	planned := []PlannedResource{
		{Address: "juju_application.app", Type: "juju_application", PlannedName: "app"},
	}
	matched, unmatchedP, unmatchedL := Match(nil, planned, false)
	if len(matched) != 0 {
		t.Errorf("matched: got %d, want 0", len(matched))
	}
	if len(unmatchedP) != 1 {
		t.Errorf("unmatchedPlanned: got %d, want 1", len(unmatchedP))
	}
	if len(unmatchedL) != 0 {
		t.Errorf("unmatchedLive: got %d, want 0", len(unmatchedL))
	}
}

func TestMatch_NoPlanned(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "juju_application", DisplayName: "app"},
	}
	matched, unmatchedP, unmatchedL := Match(live, nil, false)
	if len(matched) != 0 {
		t.Errorf("matched: got %d, want 0", len(matched))
	}
	if len(unmatchedP) != 0 {
		t.Errorf("unmatchedPlanned: got %d, want 0", len(unmatchedP))
	}
	if len(unmatchedL) != 1 {
		t.Errorf("unmatchedLive: got %d, want 1", len(unmatchedL))
	}
}

// --- extractIntegrationEndpointPairs ---

func TestExtractIntegrationEndpointPairs_Normal(t *testing.T) {
	attrs := map[string]any{
		"application": []any{
			map[string]any{"name": "app1", "endpoint": "ep1"},
			map[string]any{"name": "app2", "endpoint": "ep2"},
		},
	}
	got := extractIntegrationEndpointPairs(attrs)
	want := []string{"app1:ep1", "app2:ep2"}
	if len(got) != len(want) {
		t.Fatalf("got %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("index %d: got %q, want %q", i, got[i], w)
		}
	}
}

func TestExtractIntegrationEndpointPairs_NoApplication(t *testing.T) {
	got := extractIntegrationEndpointPairs(map[string]any{"other": "x"})
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestExtractIntegrationEndpointPairs_EmptyApps(t *testing.T) {
	got := extractIntegrationEndpointPairs(map[string]any{"application": []any{}})
	if len(got) != 0 {
		t.Errorf("got %d, want 0", len(got))
	}
}

// --- parseIntegrationIDEndpointPairs ---

func TestParseIntegrationIDEndpointPairs_Normal(t *testing.T) {
	identity := map[string]any{"id": "uuid:app1:ep1:app2:ep2"}
	got := parseIntegrationIDEndpointPairs(identity)
	want := []string{"app1:ep1", "app2:ep2"}
	if len(got) != len(want) {
		t.Fatalf("got %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("index %d: got %q, want %q", i, got[i], w)
		}
	}
}

func TestParseIntegrationIDEndpointPairs_NilIdentity(t *testing.T) {
	got := parseIntegrationIDEndpointPairs(nil)
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestParseIntegrationIDEndpointPairs_WrongParts(t *testing.T) {
	got := parseIntegrationIDEndpointPairs(map[string]any{"id": "only:three"})
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}
