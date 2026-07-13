package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	tfjson "github.com/hashicorp/terraform-json"
)

// checkResult builds a CheckResultStatic for a `check` block with the given
// display address, status, and problem messages.
func checkResult(display string, status tfjson.CheckStatus, msgs ...string) tfjson.CheckResultStatic {
	problems := make([]tfjson.CheckResultProblem, 0, len(msgs))
	for _, m := range msgs {
		problems = append(problems, tfjson.CheckResultProblem{Message: m})
	}
	return tfjson.CheckResultStatic{
		Address: tfjson.CheckStaticAddress{
			ToDisplay: display,
			Kind:      tfjson.CheckKindCheckBlock,
			Name:      strings.TrimPrefix(display, "check."),
		},
		Status: status,
		Instances: []tfjson.CheckResultDynamic{
			{Status: status, Problems: problems},
		},
	}
}

func TestFailedChecks_nilPlan(t *testing.T) {
	if got := FailedChecks(nil); got != nil {
		t.Errorf("FailedChecks(nil) = %v; want nil", got)
	}
}

func TestFailedChecks_onlyFailedCheckBlocks(t *testing.T) {
	plan := &tfjson.Plan{
		Checks: []tfjson.CheckResultStatic{
			checkResult("check.grafana_storage_directives", tfjson.CheckStatusFail,
				"grafana.storage_directives is unset, so it will use the default 1G volume."),
			// A passing check must be skipped.
			checkResult("check.loki_storage_directives", tfjson.CheckStatusPass, "should not appear"),
			// A resource pre/post-condition (not a check block) must be skipped
			// even when it failed — those fail the plan itself.
			{
				Address: tfjson.CheckStaticAddress{
					ToDisplay: "juju_application.grafana",
					Kind:      tfjson.CheckKindResource,
				},
				Status: tfjson.CheckStatusFail,
				Instances: []tfjson.CheckResultDynamic{
					{Status: tfjson.CheckStatusFail, Problems: []tfjson.CheckResultProblem{{Message: "resource cond"}}},
				},
			},
			checkResult("check.prometheus_storage_directives", tfjson.CheckStatusFail,
				"prometheus.storage_directives is unset."),
		},
	}

	got := FailedChecks(plan)
	if len(got) != 2 {
		t.Fatalf("FailedChecks returned %d warnings; want 2\n%+v", len(got), got)
	}
	if got[0].Address != "check.grafana_storage_directives" {
		t.Errorf("first address = %q", got[0].Address)
	}
	if !strings.Contains(got[0].Message, "default 1G volume") {
		t.Errorf("first message = %q", got[0].Message)
	}
	if got[1].Address != "check.prometheus_storage_directives" {
		t.Errorf("second address = %q", got[1].Address)
	}
}

func TestFailedChecks_multipleProblemsPerCheck(t *testing.T) {
	plan := &tfjson.Plan{
		Checks: []tfjson.CheckResultStatic{
			checkResult("check.multi", tfjson.CheckStatusFail, "first", "second"),
		},
	}
	got := FailedChecks(plan)
	if len(got) != 2 {
		t.Fatalf("expected 2 warnings (one per problem); got %d", len(got))
	}
	if got[0].Message != "first" || got[1].Message != "second" {
		t.Errorf("messages = %q, %q", got[0].Message, got[1].Message)
	}
}

func TestFailedChecks_failWithNoMessageStillSurfaces(t *testing.T) {
	plan := &tfjson.Plan{
		Checks: []tfjson.CheckResultStatic{
			checkResult("check.silent", tfjson.CheckStatusFail),
		},
	}
	got := FailedChecks(plan)
	if len(got) != 1 {
		t.Fatalf("expected the address to surface even with no message; got %d", len(got))
	}
	if got[0].Address != "check.silent" || got[0].Message != "" {
		t.Errorf("got %+v", got[0])
	}
}

func TestFailedChecks_fallsBackToNameWhenNoDisplay(t *testing.T) {
	plan := &tfjson.Plan{
		Checks: []tfjson.CheckResultStatic{
			{
				Address: tfjson.CheckStaticAddress{Kind: tfjson.CheckKindCheckBlock, Name: "bare"},
				Status:  tfjson.CheckStatusFail,
				Instances: []tfjson.CheckResultDynamic{
					{Status: tfjson.CheckStatusFail, Problems: []tfjson.CheckResultProblem{{Message: "m"}}},
				},
			},
		},
	}
	got := FailedChecks(plan)
	if len(got) != 1 || got[0].Address != "check.bare" {
		t.Fatalf("expected synthesized address 'check.bare'; got %+v", got)
	}
}

