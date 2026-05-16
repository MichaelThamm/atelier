package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	tfjson "github.com/hashicorp/terraform-json"
)

// stubPlanner is a Planner implementation that returns canned results, used
// by the state-transition tests below. The tests never depend on real
// terraform or filesystem state.
type stubPlanner struct {
	initErr error
	planErr error
	plan    *tfjson.Plan

	initCalled bool
	planCalled bool
}

func (s *stubPlanner) EnsureInit(ctx context.Context) error {
	s.initCalled = true
	return s.initErr
}
func (s *stubPlanner) Plan(ctx context.Context) (*tfjson.Plan, error) {
	s.planCalled = true
	if s.planErr != nil {
		return nil, s.planErr
	}
	return s.plan, nil
}

func samplePlan() *tfjson.Plan {
	return &tfjson.Plan{
		ResourceChanges: []*tfjson.ResourceChange{
			rc("module.x.juju_application.alertmanager", "module.x", "juju_application", "alertmanager", &tfjson.Change{
				Actions: []tfjson.Action{tfjson.ActionCreate},
				After:   map[string]any{"name": "alertmanager"},
			}),
			rc("module.x.juju_application.grafana", "module.x", "juju_application", "grafana", &tfjson.Change{
				Actions: []tfjson.Action{tfjson.ActionCreate},
				After:   map[string]any{"name": "grafana"},
			}),
		},
	}
}

func TestPressP_withoutPlanner_emitsErrorStatus(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	_, cmd := m.Update(key("p"))
	if m.planState != planLoading {
		t.Errorf("expected planLoading transition, got %v", m.planState)
	}
	if cmd == nil {
		t.Fatal("expected a tea.Cmd from P press")
	}
	// The cmd is a tea.Batch of (planErrorMsg-producer, spinnerTick); unpack
	// it and dispatch only the planErrorMsg.
	msg := runBatchUntil(t, cmd, func(msg tea.Msg) bool {
		_, ok := msg.(planErrorMsg)
		return ok
	})
	out, _ := m.Update(msg)
	mm := out.(*Model)
	if mm.planState != planIdle {
		t.Errorf("after error, planState = %v; want planIdle", mm.planState)
	}
	if !strings.Contains(mm.status, "plan failed") {
		t.Errorf("status = %q; expected plan-failed message", mm.status)
	}
}

func TestPressP_withPlanner_transitionsThroughLoadingToReady(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	stub := &stubPlanner{plan: samplePlan()}
	m.Planner = stub

	_, cmd := m.Update(key("P"))
	if m.planState != planLoading {
		t.Errorf("after P press, planState = %v; want planLoading", m.planState)
	}
	if cmd == nil {
		t.Fatal("expected a tea.Batch with plan + spinner")
	}

	// Execute the plan command — drive it manually since tests don't run
	// the tea runtime.
	planMsg := runBatchUntil(t, cmd, func(msg tea.Msg) bool {
		_, ok := msg.(planResultMsg)
		return ok
	})
	if planMsg == nil {
		t.Fatal("did not observe a planResultMsg")
	}
	out, _ := m.Update(planMsg)
	mm := out.(*Model)
	if mm.planState != planReady {
		t.Fatalf("after planResultMsg, planState = %v; want planReady", mm.planState)
	}
	if mm.plan == nil || mm.planTree == nil {
		t.Errorf("plan/tree should be populated")
	}
	if !stub.initCalled || !stub.planCalled {
		t.Errorf("EnsureInit and Plan should both have been called")
	}
}

func TestPlanError_propagatesToStatus(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Planner = &stubPlanner{planErr: errors.New("BoOm")}

	_, cmd := m.Update(key("p"))
	msg := runBatchUntil(t, cmd, func(msg tea.Msg) bool {
		_, ok := msg.(planErrorMsg)
		return ok
	})
	out, _ := m.Update(msg)
	mm := out.(*Model)
	if mm.planState != planIdle {
		t.Errorf("planState after error = %v", mm.planState)
	}
	if !strings.Contains(mm.status, "BoOm") {
		t.Errorf("status should contain error text; got %q", mm.status)
	}
	if mm.statusLvl != statusError {
		t.Errorf("statusLvl should be error")
	}
}

func TestPlanMode_arrowKeys_moveCursor(t *testing.T) {
	m := plannedReady(t)
	rows := flattenedRows(m.planTree)
	if len(rows) < 2 {
		t.Fatalf("expected ≥2 rows for navigation test, got %d", len(rows))
	}
	// Initially at row 0.
	if m.planCursor != 0 {
		t.Errorf("initial cursor = %d", m.planCursor)
	}
	out, _ := m.Update(key("down"))
	if out.(*Model).planCursor != 1 {
		t.Errorf("after down, cursor = %d", out.(*Model).planCursor)
	}
	out, _ = out.Update(key("up"))
	if out.(*Model).planCursor != 0 {
		t.Errorf("after up, cursor back to 0; got %d", out.(*Model).planCursor)
	}
}

