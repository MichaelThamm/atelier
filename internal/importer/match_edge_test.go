package importer

import (
	"testing"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/MichaelThamm/atelier/internal/tfexec"
)

// --- PlannedCreates edge cases ---

func TestPlannedCreates_NilPlan(t *testing.T) {
	got := PlannedCreates(nil, false)
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestPlannedCreates_NilPlan_IncludeExisting(t *testing.T) {
	got := PlannedCreates(nil, true)
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
	creates := PlannedCreates(plan, false)
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
	creates := PlannedCreates(plan, false)
	if len(creates) != 0 {
		t.Errorf("got %d, want 0", len(creates))
	}
}

func TestPlannedCreates_SkipsImporting(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		{Address: "juju_application.importing", Type: "juju_application",
			Change: &tfjson.Change{
				Actions:   tfjson.Actions{tfjson.ActionCreate},
				Importing: &tfjson.Importing{ID: "some-existing-id"},
			}},
		{Address: "juju_application.normal", Type: "juju_application",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}}},
	}}
	creates := PlannedCreates(plan, false)
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
	creates := PlannedCreates(plan, false)
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
	creates := PlannedCreates(plan, false)
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
				Actions:       tfjson.Actions{tfjson.ActionCreate},
				After:         map[string]any{"name": "app"},
				AfterIdentity: map[string]any{"id": "uuid-123"},
			}},
	}}
	creates := PlannedCreates(plan, false)
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
		// Indexed addresses: the count index and for_each key must not survive,
		// or a resource already in state can never match a live display name.
		{"module.cos.juju_model.cos[0]", "cos"},
		{`module.cos.juju_integration.alerting["loki"]`, "alerting"},
		{`module.cos.juju_integration.alerting["a.b"]`, "alerting"},
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

// --- PlannedCreates includeExisting ---

func TestPlannedCreates_IncludeExisting_IncludesNoOp(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		{Address: "module.cos.juju_application.alertmanager", Type: "juju_application",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}}},
		{Address: "module.cos.juju_application.already_there", Type: "juju_application",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionNoop},
				After: map[string]any{"name": "already_there"}}},
		{Address: "module.cos.juju_model.cos", Type: "juju_model",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}}},
	}}
	creates := PlannedCreates(plan, true)
	if len(creates) != 3 {
		t.Fatalf("expected 3 (all module resources), got %d: %v", len(creates), creates)
	}
	// Verify the no-op resource is included with its attributes.
	found := false
	for _, c := range creates {
		if c.Address == "module.cos.juju_application.already_there" {
			found = true
			if c.PlannedName != "already_there" {
				t.Errorf("expected PlannedName=already_there, got %q", c.PlannedName)
			}
		}
	}
	if !found {
		t.Error("no-op resource not found in includeExisting results")
	}
}

func TestPlannedCreates_IncludeExisting_False_SkipsNoOp(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		{Address: "module.cos.juju_application.alertmanager", Type: "juju_application",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}}},
		{Address: "module.cos.juju_application.already_there", Type: "juju_application",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionNoop}}},
	}}
	creates := PlannedCreates(plan, false)
	if len(creates) != 1 {
		t.Fatalf("expected 1 (creates only), got %d: %v", len(creates), creates)
	}
	if creates[0].Address != "module.cos.juju_application.alertmanager" {
		t.Errorf("unexpected address: %s", creates[0].Address)
	}
}

func TestPlannedCreates_IncludeExisting_SkipsUnimportableInBothModes(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		{Address: "module.cos.juju_application.alertmanager", Type: "juju_application",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}}},
		{Address: "module.cos.terraform_data.replace", Type: "terraform_data",
			Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionNoop}}},
	}}
	creates := PlannedCreates(plan, true)
	if len(creates) != 1 {
		t.Fatalf("expected 1 (terraform_data filtered even with includeExisting), got %d: %v", len(creates), creates)
	}
}

// --- Match empty inputs ---

func TestMatch_EmptyInputs(t *testing.T) {
	matched, unmatchedP, unmatchedL := Match(nil, nil, nil, false)
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
	matched, unmatchedP, unmatchedL := Match(nil, planned, nil, false)
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
	matched, unmatchedP, unmatchedL := Match(live, nil, nil, false)
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

// An unmatched resource must record whether a live counterpart was absent
// entirely (0 — nothing to import) or ambiguous (>1 — needs a manual choice),
// because the report treats those two very differently.
func TestMatch_RecordsCandidateCount(t *testing.T) {
	planned := []PlannedResource{
		{Address: "juju_application.no_live", Type: "juju_application", PlannedName: "no_live"},
		{Address: "juju_application.ambiguous", Type: "juju_application", PlannedName: "ambiguous"},
	}
	live := []tfexec.LiveResource{
		{ResourceType: "juju_application", DisplayName: "ambiguous"},
		{ResourceType: "juju_application", DisplayName: "ambiguous"},
	}
	matched, unmatchedP, _ := Match(live, planned, nil, false)
	if len(matched) != 0 {
		t.Fatalf("matched: got %d, want 0", len(matched))
	}
	got := map[string]int{}
	for _, p := range unmatchedP {
		got[p.Address] = p.LiveCandidates
	}
	if got["juju_application.no_live"] != 0 {
		t.Errorf("no_live LiveCandidates = %d, want 0", got["juju_application.no_live"])
	}
	if got["juju_application.ambiguous"] != 2 {
		t.Errorf("ambiguous LiveCandidates = %d, want 2", got["juju_application.ambiguous"])
	}
}

// markPlannedCreates is what lets the report say "apply will create this"
// rather than "import it manually": only addresses present in the create-only
// plan get the flag.
func TestMarkPlannedCreates(t *testing.T) {
	unmatched := []PlannedResource{
		{Address: "module.cos.juju_offer.loki_logging"},
		{Address: "module.cos.juju_model.cos[0]"},
	}
	creates := []PlannedResource{{Address: "module.cos.juju_offer.loki_logging"}}
	got := markPlannedCreates(unmatched, creates)
	if !got[0].Create {
		t.Error("offer should be flagged as a create")
	}
	if got[1].Create {
		t.Error("already-tracked model must not be flagged as a create")
	}
}