func TestFormatCheckWarnings(t *testing.T) {
	out := formatCheckWarnings([]CheckWarning{
		{Address: "check.a", Message: "message a"},
		{Address: "check.b", Message: "message b"},
	})
	if !strings.Contains(out, "Warning: check.a") {
		t.Errorf("missing first address; got:\n%s", out)
	}
	if !strings.Contains(out, "message a") || !strings.Contains(out, "message b") {
		t.Errorf("missing messages; got:\n%s", out)
	}
	if formatCheckWarnings(nil) != "" {
		t.Error("formatCheckWarnings(nil) should be empty")
	}
}

// samplePlanWithChecks is samplePlan plus a failed check block, for the
// end-to-end model tests.
func samplePlanWithChecks() *tfjson.Plan {
	p := samplePlan()
	p.Checks = []tfjson.CheckResultStatic{
		checkResult("check.grafana_storage_directives", tfjson.CheckStatusFail,
			"grafana.storage_directives is unset, so it will use the default 1G volume."),
	}
	return p
}

// plannedReadyWithChecks drives a plan that reports a failed check block and
// returns the model in planReady state.
func plannedReadyWithChecks(t *testing.T) *Model {
	t.Helper()
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Planner = &stubPlanner{plan: samplePlanWithChecks()}
	_, cmd := m.Update(key("p"))
	msg := runBatchUntil(t, cmd, func(msg tea.Msg) bool {
		_, ok := msg.(planResultMsg)
		return ok
	})
	out, _ := m.Update(msg)
	mm := out.(*Model)
	if mm.planState != planReady {
		t.Fatalf("setup: failed to reach planReady; state=%v", mm.planState)
	}
	return mm
}

func TestPlanResult_populatesCheckWarnings(t *testing.T) {
	m := plannedReadyWithChecks(t)
	if len(m.checkWarnings) != 1 {
		t.Fatalf("expected 1 check warning; got %d", len(m.checkWarnings))
	}
	if !strings.Contains(m.checkWarnings[0].Message, "default 1G volume") {
		t.Errorf("warning message = %q", m.checkWarnings[0].Message)
	}
}

func TestPlanScreen_showsCheckWarningBanner(t *testing.T) {
	m := plannedReadyWithChecks(t)
	out := stripANSI(m.renderPlanScreen())
	if !strings.Contains(out, "1 check warning(s)") {
		t.Errorf("plan screen missing warning banner; got:\n%s", out)
	}
	if !strings.Contains(out, "[W]") {
		t.Errorf("plan screen banner missing [W] hint; got:\n%s", out)
	}
}

func TestPlanMode_pressW_opensWarningModal(t *testing.T) {
	m := plannedReadyWithChecks(t)
	out, _ := m.Update(key("W"))
	mm := out.(*Model)
	if !mm.warnDetail {
		t.Fatal("W should open the warnings detail modal")
	}
	view := stripANSI(mm.View())
	if !strings.Contains(view, "Warning: check.grafana_storage_directives") {
		t.Errorf("modal missing warning address; got:\n%s", view)
	}
	if !strings.Contains(view, "default 1G volume") {
		t.Errorf("modal missing warning message; got:\n%s", view)
	}

	// Esc closes it.
	out2, _ := mm.Update(key("esc"))
	if out2.(*Model).warnDetail {
		t.Error("Esc should close the warnings modal")
	}
}

func TestPlanMode_pressW_noopWithoutWarnings(t *testing.T) {
	m := plannedReady(t) // samplePlan has no checks
	out, _ := m.Update(key("W"))
	if out.(*Model).warnDetail {
		t.Error("W should be a no-op when there are no check warnings")
	}
}

func TestHeader_showsCheckWarningChip(t *testing.T) {
	m := plannedReadyWithChecks(t)
	header := stripANSI(m.renderHeader())
	if !strings.Contains(header, "1 check warning(s)") {
		t.Errorf("header missing warning chip; got: %q", header)
	}
}

func TestApply_clearsCheckWarnings(t *testing.T) {
	m := plannedReadyWithChecks(t)
	m.Applier = &stubApplier{}
	_, cmd := m.Update(key("A"))
	msg := runBatchUntil(t, cmd, func(msg tea.Msg) bool {
		_, ok := msg.(applyResultMsg)
		return ok
	})
	out, _ := m.Update(msg)
	if got := out.(*Model).checkWarnings; got != nil {
		t.Errorf("apply should clear check warnings; got %v", got)
	}
}

// Compile-time assertion that stubPlanner still satisfies Planner with the
// checks-carrying plan (guards against interface drift).
var _ Planner = (*stubPlanner)(nil)

func TestFailedChecks_ignoresUnknownAndErrorStatus(t *testing.T) {
	plan := &tfjson.Plan{
		Checks: []tfjson.CheckResultStatic{
			checkResult("check.unknown", tfjson.CheckStatusUnknown, "u"),
			checkResult("check.errored", tfjson.CheckStatusError, "e"),
		},
	}
	if got := FailedChecks(plan); len(got) != 0 {
		t.Errorf("only 'fail' status should surface; got %+v", got)
	}
}
