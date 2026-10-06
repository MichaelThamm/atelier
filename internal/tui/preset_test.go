package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

func sampleVarsForPreset(t *testing.T) []tfvars.Variable {
	t.Helper()
	return []tfvars.Variable{
		{
			Name:       "internal_tls",
			Type:       mustParseType(t, "bool"),
			HasDefault: true,
			Default:    cty.False,
		},
		{
			Name:       "alertmanager",
			Type:       mustParseType(t, `object({ app_name = optional(string, "alertmanager"), units = optional(number, 1) })`),
			HasDefault: true,
			Default:    cty.EmptyObjectVal,
		},
		{
			Name:       "labels",
			Type:       mustParseType(t, `map(string)`),
			HasDefault: true,
			Default:    cty.MapValEmpty(cty.String),
		},
	}
}

// TestPresetPicker_isGone pins the removal of the `F` picker. Applying a bundle
// is `atelier apply --var-file NAME` and listing them is `atelier presets
// list`, so the TUI must not advertise a key for it.
func TestPresetPicker_isGone(t *testing.T) {
	state := &wrapper.State{Vars: sampleVarsForPreset(t), Values: map[string]cty.Value{}}
	m := New(state, "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	for _, k := range []string{"f", "F"} {
		before := len(m.State.Values)
		m = feed(m, key(k))
		if len(m.State.Values) != before {
			t.Errorf("%q changed values; the picker is gone and must apply nothing", k)
		}
	}

	m.helpModal = true
	plain := stripANSI(m.renderHelpModal())
	if strings.Contains(plain, "Open preset picker") {
		t.Errorf("help modal still advertises the picker; got:\n%s", plain)
	}
	if !strings.Contains(plain, "Save current config as a preset") {
		t.Errorf("help modal lost [S]; got:\n%s", plain)
	}
}
