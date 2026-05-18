package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	tfjson "github.com/hashicorp/terraform-json"
	"github.com/muesli/termenv"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// The theme tests assert behaviours that the palette is meant to encode —
// specifically that *some* ANSI styling is emitted for the right roles, so
// a future "fix" that drops a style by accident shows up as a failed test.
//
// They deliberately do not assert specific hex codes; the palette is a
// design choice that's allowed to evolve.

func init() {
	// Force lipgloss to emit ANSI for tests regardless of $TERM in CI. In
	// a real terminal lipgloss auto-detects 24-bit colour support; here we
	// pin it on so the tests have something to assert against.
	lipgloss.SetColorProfile(termenv.TrueColor)
}

func TestTheme_actionMarkers_areColoured(t *testing.T) {
	// Each plan-action marker should render to a non-trivial styled
	// string. We only verify that the rendered output differs from the
	// raw text (i.e. *some* escape codes were emitted).
	cases := []string{"+", "~", "-", "↻"}
	for _, m := range cases {
		got := styledAction(m)
		if got == m {
			t.Errorf("action marker %q rendered without styling: %q", m, got)
		}
	}
}

func TestTheme_varMarkers_areColoured(t *testing.T) {
	required := tfvars.Variable{Name: "x", Type: mustParseType(t, "string")}
	state := &wrapper.State{
		Vars:   []tfvars.Variable{required},
		Values: map[string]cty.Value{},
	}
	// Required + unset → [!]
	got := varMarker(state, "x")
	if !strings.Contains(got, "[!]") {
		t.Errorf("[!] marker missing from %q", got)
	}
	if got == "[!]" {
		t.Errorf("[!] marker rendered without ANSI styling: %q", got)
	}
}

func TestTheme_statusBar_planLoadingShowsSpinner(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.planState = planLoading
	bar := m.renderStatus()
	if !strings.Contains(bar, "Running terraform plan") {
		t.Errorf("loading bar missing running-plan text; got: %q", bar)
	}
}

func TestTheme_statusBar_errorRendersRedMark(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.status = "boom"
	m.statusLvl = statusError
	bar := m.renderStatus()
	if !strings.Contains(bar, "boom") {
		t.Errorf("status missing error text; got: %q", bar)
	}
	if !strings.Contains(bar, "✗") {
		t.Errorf("error bar missing ✗ mark; got: %q", bar)
	}
}

func TestTheme_focusActiveCursor_differsFromInactive(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.focus = focusLeft
	leftFocused := m.renderLeftPane()
	m.focus = focusRight
	rightFocused := m.renderLeftPane()
	if leftFocused == rightFocused {
		t.Errorf("active vs inactive pane render identical; focus indicator is missing")
	}
}

func TestTheme_planTree_modulesAreAccented(t *testing.T) {
	plan := &tfjson.Plan{
		ResourceChanges: []*tfjson.ResourceChange{
			rc("module.cos_lite.juju_application.alertmanager",
				"module.cos_lite", "juju_application", "alertmanager",
				&tfjson.Change{Actions: []tfjson.Action{tfjson.ActionCreate}}),
		},
	}
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.plan = plan
	m.planTree = BuildPlanTree(plan)
	m.planState = planReady

	out := m.renderPlanScreen()
	if !strings.Contains(out, "module.cos_lite") {
		t.Fatalf("module label missing from plan view: %q", out)
	}
	// Sanity: some ANSI escape should be in the output.
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("plan view rendered with no ANSI styling at all")
	}
}
