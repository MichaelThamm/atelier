package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderOutputView renders a full-screen view showing terraform outputs
// after a successful apply or when triggered via O. Dismissed with Esc.
func (m *Model) renderOutputView() string {
	var b strings.Builder
	fmt.Fprintln(&b, styleVarHeader.Render("Terraform outputs"))
	fmt.Fprintln(&b)

	if len(m.outputs) == 0 {
		fmt.Fprintln(&b, styleDescription.Render("  No outputs defined."))
	} else {
		// Sort output names for stable display.
		names := make([]string, 0, len(m.outputs))
		for name := range m.outputs {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			meta := m.outputs[name]
			val := formatOutputValue(meta.Value)
			if meta.Sensitive {
				val = styleDescription.Render("(sensitive)")
			}
			fmt.Fprintf(&b, "  %s = %s\n", styleVarHeader.Render(name), val)
		}
	}

	fmt.Fprintln(&b)
	fmt.Fprint(&b, styleHelp.Render("[Esc] close"))

	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Render(b.String())
}

// formatOutputValue renders a JSON value for display. Scalars are shown
// inline; complex values are pretty-printed with indentation.
func formatOutputValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return styleDescription.Render("null")
	}

	// Try scalar string first (most common output type).
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	// Pretty-print anything else (numbers, bools, objects, arrays).
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}

	pretty, err := json.MarshalIndent(v, "    ", "  ")
	if err != nil {
		return string(raw)
	}
	return string(pretty)
}
