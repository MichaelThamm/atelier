package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// lokiWorkerLikeVar mirrors the COS `loki_worker` shape that triggered the
// broken border: many fields, several with names longer than the object
// editor's fixed name column (e.g. backend_storage_directives), so the
// compact rows can exceed the pane's inner width and the field list is long
// enough to scroll.
func lokiWorkerLikeVar(t *testing.T) tfvars.Variable {
	t.Helper()
	tp := mustParseType(t, `object({
		backend_config             = optional(map(string), {})
		read_config                = optional(map(string), {})
		write_config               = optional(map(string), {})
		constraints                = optional(string, "arch=amd64")
		resources                  = optional(map(string), {})
		revision                   = optional(list(number), [])
		backend_storage_directives = optional(map(string), {})
		read_storage_directives    = optional(map(string), {})
		write_storage_directives   = optional(map(string), {})
		backend_units              = optional(number, 1)
		read_units                 = optional(number, 1)
		write_units                = optional(number, 1)
	})`)
	return tfvars.Variable{
		Name:       "loki_worker",
		Type:       tp,
		HasDefault: true,
		Default:    cty.EmptyObjectVal,
	}
}

// TestRightPane_heightMatchesLeft_whenObjectScrolls is the regression guard
// for the border-sizing bug: when a long object editor scrolls, the right
// pane must render to exactly the same physical height as the left pane, so
// their bottom borders align and the footer below them isn't shoved out of
// place. Over-long unbreakable tokens previously wrapped at Render time,
// making the right pane one row taller than its declared Height.
func TestRightPane_heightMatchesLeft_whenObjectScrolls(t *testing.T) {
	state := &wrapper.State{
		Vars:   []tfvars.Variable{lokiWorkerLikeVar(t)},
		Values: map[string]cty.Value{},
	}

	// A small height forces the tall object field list to scroll; a wide
	// enough width matches the reported terminal.
	for _, dim := range []struct{ w, h int }{
		{145, 24},
		{120, 20},
		{100, 16},
	} {
		m := New(state, "cos")
		m = feed(m, tea.WindowSizeMsg{Width: dim.w, Height: dim.h})
		// Focus the editor and drill into the object so its field list drives
		// the right pane (and can scroll).
		m.setFocus(focusRight)

		left := m.renderLeftPane()
		right := m.renderRightPane()
		lh, rh := lipgloss.Height(left), lipgloss.Height(right)
		if lh != rh {
			t.Errorf("%dx%d: left pane height %d != right pane height %d\n--- right ---\n%s",
				dim.w, dim.h, lh, rh, right)
		}
		// No content line may exceed the pane's inner width (which is what
		// causes the re-wrap). Inner width = rightWidth - 2.
		rightWidth := dim.w - 37
		if rightWidth < 20 {
			rightWidth = 20
		}
		innerW := rightWidth - 2
		for i, ln := range strings.Split(stripANSI(right), "\n") {
			// Strip the border columns before measuring content width.
			trimmed := strings.Trim(ln, "│╭╮╰╯─ ")
			if w := lipgloss.Width(trimmed); w > innerW {
				t.Errorf("%dx%d: right pane line %d width %d > innerW %d: %q",
					dim.w, dim.h, i, w, innerW, trimmed)
			}
		}
	}
}
