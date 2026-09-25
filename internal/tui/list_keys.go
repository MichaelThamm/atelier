package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/zclconf/go-cty/cty"
)

// handleLogsKey processes key events when the logs view is active.
func (m *Model) handleLogsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.progress == nil {
		m.activeView = viewEditor
		m.logScroll = 0
		return m, nil
	}

	// Get lines based on active tab for scroll calculations
	var lines []LogLine
	switch m.logsTab {
	case logsTabErrors:
		lines = m.progress.StderrLines()
	case logsTabLogs:
		lines = m.progress.StdoutLines()
	}

	h := m.panelHeight() - 1 // tab bar takes one line
	if h < 1 {
		h = 1
	}

	switch msg.String() {
	case "l", "L", "esc":
		m.activeView = viewEditor
		m.logScroll = 0
		return m, nil
	case "tab":
		// Switch between errors and logs tabs, reset scroll to bottom
		if m.logsTab == logsTabErrors {
			m.logsTab = logsTabLogs
		} else {
			m.logsTab = logsTabErrors
		}
		// Re-fetch lines for the new tab
		switch m.logsTab {
		case logsTabErrors:
			lines = m.progress.StderrLines()
		case logsTabLogs:
			lines = m.progress.StdoutLines()
		}
		// Reset scroll to bottom when switching tabs
		m.logScroll = len(lines) - h
		if m.logScroll < 0 {
			m.logScroll = 0
		}
		m.logAutoScroll = true
		return m, nil
	case "up", "k":
		if m.logScroll > 0 {
			m.logScroll--
		}
		m.logAutoScroll = false
		return m, nil
	case "down", "j":
		if m.logScroll < len(lines)-h {
			m.logScroll++
		}
		if m.logScroll >= len(lines)-h {
			m.logAutoScroll = true
		}
		return m, nil
	case "pgup":
		m.logScroll -= h
		if m.logScroll < 0 {
			m.logScroll = 0
		}
		m.logAutoScroll = false
		return m, nil
	case "pgdown":
		m.logScroll += h
		if max := len(lines) - h; m.logScroll > max {
			m.logScroll = max
			if m.logScroll < 0 {
				m.logScroll = 0
			}
		}
		if m.logScroll >= len(lines)-h {
			m.logAutoScroll = true
		}
		return m, nil
	case "home", "g":
		m.logScroll = 0
		m.logAutoScroll = false
		return m, nil
	case "end", "G":
		m.logScroll = len(lines) - h
		if m.logScroll < 0 {
			m.logScroll = 0
		}
		m.logAutoScroll = true
		return m, nil
	}
	return m, nil
}

// flashStatus sets the transient footer status line at the given level.
func (m *Model) flashStatus(text string, lvl statusLevel) {
	m.status = text
	m.statusLvl = lvl
	m.statusAt = time.Now()
}

// focusVar moves the cursor to the row owning variable `name` in module
// `moduleIdx`, adjusting scroll so it is visible. No-op if not found.
func (m *Model) focusVar(moduleIdx int, name string) {
	for i, r := range m.rows {
		if r.IsHeader || r.VarName != name {
			continue
		}
		if len(m.Modules) > 1 && r.ModuleIdx != moduleIdx {
			continue
		}
		m.cursor = i
		m.scrollToCursor()
		return
	}
}

func (m *Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(+1)
	case "pgup", "ctrl+u":
		m.moveCursor(-m.leftPaneVisibleRows() / 2)
	case "pgdown", "ctrl+d":
		m.moveCursor(m.leftPaneVisibleRows() / 2)
	case "home", "g":
		m.moveCursor(-len(m.rows))
	case "end", "G":
		m.moveCursor(len(m.rows))
	case "enter", "right", "l":
		m.setFocus(focusRight)
		return m, nil
	}
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	if len(m.rows) == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	c := m.cursor
	for i := 0; i < abs(delta); i++ {
		c += step
		// Skip over header rows.
		for c >= 0 && c < len(m.rows) && m.rows[c].IsHeader {
			c += step
		}
		if c < 0 || c >= len(m.rows) {
			return
		}
	}
	m.cursor = c
	m.scrollToCursor()
	m.refreshEditor()
}

// skipHeader advances the cursor past a header in the given direction.
func (m *Model) skipHeader(dir int) {
	for m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].IsHeader {
		m.cursor += dir
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
}

// scrollToCursor adjusts leftScroll so the cursor is visible within the
// left pane's height.
func (m *Model) scrollToCursor() {
	visible := m.leftPaneVisibleRows()
	if visible <= 0 {
		return
	}
	if m.cursor < m.leftScroll {
		m.leftScroll = m.cursor
	} else if m.cursor >= m.leftScroll+visible {
		m.leftScroll = m.cursor - visible + 1
	}
}

// leftPaneVisibleRows returns how many variable rows fit in the left pane.
func (m *Model) leftPaneVisibleRows() int {
	return m.panelHeight()
}

func (m *Model) applyEditorValue(v *tfvars.Variable, val cty.Value) {
	st := m.ActiveModuleState()
	if st.Values == nil {
		st.Values = map[string]cty.Value{}
	}
	if val == cty.NilVal {
		delete(st.Values, v.Name)
	} else {
		st.Values[v.Name] = val
		// A concrete value supersedes any reference expression the variable
		// was originally wired to (both the [→] display and the writer prefer
		// Values when present), but we deliberately KEEP the preserved raw
		// form so a later Ctrl+R can restore the original reference instead of
		// leaving the variable empty.
	}
	m.dirty = true
}

// resetCurrent restores the user's current focus point to its declared
// default. The behaviour is contextual:
//
//   - Focus on the right pane with an object editor open: reset the single
//     focused field of that object. The other fields keep their current
//     values.
//   - Anywhere else: reset the whole selected variable. The entry is
//     removed from state.Values so the sparse-write rule treats it as
//     at-default (and the variable's [ ] marker re-appears in the left
//     pane).
//
// In both cases the editor is rebuilt so what the user sees on screen
// matches state.
func (m *Model) resetCurrent() {
	v := m.SelectedVariable()
	if v == nil {
		return
	}

	if m.focus == focusRight {
		if oe, ok := m.editor.(*objectEditor); ok {
			oe.ResetFocused()
			m.applyEditorValue(v, oe.CurrentValue())
			m.status = "field reset to default"
			m.statusLvl = statusInfo
			return
		}
	}

	// Whole-variable reset.
	st := m.ActiveModuleState()
	delete(st.Values, v.Name)
	// We do NOT drop any preserved reference expression here. Removing the
	// concrete override lets the original wiring resurface, so a variable that
	// was read as e.g. `data.vault_generic_secret.s3.data["..."]` returns to
	// its [→] view instead of becoming an empty/required field.
	m.dirty = true
	m.refreshEditor()
	if _, wired := st.WiredExpression(v.Name); wired {
		m.status = "variable reset to original reference"
	} else {
		m.status = "variable reset to default"
	}
	m.statusLvl = statusInfo
}
