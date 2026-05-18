package wrapper

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverOutputNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "outputs.tf"), []byte(`
output "offers" {
  value = "x"
}
output "components" {
  value = "y"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A non-output block should be ignored.
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
module "cos_lite" {
  source = "./foo"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	names, err := DiscoverOutputNames(dir)
	if err != nil {
		t.Fatalf("DiscoverOutputNames: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 outputs, got %d: %v", len(names), names)
	}
	if names[0] != "components" || names[1] != "offers" {
		t.Errorf("expected [components offers], got %v", names)
	}
}

func TestDiscoverOutputNames_noOutputs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
module "x" { source = "./y" }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	names, err := DiscoverOutputNames(dir)
	if err != nil {
		t.Fatalf("DiscoverOutputNames: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected 0 outputs, got %v", names)
	}
}

func TestBootstrapOutputs(t *testing.T) {
	dir := t.TempDir()
	opts := BootstrapOptions{
		Dir:             dir,
		ModuleBlockName: "cos_lite",
		Source:          "git::https://example.com//tf/cos-lite",
		OutputNames:     []string{"components", "offers"},
	}
	if err := bootstrapOutputs(opts); err != nil {
		t.Fatalf("bootstrapOutputs: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, OutputsTF))
	if err != nil {
		t.Fatalf("read outputs.tf: %v", err)
	}
	want := `output "components" {
  value = module.cos_lite.components
}

output "offers" {
  value = module.cos_lite.offers
}
`
	if string(got) != want {
		t.Errorf("outputs.tf mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestBootstrapOutputs_noOutputs(t *testing.T) {
	dir := t.TempDir()
	opts := BootstrapOptions{
		Dir:             dir,
		ModuleBlockName: "x",
		Source:          "./y",
	}
	if err := bootstrapOutputs(opts); err != nil {
		t.Fatalf("bootstrapOutputs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, OutputsTF)); !os.IsNotExist(err) {
		t.Error("outputs.tf should not be created when there are no outputs")
	}
}

func TestBootstrapOutputs_doesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	existing := []byte(`output "custom" { value = "keep" }`)
	if err := os.WriteFile(filepath.Join(dir, OutputsTF), existing, 0o644); err != nil {
		t.Fatal(err)
	}
	opts := BootstrapOptions{
		Dir:             dir,
		ModuleBlockName: "x",
		Source:          "./y",
		OutputNames:     []string{"a"},
	}
	if err := bootstrapOutputs(opts); err != nil {
		t.Fatalf("bootstrapOutputs: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, OutputsTF))
	if string(got) != string(existing) {
		t.Errorf("outputs.tf was overwritten; got:\n%s", got)
	}
}
