package tui

import (
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/integrate"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

func topologyModel(t *testing.T) *Model {
	t.Helper()
	st := &wrapper.State{
		ModuleBlockName: "charmed_spark",
		Vars:            []tfvars.Variable{{Name: "cos_offers"}},
	}
	return &Model{
		WrapperDir: t.TempDir(),
		width:      120,
		height:     40,
		Modules:    []ModuleEntry{{State: st}},
		topoProposals: []integrate.Proposal{{
			Consumer: "charmed_spark", Input: "cos_offers",
			Producer: "cos_lite", Output: "cos_offers",
			Interfaces:  []string{"grafana_dashboard", "loki_push_api"},
			Subsettable: true,
		}},
		topoSel:   [][]bool{{true, true}},
		topoModal: true,
	}
}

func TestRenderTopology_showsInterfacesWithCheckboxes(t *testing.T) {
	m := topologyModel(t)
	out := stripANSI(m.renderTopology())
	for _, want := range []string{
		"charmed_spark.cos_offers <- module.cos_lite.cos_offers",
		"> [x] grafana_dashboard",
		"  [x] loki_push_api",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("topology view missing %q, got:\n%s", want, out)
		}
	}
}

func TestHandleTopologyKey_togglesAndSelectsAll(t *testing.T) {
	m := topologyModel(t)
	m.handleTopologyKey(key(" "))
	if m.topoSel[0][0] {
		t.Error("space should toggle the focused interface off")
	}
	m.handleTopologyKey(key("n"))
	if m.topoSel[0][0] || m.topoSel[0][1] {
		t.Errorf("n should clear all, got %v", m.topoSel)
	}
	m.handleTopologyKey(key("a"))
	if !m.topoSel[0][0] || !m.topoSel[0][1] {
		t.Errorf("a should select all, got %v", m.topoSel)
	}
	m.handleTopologyKey(key("down"))
	if m.topoCursor != 1 {
		t.Errorf("down should move the cursor, got %d", m.topoCursor)
	}
}

func TestApplyTopology_recordsTheChosenSubset(t *testing.T) {
	m := topologyModel(t)
	m.topoSel = [][]bool{{true, false}} // wire only grafana_dashboard
	m.applyTopology()

	if m.topoModal {
		t.Error("apply should close the modal")
	}
	if !m.dirty {
		t.Error("apply should mark the model dirty so the wire is saved")
	}
	st := m.Modules[0].State
	if _, wired := st.WiredExpression("cos_offers"); !wired {
		t.Fatalf("wire not recorded in state; UnknownAttrs=%+v", st.UnknownAttrs)
	}
	expr, _ := st.WiredExpression("cos_offers")
	if expr != "{ grafana_dashboard = module.cos_lite.cos_offers.grafana_dashboard }" {
		t.Errorf("wired expr = %q", expr)
	}
}
