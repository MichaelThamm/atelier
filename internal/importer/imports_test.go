package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/MichaelThamm/atelier/internal/tfexec"
)

func TestRenderImportsFile(t *testing.T) {
	ids := map[string]string{
		`module.cos_lite.juju_integration.alerting["loki"]`:       "uuid:alertmanager:alerting:loki:alertmanager",
		"module.cos_lite.module.grafana.juju_application.grafana": "uuid:grafana",
	}
	got := string(RenderImportsFile(ids, "test note"))

	// Addresses must be raw traversals (unquoted), including index keys.
	if !strings.Contains(got, `to = module.cos_lite.juju_integration.alerting["loki"]`) {
		t.Errorf("indexed address not rendered as a raw traversal:\n%s", got)
	}
	// IDs must be quoted strings.
	if !strings.Contains(got, `id = "uuid:grafana"`) {
		t.Errorf("id not rendered as a quoted string:\n%s", got)
	}
	if !strings.Contains(got, "# test note") {
		t.Errorf("note not emitted:\n%s", got)
	}
	// The apply hazard must be stated in the artifact itself, since the file
	// can be read (and applied) long after the run that produced it.
	if !strings.Contains(got, "duplicating live") {
		t.Errorf("artifact does not warn about the apply hazard:\n%s", got)
	}
	if n := strings.Count(got, "import {"); n != 2 {
		t.Errorf("got %d import blocks, want 2:\n%s", n, got)
	}
}

// Blocks must be emitted in a stable order so the artifact diffs cleanly.
func TestRenderImportsFileIsSorted(t *testing.T) {
	ids := map[string]string{"b.z": "2", "a.y": "1", "c.x": "3"}
	got := string(RenderImportsFile(ids))
	ia, ib, ic := strings.Index(got, "a.y"), strings.Index(got, "b.z"), strings.Index(got, "c.x")
	if !(ia < ib && ib < ic) {
		t.Errorf("blocks not sorted by address:\n%s", got)
	}
}

func change(addr, typ string, actions tfjson.Actions, importing bool) *tfjson.ResourceChange {
	c := &tfjson.Change{Actions: actions}
	if importing {
		c.Importing = &tfjson.Importing{ID: "x"}
	}
	return &tfjson.ResourceChange{Address: addr, Type: typ, Change: c}
}

func TestSummarizePlan(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		// Imports may be reported as a no-op or an update alongside Importing.
		change("juju_application.a", "juju_application", tfjson.Actions{tfjson.ActionNoop}, true),
		change("juju_offer.b", "juju_offer", tfjson.Actions{tfjson.ActionUpdate}, true),
		// A real create: the import set missed this one.
		change("juju_application.c", "juju_application", tfjson.Actions{tfjson.ActionCreate}, false),
		// Terraform-internal: expected to be created, must not alarm.
		change("terraform_data.d", "terraform_data", tfjson.Actions{tfjson.ActionCreate}, false),
		change("juju_application.e", "juju_application", tfjson.Actions{tfjson.ActionUpdate}, false),
		change("juju_application.f", "juju_application", tfjson.Actions{tfjson.ActionDelete}, false),
	}}
	got := SummarizePlan(plan)
	if got.Import != 2 {
		t.Errorf("Import = %d, want 2", got.Import)
	}
	if got.Add != 2 {
		t.Errorf("Add = %d, want 2", got.Add)
	}
	if got.UnimportableAdds != 1 {
		t.Errorf("UnimportableAdds = %d, want 1", got.UnimportableAdds)
	}
	// Only the genuinely surprising create is surfaced to the user.
	if len(got.AddAddresses) != 1 || got.AddAddresses[0] != "juju_application.c" {
		t.Errorf("AddAddresses = %v, want [juju_application.c]", got.AddAddresses)
	}
	if got.Change != 1 {
		t.Errorf("Change = %d, want 1", got.Change)
	}
	if got.Destroy != 1 {
		t.Errorf("Destroy = %d, want 1", got.Destroy)
	}
}

func TestSummarizePlanNil(t *testing.T) {
	if got := SummarizePlan(nil); got.Import != 0 || got.Add != 0 {
		t.Errorf("nil plan should summarise to zero, got %+v", got)
	}
}

func TestVarArgs(t *testing.T) {
	got := varArgs(map[string]string{"model_uuid": "abc", "region": "eu"})
	want := " -var model_uuid=abc -var region=eu"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRemoveGeneratedImportsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultImportsFile)
	if err := os.WriteFile(path, RenderImportsFile(map[string]string{"a.b": "id"}), 0o644); err != nil {
		t.Fatal(err)
	}
	RemoveGeneratedImportsFile(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("generated imports.tf should have been removed")
	}
}

// A hand-written imports.tf must survive cleanup — Atelier only removes what
// it generated.
func TestRemoveGeneratedImportsFileLeavesUserFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultImportsFile)
	body := []byte("import {\n  to = juju_application.mine\n  id = \"uuid:mine\"\n}\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	RemoveGeneratedImportsFile(path)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("user-authored imports.tf was removed")
	}
	if string(got) != string(body) {
		t.Error("user-authored imports.tf was modified")
	}
}

func TestRemoveGeneratedImportsFileMissing(t *testing.T) {
	RemoveGeneratedImportsFile(filepath.Join(t.TempDir(), "nope.tf"))
}

