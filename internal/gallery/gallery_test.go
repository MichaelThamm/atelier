package gallery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestList_isValidAndSorted(t *testing.T) {
	entries, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("gallery is empty")
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Name > entries[i].Name {
			t.Errorf("entries not sorted: %q before %q", entries[i-1].Name, entries[i].Name)
		}
	}
}

func TestEntry_commands(t *testing.T) {
	e := Entry{Name: "x"}
	if got, want := e.ApplyCommand(), "atelier apply x"; got != want {
		t.Errorf("ApplyCommand = %q, want %q", got, want)
	}
	if got, want := e.ScaffoldCommand(), "atelier module add x --strict --yes"; got != want {
		t.Errorf("ScaffoldCommand = %q, want %q", got, want)
	}
}

func TestFind(t *testing.T) {
	e, ok := Find("haproxy-product")
	if !ok || e.Module == "" {
		t.Errorf("Find(haproxy-product) = %+v, %v", e, ok)
	}
	if _, ok := Find("does-not-exist"); ok {
		t.Error("Find must not match an unknown name")
	}
}

func TestLookup(t *testing.T) {
	data, ok := Lookup("haproxy-dev")
	if !ok {
		t.Fatal("haproxy-dev is not embedded")
	}
	if !strings.Contains(string(data), "protected_hostnames_configuration") {
		t.Errorf("unexpected preset contents:\n%s", data)
	}
	if _, ok := Lookup("does-not-exist"); ok {
		t.Error("an unknown preset must not resolve")
	}
}

func TestPresetPath_materializes(t *testing.T) {
	path, ok := PresetPath("haproxy-dev")
	if !ok {
		t.Fatal("PresetPath returned false for an embedded preset")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("materialized preset missing: %v", err)
	}
	if filepath.Base(path) != "haproxy-dev.tfvars" {
		t.Errorf("path = %q, want a haproxy-dev.tfvars basename", path)
	}
	if _, ok := PresetPath("does-not-exist"); ok {
		t.Error("an unknown preset must not have a path")
	}
}
