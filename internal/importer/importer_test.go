package importer

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
)

// stepFunc adapts a function to PostImportStep.
type stepFunc func(ctx context.Context, pctx PostImportContext) error

func (f stepFunc) Name() string                                       { return "test step" }
func (f stepFunc) Run(ctx context.Context, p PostImportContext) error { return f(ctx, p) }

// The drift report must describe the state the steps left, not the one they
// read. NullEmptyNormalization asks for the plan to find null/empty attribute
// drift and then rewrites state to remove it, so the plan the steps share is
// stale the moment that step returns. Reporting it reports the drift it was
// asked to fix — which is what a CI job gating on this payload would fail on.
func TestPostImportPhase_reportsThePlanTakenAfterTheSteps(t *testing.T) {
	dirty := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		change("module.m.juju_offer.o", "juju_offer", tfjson.Actions{tfjson.ActionUpdate}, false),
	}}
	clean := &tfjson.Plan{}

	plans := 0
	got, err := postImportPhase{
		Dir:      t.TempDir(),
		Report:   true,
		Stderr:   io.Discard,
		Imported: []ImportResult{{Address: "module.m.juju_offer.o"}},
		// Stands in for NullEmptyNormalization: reads the shared plan, then fixes
		// what it saw, so the plan it read no longer matches the state.
		Steps: []PostImportStep{stepFunc(func(_ context.Context, pctx PostImportContext) error {
			_, err := pctx.Plan()
			return err
		})},
		Plan: func() (*PlanResult, error) {
			plans++
			if plans == 1 {
				return &PlanResult{Plan: dirty}, nil
			}
			return &PlanResult{Plan: clean}, nil
		},
	}.run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got == nil {
		t.Fatal("got nil summary, want one")
	}
	if got.Change != 0 || len(got.ChangeAddresses) != 0 {
		t.Errorf("reported drift the step had already fixed: %+v", got)
	}
	if plans != 2 {
		t.Errorf("plans run = %d, want 2: one for the step, one after it", plans)
	}
}

// The steps share one plan, so a run whose steps never ask pays for no plan at
// all, and a second step asking must not trigger a second one.
func TestPostImportPhase_sharesOnePlanBetweenSteps(t *testing.T) {
	plans := 0
	phase := postImportPhase{
		Dir:      t.TempDir(),
		Stderr:   io.Discard,
		Imported: []ImportResult{{Address: "module.m.juju_offer.o"}},
		Steps: []PostImportStep{
			stepFunc(func(_ context.Context, pctx PostImportContext) error {
				_, err := pctx.Plan()
				return err
			}),
			stepFunc(func(_ context.Context, pctx PostImportContext) error {
				_, err := pctx.Plan()
				return err
			}),
		},
		Plan: func() (*PlanResult, error) {
			plans++
			return &PlanResult{Plan: &tfjson.Plan{}}, nil
		},
	}
	// Report is false, so the summary is nil and no second plan is taken.
	if _, err := phase.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if plans != 1 {
		t.Errorf("plans run = %d, want 1 shared between both steps", plans)
	}
}

// Nothing asked for the drift report, so no run should end up paying for the
// plan that would answer it.
func TestPostImportPhase_noReportRunsNoSecondPlan(t *testing.T) {
	plans := 0
	_, err := postImportPhase{
		Dir:      t.TempDir(),
		Report:   false,
		Stderr:   io.Discard,
		Imported: []ImportResult{{Address: "module.m.juju_offer.o"}},
		Steps: []PostImportStep{stepFunc(func(_ context.Context, pctx PostImportContext) error {
			_, err := pctx.Plan()
			return err
		})},
		Plan: func() (*PlanResult, error) {
			plans++
			return &PlanResult{Plan: &tfjson.Plan{}}, nil
		},
	}.run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if plans != 1 {
		t.Errorf("plans run = %d, want 1 (the step's), none for an unreported plan", plans)
	}
}

// A step that fails ends the phase. The plan is not taken for a report that has
// no valid state to report on.
func TestPostImportPhase_stepFailureSkipsTheReport(t *testing.T) {
	plans := 0
	var stderr strings.Builder
	got, err := postImportPhase{
		Dir:      t.TempDir(),
		Report:   true,
		Stderr:   &stderr,
		Imported: []ImportResult{{Address: "module.m.juju_offer.o"}},
		Steps: []PostImportStep{stepFunc(func(context.Context, PostImportContext) error {
			return errors.New("normalize offer defaults: no such file")
		})},
		Plan: func() (*PlanResult, error) {
			plans++
			return &PlanResult{Plan: &tfjson.Plan{}}, nil
		},
	}.run(context.Background())
	if err == nil {
		t.Fatal("got nil error, want the step's failure")
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Errorf("error = %q, want it to carry the step's cause", err)
	}
	if got != nil {
		t.Errorf("summary = %+v, want nil alongside the error", got)
	}
	if plans != 0 {
		t.Errorf("plans run = %d, want 0", plans)
	}
}
