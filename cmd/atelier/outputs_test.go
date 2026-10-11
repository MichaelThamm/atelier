package main

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
	if err := regenerateOutputs(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "outputs.tf"))
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	for _, want := range []string{`output "m_greeting"`, "value = module.m.greeting", `output "m_secret"`, "sensitive = true"} {
		if !strings.Contains(out, want) {
			t.Errorf("outputs.tf missing %q, got:\n%s", want, out)
		}
	}
}

func TestRegenerateOutputs_moduleNotAvailableWritesNoFile(t *testing.T) {
	dir := t.TempDir()
	// A remote module with no clone present: nothing to re-export.
	if err := os.WriteFile(filepath.Join(dir, "main.tf"),
		[]byte("module \"r\" {\n  source = \"git::https://github.com/o/r.git//m?ref=v1\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := regenerateOutputs(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "outputs.tf")); !os.IsNotExist(err) {
		t.Errorf("no outputs.tf expected when the module is not cloned, err=%v", err)
	}
}