func TestPlanMode_enterTogglesCollapse(t *testing.T) {
	m := plannedReady(t)
	rows := flattenedRows(m.planTree)
	// First row is the module node, which is expanded by default.
	if rows[0].Node.Kind != nodeModule {
		t.Fatalf("first row not module: %+v", rows[0])
	}
	if rows[0].Node.Collapsed {
		t.Fatal("module starts collapsed; expected expanded")
	}
	out, _ := m.Update(key("enter"))
	if !rows[0].Node.Collapsed {
		t.Errorf("enter should toggle module to collapsed")
	}
	if c := out.(*Model).planCursor; c != 0 {
		t.Errorf("toggle should not move cursor; got %d", c)
	}
}

func TestPlanMode_esc_returnsToEditor(t *testing.T) {
	m := plannedReady(t)
	out, _ := m.Update(key("esc"))
	mm := out.(*Model)
	if mm.planState != planIdle {
		t.Errorf("after Esc, planState = %v; want planIdle", mm.planState)
	}
	// Plan and tree are preserved (re-pressing P refreshes; user can return).
	if mm.plan == nil {
		t.Errorf("plan should be retained after Esc, got nil")
	}
}

func TestPlanMode_p_triggersRePlan(t *testing.T) {
	m := plannedReady(t)
	_, cmd := m.Update(key("P"))
	if cmd == nil {
		t.Fatal("expected a re-plan cmd")
	}
	if m.planState != planLoading {
		t.Errorf("re-plan should transition back to loading; got %v", m.planState)
	}
}

func TestSpinnerTick_advancesFrameWhileLoading(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.planState = planLoading
	startFrame := m.planSpinnerFrame
	out, _ := m.Update(spinnerTickMsg{})
	if out.(*Model).planSpinnerFrame == startFrame {
		t.Errorf("spinner frame did not advance during loading")
	}
}

func TestSpinnerTick_ignoredWhenIdle(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	startFrame := m.planSpinnerFrame
	out, cmd := m.Update(spinnerTickMsg{})
	if out.(*Model).planSpinnerFrame != startFrame {
		t.Errorf("idle spinner tick should not advance frame")
	}
	if cmd != nil {
		t.Errorf("idle spinner tick should not reschedule")
	}
}

func TestSelectedPlanChange_returnsLeafOnly(t *testing.T) {
	m := plannedReady(t)
	// First row is the module — non-leaf, so SelectedPlanChange is nil.
	if m.SelectedPlanChange() != nil {
		t.Errorf("module row should not yield a Change")
	}
	// Step down past module + type to first resource. The type bucket is
	// collapsed by default; expand it first.
	rows := flattenedRows(m.planTree)
	// rows[0]: module; rows[1]: type. Expand the type.
	rows[1].Node.Collapsed = false
	rows = flattenedRows(m.planTree)
	if len(rows) < 3 {
		t.Fatalf("after expand, rows = %d", len(rows))
	}
	m.planCursor = 2 // first resource
	rc := m.SelectedPlanChange()
	if rc == nil {
		t.Fatal("expected non-nil Change for resource row")
	}
	if rc.Type != "juju_application" {
		t.Errorf("got resource type %q", rc.Type)
	}
}

func TestPlanScreen_render_includesSummaryAndFirstResource(t *testing.T) {
	m := plannedReady(t)
	out := stripANSI(m.renderPlanScreen())
	if !strings.Contains(out, "Plan: 2 to add") {
		t.Errorf("summary missing; got:\n%s", out)
	}
	if !strings.Contains(out, "module.x") {
		t.Errorf("module label missing; got:\n%s", out)
	}
}

// --- helpers ---

func plannedReady(t *testing.T) *Model {
	t.Helper()
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Planner = &stubPlanner{plan: samplePlan()}
	_, cmd := m.Update(key("p"))
	msg := runBatchUntil(t, cmd, func(msg tea.Msg) bool {
		_, ok := msg.(planResultMsg)
		return ok
	})
	out, _ := m.Update(msg)
	if out.(*Model).planState != planReady {
		t.Fatalf("setup: failed to reach planReady; state=%v", out.(*Model).planState)
	}
	return out.(*Model)
}

// runBatchUntil drives a tea.Cmd, dispatching its produced messages
// recursively into a queue and returning the first message that matches the
// predicate. Spinner ticks (which would be infinite) are dropped.
func runBatchUntil(t *testing.T, cmd tea.Cmd, want func(tea.Msg) bool) tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if _, ok := msg.(spinnerTickMsg); ok {
			continue // ignore — would tick forever
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				queue = append(queue, sub)
			}
			continue
		}
		if want(msg) {
			return msg
		}
	}
	t.Fatalf("expected matching message; queue drained without match")
	return nil
}
