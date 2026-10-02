package gallery

import "testing"

func TestPresetKeys(t *testing.T) {
	got, err := PresetKeys("cos-single-unit")
	if err != nil {
		t.Fatalf("PresetKeys: %v", err)
	}
	want := []string{
		"alertmanager", "grafana", "loki_coordinator", "loki_worker",
		"mimir_coordinator", "mimir_worker", "tempo_coordinator", "tempo_worker",
	}
	if len(got) != len(want) {
		t.Fatalf("PresetKeys = %v, want %d keys", got, len(want))
	}
	for i, k := range want {
		if got[i] != k {
			t.Errorf("key %d = %q, want %q", i, got[i], k)
		}
	}
	if _, err := PresetKeys("does-not-exist"); err == nil {
		t.Error("PresetKeys must fail for an unknown preset")
	}
}

func TestEntry_Coverage(t *testing.T) {
	cases := []struct {
		name          string
		entry         Entry
		required      []string
		wantUncovered []string
		wantStale     []string
	}{
		{
			name:     "covered by presets and requires",
			entry:    Entry{Presets: []string{"cos-single-unit", "cos-no-ingress"}, Requires: []string{"s3_access_key", "s3_endpoint"}},
			required: []string{"s3_access_key", "s3_endpoint", "alertmanager", "ingress"},
		},
		{
			name:          "required input nobody supplies",
			entry:         Entry{Requires: []string{"model"}},
			required:      []string{"model", "storage_backend"},
			wantUncovered: []string{"storage_backend"},
		},
		{
			name:      "requires the module no longer declares",
			entry:     Entry{Requires: []string{"model", "old_input"}},
			required:  []string{"model"},
			wantStale: []string{"old_input"},
		},
		{
			name:     "a value in requires still covers by name",
			entry:    Entry{Requires: []string{"storage_backend=storage-class"}},
			required: []string{"storage_backend"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uncovered, stale, err := c.entry.Coverage(c.required)
			if err != nil {
				t.Fatalf("Coverage: %v", err)
			}
			if len(uncovered) != len(c.wantUncovered) {
				t.Fatalf("uncovered = %v, want %v", uncovered, c.wantUncovered)
			}
			for i, u := range c.wantUncovered {
				if uncovered[i] != u {
					t.Errorf("uncovered[%d] = %q, want %q", i, uncovered[i], u)
				}
			}
			if len(stale) != len(c.wantStale) {
				t.Fatalf("stale = %v, want %v", stale, c.wantStale)
			}
			for i, s := range c.wantStale {
				if stale[i] != s {
					t.Errorf("stale[%d] = %q, want %q", i, stale[i], s)
				}
			}
		})
	}
}

// The gallery's own entries must be consistent with what their modules declare;
// otherwise `atelier apply <name>` fails before it can plan. This reads the
// embedded presets and requires only — it does not clone — so it catches the
// manifest-level half of the drift.
func TestShippedEntriesCoverTheirPresets(t *testing.T) {
	entries, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range entries {
		if len(e.Presets) == 0 {
			continue
		}
		for _, p := range e.Presets {
			if _, err := PresetKeys(p); err != nil {
				t.Errorf("%s: %v", e.Name, err)
			}
		}
	}
}
