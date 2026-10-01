package wrapper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenameModuleBlock pins the fix for `--as` on a fresh bootstrap: the
// block already exists under the candidate-derived name, so renaming must
// relabel it in place, not append a second block.
func TestRenameModuleBlock(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, MainTF)
	if err := os.WriteFile(mainPath, []byte(`module "product" {
  source = "git::https://example.com/m.git//terraform/product?ref=abc"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &State{Dir: dir, ModuleBlockName: "product"}
	if err := s.RenameModuleBlock("haproxy"); err != nil {
		t.Fatalf("RenameModuleBlock: %v", err)
	}
	if s.ModuleBlockName != "haproxy" {
		t.Errorf("ModuleBlockName = %q, want haproxy", s.ModuleBlockName)
	}

	data, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, `module "haproxy"`) {
		t.Errorf("renamed block missing:\n%s", out)
	}
	if strings.Contains(out, `module "product"`) {
		t.Errorf("old block must be gone, not duplicated:\n%s", out)
	}
	if n := strings.Count(out, "module "); n != 1 {
		t.Errorf("expected exactly one module block, got %d:\n%s", n, out)
	}
}

func TestRenameModuleBlock_missing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, MainTF), []byte(`module "other" {}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &State{Dir: dir, ModuleBlockName: "product"}
	if err := s.RenameModuleBlock("haproxy"); err == nil {
		t.Error("expected an error when the named block is absent")
	}
}
