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
	if got, want := e.ScaffoldCommand(), "atelier add x --strict --yes"; got != want {
		t.Errorf("ScaffoldCommand = %q, want %q", got, want)
	}
}

func TestEntry_requiresAppendedToApply(t *testing.T) {
	e := Entry{Name: "x", Requires: []string{"model_uuid", "s3_endpoint"}}
	want := "atelier apply x --var model_uuid=<model_uuid> --var s3_endpoint=<s3_endpoint>"
	if got := e.ApplyCommand(); got != want {
		t.Errorf("ApplyCommand = %q, want %q", got, want)
	}
	// The scaffold form CI runs omits them: CI cannot supply deployment-specific
	// values, and the entry's static presets must still be validated.
	if got, want := e.ScaffoldCommand(), "atelier add x --strict --yes"; got != want {
		t.Errorf("ScaffoldCommand = %q, want %q", got, want)
	}
}

func TestEntry_shortRef(t *testing.T) {
	if got, want := (Entry{Ref: "0123456789abcdef0123"}).ShortRef(), "0123456789ab"; got != want {
		t.Errorf("ShortRef = %q, want %q", got, want)
	}
	if got, want := (Entry{Ref: "abc"}).ShortRef(), "abc"; got != want {
		t.Errorf("ShortRef = %q, want %q", got, want)
	}
}

func TestEntry_requiresWithValueRendersDefault(t *testing.T) {
	// A `name=value` requires entry carries a working default: it renders that
	// value rather than a placeholder, and RequiresNames still reports the name.
	e := Entry{Name: "x", Requires: []string{"storage_backend=storage-class", "model"}}
	want := "atelier apply x --var storage_backend=storage-class --var model=<model>"
	if got := e.ApplyCommand(); got != want {
		t.Errorf("ApplyCommand = %q, want %q", got, want)
	}
	got := e.RequiresNames()
	if len(got) != 2 || got[0] != "storage_backend" || got[1] != "model" {
		t.Errorf("RequiresNames = %v, want [storage_backend model]", got)
	}
}

func TestFind(t *testing.T) {
	e, ok := Find("cos")
	if !ok || e.Module == "" {
		t.Errorf("Find(cos) = %+v, %v", e, ok)
	}
	if _, ok := Find("does-not-exist"); ok {
		t.Error("Find must not match an unknown name")
	}
}

func TestLookup(t *testing.T) {
	data, ok := Lookup("cos-single-unit")
	if !ok {
		t.Fatal("cos-single-unit is not embedded")
	}
	if !strings.Contains(string(data), "loki_coordinator") {
		t.Errorf("unexpected preset contents:\n%s", data)
	}
	if _, ok := Lookup("does-not-exist"); ok {
		t.Error("an unknown preset must not resolve")
	}
}

func TestPresetPath_materializes(t *testing.T) {
	path, ok := PresetPath("cos-no-ingress")
	if !ok {
		t.Fatal("PresetPath returned false for an embedded preset")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("materialized preset missing: %v", err)
	}
	if filepath.Base(path) != "cos-no-ingress.tfvars" {
		t.Errorf("path = %q, want a cos-no-ingress.tfvars basename", path)
	}
	if _, ok := PresetPath("does-not-exist"); ok {
		t.Error("an unknown preset must not have a path")
	}
}
