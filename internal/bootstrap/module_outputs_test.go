package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegenerateOutputs_reExportsModuleOutputs(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.tf", "module \"m\" {\n  source = \"./m\"\n}\n")
	write("m/outputs.tf", `output "greeting" {
  type  = string
  value = "hi"
}
output "secret" {
  type      = string
  sensitive = true
  value     = "shh"
}
`)
	if err := RegenerateOutputs(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "outputs.tf"))
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	for _, want := range []string{`output "m_greeting"`, "module.m.greeting", `output "m_secret"`, "sensitive = true"} {
		if !strings.Contains(out, want) {
			t.Errorf("outputs.tf missing %q, got:\n%s", want, out)
		}
	}
}

func TestRegenerateOutputs_skipsARepoPinnedAtTwoRefs(t *testing.T) {
	dir := t.TempDir()
	// A clone exists and declares an output, but two blocks point the same repo
	// at different refs; one clone serves only one of them, so nothing is
	// re-exported rather than guessing.
	clone := filepath.Join(dir, ".atelier", "clone", "r", "m")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "outputs.tf"),
		[]byte("output \"x\" {\n  value = \"1\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(
		"module \"a\" {\n  source = \"git::https://github.com/o/r.git//m?ref=v1\"\n}\n"+
			"module \"b\" {\n  source = \"git::https://github.com/o/r.git//m?ref=v2\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RegenerateOutputs(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "outputs.tf")); !os.IsNotExist(err) {
		t.Errorf("no outputs.tf expected when one clone serves two refs, err=%v", err)
	}
}

func TestRegenerateOutputs_moduleNotAvailableWritesNoFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"),
		[]byte("module \"r\" {\n  source = \"git::https://github.com/o/r.git//m?ref=v1\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RegenerateOutputs(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "outputs.tf")); !os.IsNotExist(err) {
		t.Errorf("no outputs.tf expected when the module is not cloned, err=%v", err)
	}
}
