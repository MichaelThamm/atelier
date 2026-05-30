package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

func mimirState(t *testing.T) *wrapper.State {
	t.Helper()
	return &wrapper.State{
		ModuleBlockName: "mimir",
		Vars: []tfvars.Variable{
			{Name: "model_uuid", Type: mustParseType(t, "string")},
			{Name: "channel", Type: mustParseType(t, "string"), HasDefault: true, Default: cty.StringVal("dev/edge")},
			{Name: "workers", Type: mustParseType(t, "map(object({units = optional(number, 1)}))"), HasDefault: true, Default: cty.EmptyObjectVal},
		},
		Values: map[string]cty.Value{},
	}
}

func seaweedState(t *testing.T) *wrapper.State {
	t.Helper()
	return &wrapper.State{
		ModuleBlockName: "seaweedfs",
		Vars: []tfvars.Variable{
			{Name: "model_uuid", Type: mustParseType(t, "string")},
			{Name: "filer_units", Type: mustParseType(t, "number"), HasDefault: true, Default: cty.NumberIntVal(1)},
		},
		Values: map[string]cty.Value{},
	}
}

func TestMultiModule_singleModule_noHeaders(t *testing.T) {
	st := mimirState(t)
	m := New(st, "mimir")
	// Single module: no headers in the row list.
	for _, r := range m.rows {
		if r.IsHeader {
			t.Error("single-module model should have no header rows")
		}
	}
	if len(m.rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(m.rows))
	}
}

func TestMultiModule_twoModules_hasHeaders(t *testing.T) {
	st := mimirState(t)
	m := New(st, "mimir")
	m.AddModule(seaweedState(t), "seaweedfs")

	// Should have 2 headers + 3 mimir vars + 2 seaweedfs vars = 7 rows.
	if len(m.rows) != 7 {
		t.Fatalf("expected 7 rows, got %d", len(m.rows))
	}
	// First row: header for mimir.
	if !m.rows[0].IsHeader || m.rows[0].VarName != "mimir" {
		t.Errorf("row 0 should be mimir header, got %+v", m.rows[0])
	}
	// Row 4: header for seaweedfs.
	if !m.rows[4].IsHeader || m.rows[4].VarName != "seaweedfs" {
		t.Errorf("row 4 should be seaweedfs header, got %+v", m.rows[4])
	}
}

func TestMultiModule_cursorSkipsHeaders(t *testing.T) {
	st := mimirState(t)
	m := New(st, "mimir")
	m.AddModule(seaweedState(t), "seaweedfs")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Initial cursor should be on the first variable (skipping header).
	if m.cursor != 1 {
		t.Fatalf("initial cursor should be 1 (first var), got %d", m.cursor)
	}
	if v := m.SelectedVariable(); v == nil || v.Name != "model_uuid" {
		t.Errorf("expected model_uuid, got %v", v)
	}

	// Move down through mimir vars.
	m = feed(m, key("down")) // channel
	m = feed(m, key("down")) // workers
	m = feed(m, key("down")) // should skip seaweedfs header → seaweedfs.model_uuid
	v := m.SelectedVariable()
	if v == nil || v.Name != "model_uuid" {
		t.Errorf("expected seaweedfs model_uuid after skipping header, got %v", v)
	}
	// Verify it's from the seaweedfs module (index 1).
	if m.rows[m.cursor].ModuleIdx != 1 {
		t.Errorf("expected ModuleIdx=1 (seaweedfs), got %d", m.rows[m.cursor].ModuleIdx)
	}
}

func TestMultiModule_cursorSkipsHeadersGoingUp(t *testing.T) {
	st := mimirState(t)
	m := New(st, "mimir")
	m.AddModule(seaweedState(t), "seaweedfs")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Navigate to seaweedfs first var.
	m = feed(m, key("down"), key("down"), key("down"))
	// Now go back up: should skip the seaweedfs header.
	m = feed(m, key("up"))
	if v := m.SelectedVariable(); v == nil || v.Name != "workers" {
		t.Errorf("expected workers going up past header, got %v", v)
	}
	if m.rows[m.cursor].ModuleIdx != 0 {
		t.Errorf("expected ModuleIdx=0 (mimir), got %d", m.rows[m.cursor].ModuleIdx)
	}
}

func TestMultiModule_editAppliesCorrectState(t *testing.T) {
	st1 := mimirState(t)
	st2 := seaweedState(t)
	m := New(st1, "mimir")
	m.AddModule(st2, "seaweedfs")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Navigate to seaweedfs.filer_units (last row).
	m = feed(m, key("down"), key("down"), key("down"), key("down"))
	if v := m.SelectedVariable(); v == nil || v.Name != "filer_units" {
		t.Fatalf("expected filer_units, got %v", m.SelectedVariable())
	}

	// Edit it.
	m = feed(m, key("tab")) // focus editor
	m = feed(m, key("backspace"), key("3"))

	// Value should be in seaweedfs state, not mimir state.
	if _, ok := st1.Values["filer_units"]; ok {
		t.Error("filer_units should NOT be in mimir state")
	}
	if val, ok := st2.Values["filer_units"]; !ok {
		t.Error("filer_units should be in seaweedfs state")
	} else if !val.Equals(cty.NumberFloatVal(3)).True() {
		t.Errorf("expected 3, got %v", val.GoString())
	}
}

func TestMultiModule_renderLeftPane_showsHeaders(t *testing.T) {
	st := mimirState(t)
	m := New(st, "mimir")
	m.AddModule(seaweedState(t), "seaweedfs")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	view := m.renderLeftPane()
	stripped := stripANSI(view)
	if !strings.Contains(stripped, "── mimir") {
		t.Errorf("left pane should contain mimir header, got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "── seaweedfs") {
		t.Errorf("left pane should contain seaweedfs header, got:\n%s", stripped)
	}
}