// A generated imports.tf must be gone before the module is planned. With
// `import {}` blocks present Terraform reports those resources as Importing,
// and PlannedCreates skips Importing entries — so a stale artifact from an
// earlier --dry-run silently turns the next real run into a no-op. This pins
// the two halves of that interaction together.
func TestPlannedCreatesSkipsImportingSoStaleArtifactMustBeRemoved(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		change("juju_application.a", "juju_application", tfjson.Actions{tfjson.ActionCreate}, true),
		change("juju_application.b", "juju_application", tfjson.Actions{tfjson.ActionCreate}, false),
	}}
	creates := PlannedCreates(plan, false)
	if len(creates) != 1 || creates[0].Address != "juju_application.b" {
		t.Fatalf("PlannedCreates should skip Importing entries, got %v", creates)
	}

	// Therefore Generate must clear the artifact before planning.
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultImportsFile)
	if err := os.WriteFile(path, RenderImportsFile(map[string]string{"juju_application.a": "id"}), 0o644); err != nil {
		t.Fatal(err)
	}
	RemoveGeneratedImportsFile(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("stale generated artifact would poison the plan; it must be removed")
	}
}

// --- BuildImportIDs: the re-run contract ---

func TestBuildImportIDsOnlyImportsCreates(t *testing.T) {
	matched := []MatchedImport{
		{Address: "juju_application.a", ResourceType: "juju_application", Name: "a"},
		{Address: "juju_application.b", ResourceType: "juju_application", Name: "b"},
	}
	// Only `a` is a create; `b` is already in state.
	creates := []PlannedResource{{Address: "juju_application.a"}}
	build := func(m MatchedImport, _ map[string]string) string { return "id:" + m.Name }

	ids, unresolved := BuildImportIDs(matched, creates, build, nil)
	if len(ids) != 1 || ids["juju_application.a"] != "id:a" {
		t.Errorf("ids = %v, want only juju_application.a", ids)
	}
	// Already-in-state is not a problem, so it must not be reported as one.
	if len(unresolved) != 0 {
		t.Errorf("unresolved = %v, want empty", unresolved)
	}
}

// A second run over an already-imported deployment must be a clean no-op: same
// live objects, same matches, nothing left to create, nothing reported wrong.
// This is the "fix your vars and re-run" workflow.
func TestBuildImportIDsIsIdempotent(t *testing.T) {
	matched := []MatchedImport{
		{Address: "juju_application.a", ResourceType: "juju_application", Name: "a"},
		{Address: "juju_offer.b", ResourceType: "juju_offer", Name: "b"},
	}
	build := func(m MatchedImport, _ map[string]string) string { return "id:" + m.Name }

	// First run: everything is a create.
	creates := []PlannedResource{{Address: "juju_application.a"}, {Address: "juju_offer.b"}}
	ids, unresolved := BuildImportIDs(matched, creates, build, nil)
	if len(ids) != 2 || len(unresolved) != 0 {
		t.Fatalf("first run: ids=%v unresolved=%v", ids, unresolved)
	}

	// Second run: state now holds both, so the plan reports no creates.
	ids, unresolved = BuildImportIDs(matched, nil, build, nil)
	if len(ids) != 0 {
		t.Errorf("second run should import nothing, got %v", ids)
	}
	if len(unresolved) != 0 {
		t.Errorf("second run should report no problems, got %v", unresolved)
	}
}

// A create with no derivable import ID is the dangerous case — the module would
// create a resource that already exists — so it must surface, not vanish.
func TestBuildImportIDsReportsUnresolvedCreates(t *testing.T) {
	matched := []MatchedImport{{Address: "juju_application.a", ResourceType: "juju_application", Name: "a"}}
	creates := []PlannedResource{{Address: "juju_application.a"}}

	_, unresolved := BuildImportIDs(matched, creates, func(MatchedImport, map[string]string) string { return "" }, nil)
	if len(unresolved) != 1 || unresolved[0].Address != "juju_application.a" {
		t.Errorf("unresolved = %v, want [juju_application.a]", unresolved)
	}

	// Same when no provider builder is wired at all.
	_, unresolved = BuildImportIDs(matched, creates, nil, nil)
	if len(unresolved) != 1 {
		t.Errorf("nil builder: unresolved = %v, want 1 entry", unresolved)
	}
}

// --- GroupUnmatchedLive ---

func TestGroupUnmatchedLive(t *testing.T) {
	live := []tfexec.LiveResource{
		{ResourceType: "juju_secret", DisplayName: "admin-password"},
		{ResourceType: "juju_integration", DisplayName: "uuid:traefik:peers"},
		{ResourceType: "juju_secret", DisplayName: "active-ca-certificates"},
		{ResourceType: "juju_storage_pool", DisplayName: "kubernetes"},
		{ResourceType: "juju_integration", DisplayName: "uuid:grafana:replicas"},
	}
	got := GroupUnmatchedLive(live)
	if len(got) != 3 {
		t.Fatalf("got %d groups, want 3: %+v", len(got), got)
	}
	// Groups sorted by type.
	if got[0].Type != "juju_integration" || got[1].Type != "juju_secret" || got[2].Type != "juju_storage_pool" {
		t.Errorf("groups not sorted by type: %+v", got)
	}
	if got[1].Count != 2 {
		t.Errorf("juju_secret count = %d, want 2", got[1].Count)
	}
	// Names sorted within a group, so output is stable across runs.
	if got[1].Names[0] != "active-ca-certificates" || got[1].Names[1] != "admin-password" {
		t.Errorf("names not sorted: %v", got[1].Names)
	}
}

func TestGroupUnmatchedLiveEmpty(t *testing.T) {
	if got := GroupUnmatchedLive(nil); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}
