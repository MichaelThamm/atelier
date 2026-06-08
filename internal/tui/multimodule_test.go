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

// TestMultiModule_renderLeftPane_headerNeverOverflows guards against the
// section header wrapping bug: a long "name @ref" header must be truncated so
// the rendered line never exceeds the pane's inner content width. Previously
// the math used byte length, so the multi-byte ellipsis pushed the line one
// column over the border and wrapped onto the next row.
func TestMultiModule_renderLeftPane_headerNeverOverflows(t *testing.T) {
	const innerWidth = 30 // leftWidth(32) - 2 border cols; see renderLeftPane

	st := mimirState(t)
	m := New(st, "mimir")
	m.AddModule(seaweedState(t), "ingress_configurator")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	// A ref long enough that "ingress_configurator @rev..." exceeds the pane.
	m.Modules[1].Ref = "rev123456789"

	view := m.renderLeftPane()
	for _, line := range strings.Split(stripANSI(view), "\n") {
		if !strings.HasPrefix(strings.TrimLeft(line, " "), "──") {
			continue // only assert on header lines
		}
		trimmed := strings.TrimRight(line, " ")
		if w := lipgloss.Width(trimmed); w > innerWidth {
			t.Errorf("header line overflows pane (%d > %d cols): %q", w, innerWidth, trimmed)
		}
	}
}

// TestModuleLabel verifies the single version-token helper (ADR-0019): a
// pinned module renders "name@ref"; an unpinned module renders the bare name
// with no SHA and no synthesized branch.
func TestModuleLabel(t *testing.T) {
	if got := moduleLabel("traefik", "rev301"); got != "traefik@rev301" {
		t.Errorf("pinned: got %q, want %q", got, "traefik@rev301")
	}
	if got := moduleLabel("cos", ""); got != "cos" {
		t.Errorf("unpinned: got %q, want %q", got, "cos")
	}
}

// TestModuleBanner_unifiedFormat verifies the top banner (ADR-0019, as
// amended): single-module sessions render "Module: name@ref"; an unpinned
// remote module gets a dim "unpinned" affordance while an unpinned *local*
// module (no source to pin) stays a bare name. The banner never shows a
// "(sha)" fragment.
func TestModuleBanner_unifiedFormat(t *testing.T) {
	t.Run("pinned", func(t *testing.T) {
		m := New(mimirState(t), "traefik")
		m.Modules[0].SourceURL = "git::https://example.com/repo//mod"
		m.Modules[0].Ref = "rev301"
		m.Modules[0].ResolvedSHA = "abc1234def5678"

		banner := stripANSI(m.moduleBanner())
		if banner != "Module: traefik@rev301" {
			t.Errorf("got %q, want %q", banner, "Module: traefik@rev301")
		}
		if strings.Contains(banner, "(") {
			t.Errorf("banner should not contain a SHA fragment: %q", banner)
		}
	})

	t.Run("unpinned remote", func(t *testing.T) {
		m := New(mimirState(t), "cos")
		m.Modules[0].SourceURL = "git::https://example.com/repo//cos"
		m.Modules[0].ResolvedSHA = "abc1234def5678" // present but must not show

		banner := stripANSI(m.moduleBanner())
		if !strings.HasPrefix(banner, "Module: cos") {
			t.Errorf("got %q, want prefix %q", banner, "Module: cos")
		}
		if !strings.Contains(banner, "unpinned") {
			t.Errorf("unpinned remote module should show affordance, got %q", banner)
		}
		if strings.Contains(banner, "(") {
			t.Errorf("unpinned banner should not contain a SHA fragment: %q", banner)
		}
	})

	t.Run("unpinned local", func(t *testing.T) {
		m := New(mimirState(t), "cos") // no SourceURL: nothing to pin

		banner := stripANSI(m.moduleBanner())
		if banner != "Module: cos" {
			t.Errorf("local module should be a bare name, got %q", banner)
		}
	})
}

