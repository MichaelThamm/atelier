package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeModule creates a local module directory with one variable, returning its
// path for use as a local-path source.
func writeModule(t *testing.T, dir, varName string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "variable \"" + varName + "\" {\n  type    = string\n  default = \"x\"\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "variables.tf"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadSecondaryModules_skipsPrimaryAndSourceless(t *testing.T) {
	primaryDir := writeModule(t, filepath.Join(t.TempDir(), "primary"), "primary_var")
	secondDir := writeModule(t, filepath.Join(t.TempDir(), "second"), "second_var")

	wrapperDir := t.TempDir()
	mainTF := `module "primary" {
  source = "` + primaryDir + `"
}
module "second" {
  source = "` + secondDir + `"
}
module "no_source" {
}
`
	if err := os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(mainTF), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := &BlockLoader{}
	mods, warnings, err := LoadSecondaryModules(context.Background(), wrapperDir, "primary", loader)
	if err != nil {
		t.Fatalf("LoadSecondaryModules: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(mods) != 1 {
		t.Fatalf("got %d modules, want 1 (primary and sourceless must be skipped)", len(mods))
	}
	if mods[0].Name != "second" {
		t.Errorf("module name = %q, want second", mods[0].Name)
	}
	if mods[0].State == nil || len(mods[0].State.Vars) != 1 {
		t.Fatalf("second module state not loaded: %+v", mods[0].State)
	}
	if mods[0].State.Vars[0].Name != "second_var" {
		t.Errorf("loaded wrong schema: %v", mods[0].State.Vars[0].Name)
	}
}

func TestLoadSecondaryModules_preservesMainTFOrder(t *testing.T) {
	dirA := writeModule(t, filepath.Join(t.TempDir(), "a"), "a")
	dirB := writeModule(t, filepath.Join(t.TempDir(), "b"), "b")
	dirC := writeModule(t, filepath.Join(t.TempDir(), "c"), "c")

	wrapperDir := t.TempDir()
	// Declared out of alphabetical order; the loader must preserve this order,
	// not sort by name or finish-time.
	mainTF := `module "a" {
  source = "` + dirA + `"
}
module "b" {
  source = "` + dirB + `"
}
module "c" {
  source = "` + dirC + `"
}
`
	if err := os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(mainTF), 0o644); err != nil {
		t.Fatal(err)
	}

	mods, _, err := LoadSecondaryModules(context.Background(), wrapperDir, "", &BlockLoader{})
	if err != nil {
		t.Fatalf("LoadSecondaryModules: %v", err)
	}
	if len(mods) != 3 {
		t.Fatalf("got %d modules, want 3", len(mods))
	}
	for i, want := range []string{"a", "b", "c"} {
		if mods[i].Name != want {
			t.Errorf("position %d = %q, want %q", i, mods[i].Name, want)
		}
	}
}

// A block whose source cannot be loaded is reported as a warning and omitted,
// so one bad module does not hide the rest.
func TestLoadSecondaryModules_warnsOnUnloadableBlock(t *testing.T) {
	good := writeModule(t, filepath.Join(t.TempDir(), "good"), "good")

	wrapperDir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	mainTF := `module "bad" {
  source = "` + missing + `"
}
module "good" {
  source = "` + good + `"
}
`
	if err := os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(mainTF), 0o644); err != nil {
		t.Fatal(err)
	}

	mods, warnings, err := LoadSecondaryModules(context.Background(), wrapperDir, "", &BlockLoader{})
	if err != nil {
		t.Fatalf("LoadSecondaryModules: %v", err)
	}
	if len(mods) != 1 || mods[0].Name != "good" {
		t.Fatalf("got %+v, want only the loadable module", mods)
	}
	if len(warnings) != 1 {
		t.Errorf("got %d warnings, want 1 for the unloadable block", len(warnings))
	}
}
