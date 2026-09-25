package tui

import (
	"strings"
	"testing"
)

// TestSetTFVarsMode_showsHeaderChipAndHint guards the discoverability of the
// opt-in pass-through shape: with it on, the header carries a persistent
// `tfvars` chip and the footer explains where values live. Without this, the
// generated `x = var.x` forwards read as user-authored wiring.
func TestSetTFVarsMode_showsHeaderChipAndHint(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m.width, m.height = 140, 40

	m.SetTFVarsMode(true)
	if !m.TFVarsMode {
		t.Fatal("TFVarsMode not set")
	}
	if header := stripANSI(m.renderHeader()); !strings.Contains(header, "tfvars") {
		t.Errorf("header missing tfvars chip: %q", header)
	}
	if footer := stripANSI(m.renderFooter()); !strings.Contains(footer, "terraform.tfvars") {
		t.Errorf("footer missing opening hint: %q", footer)
	}
}

func TestSetTFVarsMode_classicHasNoChip(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m.width, m.height = 140, 40

	m.SetTFVarsMode(false)
	if m.TFVarsMode {
		t.Error("TFVarsMode should be false")
	}
	if header := stripANSI(m.renderHeader()); strings.Contains(header, "tfvars") {
		t.Errorf("classic wrapper should not show a tfvars chip: %q", header)
	}
}