// TestModuleBanner_multiModulePosition verifies that multi-module sessions
// carry the active module's position in the banner, distinguishing it from
// the per-group section headers (ADR-0019, as amended).
func TestModuleBanner_multiModulePosition(t *testing.T) {
	m := New(mimirState(t), "mimir")
	m.AddModule(seaweedState(t), "seaweedfs")
	m.Modules[1].Ref = "rev81"

	// Cursor starts on the first variable of module 0 (mimir).
	if got := stripANSI(m.moduleBanner()); got != "Module 1/2: mimir" {
		t.Errorf("got %q, want %q", got, "Module 1/2: mimir")
	}

	// Move the cursor into the second module's variables.
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	for i := 0; i < len(m.rows); i++ {
		if m.rows[i].ModuleIdx == 1 && !m.rows[i].IsHeader {
			m.cursor = i
			break
		}
	}
	if got := stripANSI(m.moduleBanner()); got != "Module 2/2: seaweedfs@rev81" {
		t.Errorf("got %q, want %q", got, "Module 2/2: seaweedfs@rev81")
	}
}

// TestMultiModule_renderLeftPane_preservesRefSuffix verifies that when a
// "name@ref" header overflows the pane, the name is truncated but the
// actionable "@ref" suffix is preserved whole (ADR-0019).
func TestMultiModule_renderLeftPane_preservesRefSuffix(t *testing.T) {
	st := mimirState(t)
	m := New(st, "mimir")
	m.AddModule(seaweedState(t), "ingress_configurator")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Modules[1].Ref = "rev123456789"

	stripped := stripANSI(m.renderLeftPane())
	if !strings.Contains(stripped, "@rev123456789") {
		t.Errorf("left pane should preserve full @ref suffix, got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "ingress_conf") {
		t.Errorf("left pane should retain a truncated module name, got:\n%s", stripped)
	}
}

// TestMultiModule_renderLeftPane_unpinnedAffordance verifies that an unpinned
// remote module's header shows the dim "unpinned" affordance, while an
// unpinned local module (no source) does not (ADR-0019, as amended).
func TestMultiModule_renderLeftPane_unpinnedAffordance(t *testing.T) {
	st := mimirState(t)
	m := New(st, "mimir")
	m.AddModule(seaweedState(t), "seaweedfs")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	// mimir is a remote module with no pin; seaweedfs is local.
	m.Modules[0].SourceURL = "git::https://example.com/repo//mimir"

	stripped := stripANSI(m.renderLeftPane())
	mimirHeader, seaweedHeader := "", ""
	for _, line := range strings.Split(stripped, "\n") {
		switch {
		case strings.Contains(line, "── mimir"):
			mimirHeader = line
		case strings.Contains(line, "── seaweedfs"):
			seaweedHeader = line
		}
	}
	if !strings.Contains(mimirHeader, "unpinned") {
		t.Errorf("remote unpinned module header should show affordance, got %q", mimirHeader)
	}
	if strings.Contains(seaweedHeader, "unpinned") {
		t.Errorf("local module header should not show affordance, got %q", seaweedHeader)
	}
}

// TestTruncateMiddle verifies the ref middle-truncation helper preserves both
// the head and the discriminating tail (ADR-0019, as amended).
func TestTruncateMiddle(t *testing.T) {
	if got := truncateMiddle("short", 10); got != "short" {
		t.Errorf("no-op case: got %q, want %q", got, "short")
	}
	got := truncateMiddle("release/2026-06-07-hotfix", 12)
	if lipgloss.Width(got) > 12 {
		t.Errorf("result %q exceeds width 12", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("expected an ellipsis in %q", got)
	}
	if !strings.HasPrefix(got, "release") && !strings.HasPrefix(got, "releas") {
		t.Errorf("expected head preserved, got %q", got)
	}
	if !strings.HasSuffix(got, "hotfix") && !strings.HasSuffix(got, "otfix") {
		t.Errorf("expected discriminating tail preserved, got %q", got)
	}
}
