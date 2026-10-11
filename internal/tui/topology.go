package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichaelThamm/atelier/internal/integrate"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// openTopology builds the wrapper's proposed interface integrations (ADR-0062)
// and opens the topology modal. Every interface starts selected, so the default
// action wires the whole matched object.
func (m *Model) openTopology() (tea.Model, tea.Cmd) {
	proposals, err := integrate.Propose(m.WrapperDir)
	if err != nil {
		m.status = "integrate: " + err.Error()
		m.statusLvl = statusError
		m.statusAt = time.Now()
		return m, nil
	}
	if len(proposals) == 0 {
		m.status = "No proposed integrations."
		m.statusLvl = statusInfo
		m.statusAt = time.Now()
		return m, nil
	}
	m.topoProposals = proposals
	m.topoSel = make([][]bool, len(proposals))
	for i, p := range proposals {
		m.topoSel[i] = make([]bool, len(p.Interfaces))
		for j := range m.topoSel[i] {
			m.topoSel[i][j] = true
		}
	}
	m.topoCursor = 0
	m.topoModal = true
	return m, nil
}

// handleTopologyKey drives the topology modal: move between interface rows,
// toggle, select all/none, wire, or close.
func (m *Model) handleTopologyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "t", "T":
		m.topoModal = false
	case "up", "k":
		if m.topoCursor > 0 {
			m.topoCursor--
		}
	case "down", "j":
		if m.topoCursor < len(m.topoRows())-1 {
			m.topoCursor++
		}
	case " ":
		if i, j, ok := m.currentTopoRow(); ok {
			m.topoSel[i][j] = !m.topoSel[i][j]
		}
	case "a", "A":
		m.setAllTopo(true)
	case "n", "N":
		m.setAllTopo(false)
	case "enter":
		return m.applyTopology()
	}
	return m, nil
}

// applyTopology wires every proposal's selected interfaces into the consumer
// module's in-memory state, so the normal save writes them to main.tf. A
// proposal with nothing selected is skipped.
func (m *Model) applyTopology() (tea.Model, tea.Cmd) {
	wired := 0
	for i, p := range m.topoProposals {
		var selected []string
		for j, name := range p.Interfaces {
			if m.topoSel[i][j] {
				selected = append(selected, name)
			}
		}
		if len(selected) == 0 {
			continue
		}
		m.wireIntoState(p.Consumer, p.Input, p.WireExpr(selected))
		wired++
	}
	m.topoModal = false
	m.dirty = true
	m.status = fmt.Sprintf("Wired %d integration(s).", wired)
	m.statusLvl = statusInfo
	m.statusAt = time.Now()
	return m, nil
}

// wireIntoState records `input = expr` on the consumer module's in-memory state
// as a preserved expression, so the next save emits it and a later edit does
// not revert it (ADR-0062). It falls back to editing main.tf directly when the
// block is not among the loaded modules.
func (m *Model) wireIntoState(consumer, input, expr string) {
	for i := range m.Modules {
		st := m.Modules[i].State
		if st == nil || st.ModuleBlockName != consumer {
			continue
		}
		delete(st.Values, input)
		st.ClearUnknownAttr(input)
		st.UnknownAttrs = append(st.UnknownAttrs, wrapper.RawAttr{
			Name:    input,
			Raw:     []byte(input + " = " + expr),
			RawExpr: []byte(expr),
		})
		return
	}
	_ = wrapper.WireModuleInput(m.WrapperDir, consumer, input, expr)
}

// topoRows flattens the selectable interface rows across all proposals.
func (m *Model) topoRows() [][2]int {
	var rows [][2]int
	for i, p := range m.topoProposals {
		for j := range p.Interfaces {
			rows = append(rows, [2]int{i, j})
		}
	}
	return rows
}

func (m *Model) currentTopoRow() (int, int, bool) {
	rows := m.topoRows()
	if m.topoCursor < 0 || m.topoCursor >= len(rows) {
		return 0, 0, false
	}
	return rows[m.topoCursor][0], rows[m.topoCursor][1], true
}

func (m *Model) setAllTopo(v bool) {
	for i := range m.topoSel {
		for j := range m.topoSel[i] {
			m.topoSel[i][j] = v
		}
	}
}

// renderTopology draws the topology modal: each proposal with a checkbox per
// interface, the focused row marked.
func (m *Model) renderTopology() string {
	var b strings.Builder
	row := 0
	for i, p := range m.topoProposals {
		fmt.Fprintf(&b, "%s.%s <- module.%s.%s\n", p.Consumer, p.Input, p.Producer, p.Output)
		for j, name := range p.Interfaces {
			mark := "[ ]"
			if m.topoSel[i][j] {
				mark = "[x]"
			}
			cursor := "  "
			if row == m.topoCursor {
				cursor = "> "
			}
			fmt.Fprintf(&b, "%s%s %s\n", cursor, mark, name)
			row++
		}
		b.WriteString("\n")
	}
	footer := "space toggle · a all · n none · enter wire · esc close"
	return m.renderModalFrame("Integrate — proposed interfaces", strings.TrimRight(b.String(), "\n"), footer)
}
