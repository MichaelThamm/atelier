package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveVarFile_moduleAndRepoExamples(t *testing.T) {
	clone := t.TempDir()
	mod := filepath.Join(clone, "terraform", "cos")
	writeAt(t, filepath.Join(mod, "examples", "s3.tfvars"), "s3_endpoint = \"x\"\n")
	writeAt(t, filepath.Join(clone, "terraform", "examples", "shared.tfvars"), "a = 1\n")

	got, ok := ResolveVarFile("", clone, "terraform/cos", "s3")
	if !ok {
		t.Fatal("s3 not resolved from <module>/examples")
	}
	if got != filepath.Join(mod, "examples", "s3.tfvars") {
		t.Errorf("got %s", got)
	}
	if _, ok := ResolveVarFile("", clone, "terraform/cos", "s3.tfvars"); !ok {
		t.Error("extension should be optional")
	}

	got, ok = ResolveVarFile("", clone, "terraform/cos", "shared")
	if !ok {
		t.Fatal("shared not resolved from <repo>/terraform/examples")
	}
	if !strings.HasSuffix(got, filepath.Join("terraform", "examples", "shared.tfvars")) {
		t.Errorf("got %s", got)
	}

	if _, ok := ResolveVarFile("", clone, "terraform/cos", "missing"); ok {
		t.Error("missing name should not resolve")
	}
}

func TestResolveVarFile_localPathWins(t *testing.T) {
	local := filepath.Join(t.TempDir(), "s3.tfvars")
	writeAt(t, local, "x = 1\n")

	clone := t.TempDir()
	writeAt(t, filepath.Join(clone, "terraform", "cos", "examples", "s3.tfvars"), "y = 2\n")

	got, ok := ResolveVarFile("", clone, "terraform/cos", local)
	if !ok || got != local {
		t.Fatalf("got %q ok=%v, want the local path %q", got, ok, local)
	}
}

// TestLocalVarFiles_walkUpNearestWins pins the replacement for the walk-up
// atelier.local.yaml: a nearer atelier.presets/ overrides a shared ancestor.
func TestLocalVarFiles_walkUpNearestWins(t *testing.T) {
	base := t.TempDir()
	wrap := filepath.Join(base, "cos-lite")
	sharedA := filepath.Join(base, "atelier.presets", "cos-s3.tfvars")
	nearA := filepath.Join(wrap, "atelier.presets", "cos-s3.tfvars")
	writeAt(t, sharedA, "x = 1\n")
	writeAt(t, nearA, "x = 2\n")
	writeAt(t, filepath.Join(base, "atelier.presets", "cos-units.tfvars"), "y = 1\n")

	files := LocalVarFiles(wrap)
	byName := map[string]VarFile{}
	for _, f := range files {
		byName[f.Name] = f
	}
	if _, ok := byName["cos-units"]; !ok {
		t.Errorf("shared ancestor bundle cos-units not discovered: %+v", files)
	}
	if got := byName["cos-s3"].Path; got != nearA {
		t.Errorf("nearest atelier.presets should win: got %s, want %s", got, nearA)
	}
}

// TestResolveVarFile_localWalkUpOverridesRepo: a personal bundle may shadow a
// product example of the same name.
func TestResolveVarFile_localWalkUpOverridesRepo(t *testing.T) {
	base := t.TempDir()
	wrap := filepath.Join(base, "cos")
	local := filepath.Join(base, "atelier.presets", "s3.tfvars")
	writeAt(t, local, "s3_endpoint = \"local\"\n")

	clone := t.TempDir()
	repoFile := filepath.Join(clone, "terraform", "cos", "examples", "s3.tfvars")
	writeAt(t, repoFile, "s3_endpoint = \"repo\"\n")

	got, ok := ResolveVarFile(wrap, clone, "terraform/cos", "s3")
	if !ok || got != local {
		t.Fatalf("got %q ok=%v, want the local bundle %q", got, ok, local)
	}
}

func TestResolveVarFiles_errorListsAvailable(t *testing.T) {
	clone := t.TempDir()
	mod := filepath.Join(clone, "terraform", "cos")
	writeAt(t, filepath.Join(mod, "examples", "s3.tfvars"), "x = 1\n")
	writeAt(t, filepath.Join(mod, "examples", "units.tfvars"), "y = 2\n")

	_, err := ResolveVarFiles("", clone, "terraform/cos", []string{"missing"})
	if err == nil {
		t.Fatal("expected an error for a missing name")
	}
	for _, want := range []string{"s3", "units"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should list %q; got: %s", want, err)
		}
	}
}

func TestListAllVarFiles_bothSources(t *testing.T) {
	base := t.TempDir()
	wrap := filepath.Join(base, "cos")
	writeAt(t, filepath.Join(base, "atelier.presets", "personal.tfvars"), "x = 1\n")

	clone := t.TempDir()
	writeAt(t, filepath.Join(clone, "terraform", "cos", "examples", "s3.tfvars"), "y = 1\n")
	writeAt(t, filepath.Join(clone, "terraform", "cos", "examples", ".terraform", "v.tfvars"), "z = 1\n")
	writeAt(t, filepath.Join(clone, "terraform", "cos", ".git", "x.tfvars"), "z = 1\n")

	got := ListAllVarFiles(wrap, clone, "terraform/cos")
	sources := map[string]string{}
	for _, f := range got {
		sources[f.Name] = f.Source
	}
	if sources["personal"] != "local" {
		t.Errorf("personal bundle not listed as local: %+v", got)
	}
	if sources["s3"] != "repo" {
		t.Errorf("repo example not listed as repo: %+v", got)
	}
	if _, ok := sources["v"]; ok {
		t.Errorf("scratch dirs must be skipped: %+v", got)
	}
}

func TestValidVarFileRef_rejectsEscapes(t *testing.T) {
	for _, ref := range []string{"../secret.tfvars", "/etc/passwd", ".", ".."} {
		if validVarFileRef(ref) {
			t.Errorf("validVarFileRef(%q) = true, want false", ref)
		}
	}
	for _, ref := range []string{"s3", "s3.tfvars", "examples/s3.tfvars"} {
		if !validVarFileRef(ref) {
			t.Errorf("validVarFileRef(%q) = false, want true", ref)
		}
	}
}
