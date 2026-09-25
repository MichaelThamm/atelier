package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichaelThamm/atelier/internal/manifest"
)

// handlePresetKey routes keys while the preset picker overlay is visible.
func (m *Model) handlePresetKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.presetPicker = false
		return m, nil
	case "up", "k":
		if m.presetCursor > 0 {
			m.presetCursor--
		}
		return m, nil
	case "down", "j":
		if m.presetCursor < len(m.presets)-1 {
			m.presetCursor++
		}
		return m, nil
	case "enter":
		cmd := m.applyPresetCmd(m.presetCursor)
		m.presetPicker = false
		return m, cmd
	}
	return m, nil
}

// applyPreset applies the preset at index i: merges its values into
// state.Values, refreshes the editor, and flashes a status message.
func (m *Model) applyPreset(i int) {
	if i < 0 || i >= len(m.presets) {
		return
	}
	p := m.presets[i]
	for name, val := range p.Values {
		m.State.Values[name] = val
		// The preset value supersedes any reference expression on this var,
		// but we keep the preserved raw form so a later reset can restore it.
	}
	m.refreshEditor()
	m.status = fmt.Sprintf("Applied preset: %s", p.Name)
	m.statusLvl = statusInfo
	m.statusAt = time.Now()
	m.dirty = true
}

// applyPresetCmd wraps applyPreset and returns a validate debounce command.
func (m *Model) applyPresetCmd(i int) tea.Cmd {
	m.applyPreset(i)
	return m.scheduleValidate()
}

// openSavePreset opens the save-preset modal, refusing early (with a status
// hint, no modal) in the two cases where there is nothing useful to do:
// an atelier.local.yaml already exists in the wrapper directory (Atelier never
// overwrites one — ADR-0026), or the configuration is entirely at its defaults
// so the generated preset would be empty.
func (m *Model) openSavePreset() (tea.Model, tea.Cmd) {
	if manifest.HasLocalFile(m.State.Dir) {
		m.flashStatus(fmt.Sprintf("%s already exists here — edit it directly or move it to a parent",
			manifest.LocalFileName), statusInfo)
		return m, nil
	}
	_, n := snapshotPreset(m.State, "", "")
	if n == 0 {
		m.flashStatus("nothing to save — all values are at their defaults", statusInfo)
		return m, nil
	}

	m.savePresetModal = true
	m.savePresetFocus = 0
	m.savePresetName = newCellInput("", false, "")
	m.savePresetDesc = newCellInput("", false, "")
	m.savePresetDesc.Blur()
	return m, nil
}

// handleSavePresetKey routes keys while the save-preset modal is visible. Tab
// (or ↑/↓) moves between the name and description fields; Enter writes the file;
// Esc cancels. Everything else edits the focused field.
func (m *Model) handleSavePresetKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.savePresetModal = false
		return m, nil
	case tea.KeyTab, tea.KeyDown, tea.KeyUp:
		m.setSavePresetFocus(1 - m.savePresetFocus)
		return m, nil
	case tea.KeyEnter:
		return m.commitSavePreset()
	}
	if m.savePresetFocus == 0 {
		m.savePresetName.Update(msg)
	} else {
		m.savePresetDesc.Update(msg)
	}
	return m, nil
}

// setSavePresetFocus moves focus between the name (0) and description (1)
// cells, updating which one draws a caret.
func (m *Model) setSavePresetFocus(f int) {
	m.savePresetFocus = f
	if f == 0 {
		m.savePresetName.Focus()
		m.savePresetDesc.Blur()
	} else {
		m.savePresetName.Blur()
		m.savePresetDesc.Focus()
	}
}

// commitSavePreset validates the name, snapshots the current configuration, and
// writes a new atelier.local.yaml. A blank name keeps the modal open; a write
// error (including a file that appeared since the modal opened) is surfaced in
// the status line.
func (m *Model) commitSavePreset() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.savePresetName.Value())
	if name == "" {
		return m, nil // name is required; wait for input
	}
	desc := strings.TrimSpace(m.savePresetDesc.Value())

	preset, n := snapshotPreset(m.State, name, desc)
	if n == 0 {
		m.savePresetModal = false
		m.flashStatus("nothing to save — all values are at their defaults", statusInfo)
		return m, nil
	}

	m.savePresetModal = false
	if err := manifest.SavePreset(m.State.Dir, preset); err != nil {
		m.flashStatus(fmt.Sprintf("save preset failed: %v", err), statusError)
		return m, nil
	}
	m.flashStatus(fmt.Sprintf("Saved preset %q (%d vars) to %s", name, n, manifest.LocalFileName), statusInfo)
	return m, nil
}
