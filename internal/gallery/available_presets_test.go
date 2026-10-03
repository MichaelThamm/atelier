package gallery

import "testing"

// An available preset is offered rather than composed, so it must still be
// reachable by name: `atelier apply cos --var-file cos-no-ingress` resolves it
// like any other bundle.
func TestAvailablePresetResolves(t *testing.T) {
	entry, ok := Find("cos")
	if !ok {
		t.Fatal("no gallery entry named cos")
	}
	for _, p := range entry.AvailablePresets {
		if !HasPreset(p) {
			t.Errorf("available preset %q is not embedded", p)
		}
		if _, ok := PresetPath(p); !ok {
			t.Errorf("available preset %q did not materialize", p)
		}
	}
}

// AllPresets is what validate and materialize walk, so an available preset that
// neither list reports would fail the manifest as an orphan.
func TestAllPresets_coversBothLists(t *testing.T) {
	e := Entry{
		Name:             "x",
		Presets:          []string{"a"},
		AvailablePresets: []string{"b"},
	}
	got := e.AllPresets()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("AllPresets = %v, want [a b]", got)
	}
}

// A preset listed as both composed and available has no single status: it is
// applied on every run and advertised as an opt-in. validate rejects it.
func TestValidate_rejectsPresetInBothLists(t *testing.T) {
	if err := validate([]Entry{{
		Name:             "x",
		Module:           "https://example.com/m",
		Ref:              "abc",
		Presets:          []string{"cos-single-unit"},
		AvailablePresets: []string{"cos-single-unit"},
	}}); err == nil {
		t.Fatal("validate accepted a preset that is both composed and available")
	}
}

// Every embedded preset must be claimed by some entry, composed or available.
// A preset neither list names would never be exercised by the gallery check and
// could rot unnoticed.
func TestShippedEntriesClaimEveryPreset(t *testing.T) {
	entries, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	claimed := map[string]bool{}
	for _, e := range entries {
		for _, p := range e.AllPresets() {
			if _, err := PresetKeys(p); err != nil {
				t.Errorf("%s: %v", e.Name, err)
			}
			claimed[p] = true
		}
	}
	for _, p := range []string{
		"cos-grafana-single-unit", "cos-single-unit", "cos-no-ingress",
		"cos-lite-no-ingress", "spark-single-unit",
	} {
		if !claimed[p] {
			t.Errorf("preset %q is embedded but no entry claims it", p)
		}
	}
}
