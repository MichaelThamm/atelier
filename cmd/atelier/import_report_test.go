package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/importer"
)

// The import report must not tell a user to "import manually" a resource that
// does not exist live — that was the bug: 53 planned creates were listed as
// "zero or ambiguous live matches" alongside the resources that were genuinely
// ambiguous or already tracked. Each category now reads for what it is.
func TestReportUnmatchedPlanned_SplitsCategories(t *testing.T) {
	res := &importer.Result{
		UnmatchedPlanned: []importer.PlannedResource{
			{Address: "module.cos.module.loki.juju_integration.a", Type: "juju_integration", Module: "module.cos.module.loki", Create: true},
			{Address: "module.cos.module.loki.juju_integration.b", Type: "juju_integration", Module: "module.cos.module.loki", Create: true},
			{Address: "module.cos.juju_model.cos[0]", Type: "juju_model", Module: "module.cos"},
			{Address: "module.cos.juju_offer.ambiguous", Type: "juju_offer", Module: "module.cos", Create: true, LiveCandidates: 2},
		},
	}
	var buf bytes.Buffer
	reportUnmatchedPlanned(&buf, res, false)
	got := buf.String()
	for _, want := range []string{
		"Unmatched module resources (no single live object identified): 4",
		"Will be created on apply",
		"module.cos.module.loki",
		"Already in state",
		"module.cos.juju_model.cos[0]",
		"Matched more than one live object",
		"module.cos.juju_offer.ambiguous",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "import these manually if needed") {
		t.Errorf("stale one-size-fits-all advice still present:\n%s", got)
	}
}

// The compact report collapses a whole absent subtree to one module line; a
// verbose run lists every address instead.
func TestReportUnmatchedPlanned_GroupsByModuleAndVerboseExpands(t *testing.T) {
	res := &importer.Result{
		UnmatchedPlanned: []importer.PlannedResource{
			{Address: "module.cos.module.loki.juju_integration.a", Type: "juju_integration", Module: "module.cos.module.loki", Create: true},
			{Address: "module.cos.module.loki.juju_integration.b", Type: "juju_integration", Module: "module.cos.module.loki", Create: true},
			{Address: "module.cos.module.tempo.juju_integration.c", Type: "juju_integration", Module: "module.cos.module.tempo", Create: true},
		},
	}

	var compact bytes.Buffer
	reportUnmatchedPlanned(&compact, res, false)
	if !strings.Contains(compact.String(), "module.cos.module.loki") {
		t.Errorf("compact report should name the module subtree:\n%s", compact.String())
	}
	if strings.Contains(compact.String(), "juju_integration.a") {
		t.Errorf("compact report should not list every address:\n%s", compact.String())
	}

	var verbose bytes.Buffer
	reportUnmatchedPlanned(&verbose, res, true)
	for _, addr := range []string{
		"module.cos.module.loki.juju_integration.a",
		"module.cos.module.loki.juju_integration.b",
		"module.cos.module.tempo.juju_integration.c",
	} {
		if !strings.Contains(verbose.String(), addr) {
			t.Errorf("verbose report missing %q:\n%s", addr, verbose.String())
		}
	}
}

func TestReportUnmatchedPlanned_NothingToSay(t *testing.T) {
	var buf bytes.Buffer
	reportUnmatchedPlanned(&buf, &importer.Result{}, false)
	if buf.Len() != 0 {
		t.Errorf("empty result should print nothing, got:\n%s", buf.String())
	}
}

// "Nothing to import" must not hide a plan that still adds resources.
func TestReportPlanBeforeImport_ReportsAdds(t *testing.T) {
	res := &importer.Result{Plan: &importer.PlanSummary{Add: 53, Change: 0, Destroy: 0}}
	var buf bytes.Buffer
	reportPlanBeforeImport(&buf, res)
	want := "Plan before import: 53 to add, 0 to change, 0 to destroy."
	if !strings.Contains(buf.String(), want) {
		t.Errorf("got:\n%s\nwant %q", buf.String(), want)
	}
}

func TestReportPlanBeforeImport_NotComputed(t *testing.T) {
	var buf bytes.Buffer
	reportPlanBeforeImport(&buf, &importer.Result{})
	if buf.Len() != 0 {
		t.Errorf("no plan should print nothing, got:\n%s", buf.String())
	}
}
