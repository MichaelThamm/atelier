package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderOutputView renders a scrollable full-screen view showing terraform
// outputs. Supports j/k/arrows for scrolling, g/G for top/bottom, Esc to
// dismiss.
func (m *Model) renderOutputView() string {
	// Build content lines.
	lines := m.buildOutputLines()

	// Reserve 3 lines: header, blank, footer.
	viewH := m.height - 3
	if viewH < 1 {
		viewH = 1
	}

	// Clamp scroll.
	maxScroll := len(lines) - viewH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.outputScroll > maxScroll {
		m.outputScroll = maxScroll
	}

	// Slice visible window.
	end := m.outputScroll + viewH
	if end > len(lines) {
		end = len(lines)
	}
	visible := lines[m.outputScroll:end]

	// Compose.
	var b strings.Builder
	fmt.Fprintln(&b, styleVarHeader.Render("Terraform outputs"))
	fmt.Fprintln(&b)
	for _, line := range visible {
		fmt.Fprintln(&b, line)
	}

	// Footer with scroll indicator.
	footer := "[Esc] close  [j/k] scroll  [g/G] top/bottom"
	if len(lines) > viewH {
		pct := 0
		if maxScroll > 0 {
			pct = m.outputScroll * 100 / maxScroll
		}
		footer += fmt.Sprintf("  (%d%%)", pct)
	}
	fmt.Fprint(&b, styleHelp.Render(footer))

	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Render(b.String())
}

// buildOutputLines renders all outputs into styled lines.
func (m *Model) buildOutputLines() []string {
	if len(m.outputs) == 0 {
		return []string{styleDescription.Render("  No outputs defined.")}
	}

	names := make([]string, 0, len(m.outputs))
	for name := range m.outputs {
		names = append(names, name)
	}
	sort.Strings(names)

	var lines []string
	for i, name := range names {
		if i > 0 {
			lines = append(lines, "") // blank separator between outputs
		}
		meta := m.outputs[name]

		// Output header: name with sensitive tag.
		header := "  " + stylePlanModule.Render(name)
		if meta.Sensitive {
			header += " " + styleSensitiveTag.Render("(sensitive)")
		}
		lines = append(lines, header)

		if meta.Sensitive {
			continue
		}

		// Format the value with proper indentation.
		val := formatOutputValue(meta.Value)
		for _, vline := range strings.Split(val, "\n") {
			lines = append(lines, "    "+vline)
		}
	}
	return lines
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

	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(pretty)
}
