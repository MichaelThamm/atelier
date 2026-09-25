package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichaelThamm/atelier/internal/wrapper"
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

// openSavePreset opens the save-preset modal, refusing early when the current
// configuration has no non-default values (the bundle would be empty). The
// bundle is written to atelier.presets/<name>.tfvars in the wrapper directory
// (ADR-0032).
func (m *Model) openSavePreset() (tea.Model, tea.Cmd) {
	if len(snapshotValues(m.State)) == 0 {
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
// writes a new atelier.presets/<name>.tfvars in the wrapper directory. A blank
// name keeps the modal open; an existing file is refused so a hand-authored
// bundle is never overwritten.
func (m *Model) commitSavePreset() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.savePresetName.Value())
	if name == "" {
		return m, nil // name is required; wait for input
	}
	desc := strings.TrimSpace(m.savePresetDesc.Value())

	values := snapshotValues(m.State)
	if len(values) == 0 {
		m.savePresetModal = false
		m.flashStatus("nothing to save — all values are at their defaults", statusInfo)
		return m, nil
	}

	dir := filepath.Join(m.State.Dir, wrapper.PresetsDir)
	stem := bundleFileName(name)
	path := filepath.Join(dir, stem+".tfvars")
	if _, err := os.Stat(path); err == nil {
		m.flashStatus(fmt.Sprintf("%s already exists — choose another name", path), statusError)
		return m, nil // keep the modal open for renaming
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.savePresetModal = false
		m.flashStatus(fmt.Sprintf("save preset failed: %v", err), statusError)
		return m, nil
	}

	body := wrapper.RenderTFVarsValues(m.State.Vars, values)
	if desc != "" {
		body = append([]byte("# "+desc+"\n\n"), body...)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		m.savePresetModal = false
		m.flashStatus(fmt.Sprintf("save preset failed: %v", err), statusError)
		return m, nil
	}

	// Make the new bundle available in the picker immediately.
	m.presets = append(m.presets, ResolvedPreset{
		Name:        stem,
		Description: desc,
		Values:      values,
		Source:      "local",
	})
	m.savePresetModal = false
	m.flashStatus(fmt.Sprintf("Saved preset %q (%d vars) to %s", name, len(values), path), statusInfo)
	return m, nil
}

// bundleFileName turns a user-entered preset name into a safe .tfvars stem:
// lowercase, spaces and unsupported characters collapse to '-', and the result
// is never empty.
func bundleFileName(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		out = "preset"
	}
	return out
}
