package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// End-to-end reproduction of the prodstack-7 traefik bug: a module block with a
// wired reference (model_uuid = data.juju_model.service_model.uuid) must keep
// that line after switching the ref, exercising the REAL read -> applyRefSwitch
// -> write -> read chain.
func TestRefSwitch_endToEnd_preservesWiredReference(t *testing.T) {
	dir := t.TempDir()
	main := `module "traefik" {
  source     = "git::https://github.com/canonical/traefik-k8s-operator//terraform?ref=rev301"
  app_name   = "upstream-traefik"
  channel    = "latest/stable"
  model_uuid = data.juju_model.service_model.uuid
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := []tfvars.Variable{
		{Name: "app_name", Type: mustParseType(t, "string")},
		{Name: "channel", Type: mustParseType(t, "string")},
		{Name: "model_uuid", Type: mustParseType(t, "string")},
	}

	pm, err := wrapper.ReadMainForBlock(dir, "traefik", vars)
	if err != nil {
		t.Fatalf("ReadMainForBlock: %v", err)
	}
	if pm == nil {
		t.Fatal("ReadMainForBlock returned nil")
	}
	// Sanity: the wired ref lands in UnknownAttrs, not Values.
	if _, ok := pm.Values["model_uuid"]; ok {
		t.Fatal("model_uuid should be a wired expression, not a concrete value")
	}

	old := &wrapper.State{
		Dir:             dir,
		Source:          pm.Source,
		ModuleBlockName: "traefik",
		Vars:            vars,
		Values:          pm.Values,
		UnknownAttrs:    pm.UnknownAttrs,
	}
	m := New(old, "traefik")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Mimic prodRefSwitcher.SwitchRef -> PrepareState: a fresh schema for the
	// new ref with NO main.tf overlay (no Values, no UnknownAttrs).
	newState := &wrapper.State{
		Dir:             dir,
		Source:          "git::https://github.com/canonical/traefik-k8s-operator//terraform?ref=rev300",
		ModuleBlockName: "traefik",
		Vars:            vars,
		Values:          map[string]cty.Value{},
	}
	m.refModuleIdx = 0
	m.applyRefSwitch(&RefSwitchResult{State: newState, LiteralRef: "rev300", ResolvedSHA: "deadbeef"})

	if err := m.Modules[0].State.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	if !strings.Contains(got, "model_uuid = data.juju_model.service_model.uuid") {
		t.Errorf("wired model_uuid reference was dropped on ref switch.\nGot:\n%s", got)
	}
	if !strings.Contains(got, "ref=rev300") {
		t.Errorf("source ref was not updated to rev300.\nGot:\n%s", got)
	}
	if !strings.Contains(got, "upstream-traefik") {
		t.Errorf("concrete app_name value was dropped.\nGot:\n%s", got)
	}
	if !strings.Contains(got, "latest/stable") {
		t.Errorf("concrete channel value was dropped.\nGot:\n%s", got)
	}
}
