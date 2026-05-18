package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// renderOutputView renders a scrollable centered modal showing terraform
// outputs. Supports j/k/arrows for scrolling, g/G for top/bottom, Esc to
// dismiss.
func (m *Model) renderOutputView() string {
	// Build content lines.
	lines := m.buildOutputLines()

	// Inner dimensions match the modal frame: width - 8, height - 7 (border+padding+title+footer).
	innerH := m.height - 9
	if innerH < 3 {
		innerH = 3
	}

	// Clamp scroll.
	maxScroll := len(lines) - innerH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.outputScroll > maxScroll {
		m.outputScroll = maxScroll
	}

	// Slice visible window.
	end := m.outputScroll + innerH
	if end > len(lines) {
		end = len(lines)
	}
	visible := lines[m.outputScroll:end]

	body := strings.Join(visible, "\n")

	// Footer with scroll indicator.
	footer := "[Esc] close  [j/k] scroll  [g/G] top/bottom"
	if len(lines) > innerH {
		pct := 0
		if maxScroll > 0 {
			pct = m.outputScroll * 100 / maxScroll
		}
		footer += fmt.Sprintf("  (%d%%)", pct)
	}

	return m.renderModalFrame("Terraform outputs", body, footer)
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

// formatOutputValue renders a JSON value for display with syntax highlighting.
// Scalars are shown inline; complex values are pretty-printed with colored
// keys, strings, numbers, bools, and nulls.
func formatOutputValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return styleJsonNull.Render("null")
	}

	// Try scalar string first (most common output type).
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return styleJsonString.Render(`"` + s + `"`)
	}

	// Pretty-print anything else.
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}

	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(raw)
	}
	return colouriseJSON(string(pretty))
}

// colouriseJSON applies syntax highlighting to pretty-printed JSON.
func colouriseJSON(s string) string {
	var b strings.Builder
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(colouriseJSONLine(line))
	}
	return b.String()
}

// colouriseJSONLine highlights a single line of pretty-printed JSON.
// Handles: "key": value, standalone values, braces/brackets.
func colouriseJSONLine(line string) string {
	trimmed := strings.TrimSpace(line)
	indent := line[:len(line)-len(trimmed)]

	// Structural characters only (braces, brackets, trailing commas).
	if trimmed == "{" || trimmed == "}" || trimmed == "}," ||
		trimmed == "[" || trimmed == "]" || trimmed == "]," {
		return indent + styleJsonBrace.Render(trimmed)
	}

	// Key-value pair: "key": value
	if strings.HasPrefix(trimmed, `"`) {
		colonIdx := strings.Index(trimmed, `": `)
		if colonIdx > 0 {
			key := trimmed[:colonIdx+1] // includes closing quote
			rest := trimmed[colonIdx+2:]  // ": value" → " value"
			return indent + styleJsonKey.Render(key) + styleJsonBrace.Render(":") + colouriseValue(rest)
		}
	}

	// Standalone value (array element).
	return indent + colouriseValue(trimmed)
}

// colouriseValue highlights a JSON value token.
func colouriseValue(s string) string {
	s = strings.TrimPrefix(s, " ")
	trailing := ""
	if strings.HasSuffix(s, ",") {
		trailing = ","
		s = s[:len(s)-1]
	}

	var styled string
	switch {
	case s == "null":
		styled = styleJsonNull.Render(s)
	case s == "true" || s == "false":
		styled = styleJsonBool.Render(s)
	case strings.HasPrefix(s, `"`):
		styled = styleJsonString.Render(s)
	case s == "{" || s == "}" || s == "[" || s == "]":
		styled = styleJsonBrace.Render(s)
	case len(s) > 0 && (s[0] >= '0' && s[0] <= '9' || s[0] == '-'):
		styled = styleJsonNumber.Render(s)
	default:
		styled = s
	}
	return " " + styled + trailing
}
