package wrapper

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// --- rangeBytes ---

func TestRangeBytes_Normal(t *testing.T) {
	src := []byte("hello world")
	r := hcl.Range{
		Start: hcl.Pos{Byte: 6},
		End:   hcl.Pos{Byte: 11},
	}
	got := rangeBytes(src, r)
	if string(got) != "world" {
		t.Errorf("got %q, want world", got)
	}
}

func TestRangeBytes_ZeroLength(t *testing.T) {
	src := []byte("hello")
	r := hcl.Range{
		Start: hcl.Pos{Byte: 3},
		End:   hcl.Pos{Byte: 3},
	}
	got := rangeBytes(src, r)
	if len(got) != 0 {
		t.Errorf("got %d bytes, want 0", len(got))
	}
}

func TestRangeBytes_InvertedRange(t *testing.T) {
	src := []byte("hello")
	r := hcl.Range{
		Start: hcl.Pos{Byte: 5},
		End:   hcl.Pos{Byte: 2},
	}
	got := rangeBytes(src, r)
	if got != nil {
		t.Errorf("got %v, want nil for inverted range", got)
	}
}

func TestRangeBytes_NegativeStart(t *testing.T) {
	src := []byte("hello")
	r := hcl.Range{
		Start: hcl.Pos{Byte: -1},
		End:   hcl.Pos{Byte: 3},
	}
	got := rangeBytes(src, r)
	if got != nil {
		t.Errorf("got %v, want nil for negative start", got)
	}
}

func TestRangeBytes_EndBeyondLength(t *testing.T) {
	src := []byte("hello")
	r := hcl.Range{
		Start: hcl.Pos{Byte: 3},
		End:   hcl.Pos{Byte: 100},
	}
	got := rangeBytes(src, r)
	if got != nil {
		t.Errorf("got %v, want nil for end beyond length", got)
	}
}

func TestRangeBytes_FullRange(t *testing.T) {
	src := []byte("hello")
	r := hcl.Range{
		Start: hcl.Pos{Byte: 0},
		End:   hcl.Pos{Byte: 5},
	}
	got := rangeBytes(src, r)
	if string(got) != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

func TestRangeBytes_DoesNotShareBackingArray(t *testing.T) {
	src := []byte("hello world")
	r := hcl.Range{
		Start: hcl.Pos{Byte: 0},
		End:   hcl.Pos{Byte: 5},
	}
	got := rangeBytes(src, r)
	// Mutate the source — the returned slice should be unaffected.
	src[0] = 'X'
	if string(got) != "hello" {
		t.Errorf("got %q after src mutation — shares backing array", got)
	}
}

// --- ReadModuleBlocks ---

func TestReadModuleBlocks_NoFile(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadModuleBlocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestReadModuleBlocks_NoModuleBlocks(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
resource "null_resource" "x" {}
`), 0644)
	got, err := ReadModuleBlocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d blocks, want 0", len(got))
	}
}

func TestReadModuleBlocks_SingleBlock(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
module "cos_lite" {
  source = "git::https://github.com/org/repo.git//cos-lite?ref=v1"
}
`), 0644)
	got, err := ReadModuleBlocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d blocks, want 1", len(got))
	}
	if got[0].Name != "cos_lite" {
		t.Errorf("name: got %q", got[0].Name)
	}
	if got[0].Source != "git::https://github.com/org/repo.git//cos-lite?ref=v1" {
		t.Errorf("source: got %q", got[0].Source)
	}
}

func TestReadModuleBlocks_MultipleBlocks(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
module "a" {
  source = "./a"
}
module "b" {
  source = "./b"
}
module "c" {
  source = "./c"
}
`), 0644)
	got, err := ReadModuleBlocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d blocks, want 3", len(got))
	}
	names := map[string]bool{}
	for _, b := range got {
		names[b.Name] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !names[want] {
			t.Errorf("missing block %q", want)
		}
	}
}

// --- ReadMainForBlock ---

func TestReadMainForBlock_Found(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
module "grafana" {
  source  = "git::https://github.com/org/repo.git//grafana?ref=v2"
  enabled = true
}
module "prom" {
  source = "./prom"
}
`), 0644)
	vars := []tfvars.Variable{
		{Name: "enabled", HasDefault: true, Default: cty.BoolVal(false)},
	}
	got, err := ReadMainForBlock(dir, "grafana", vars)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.ModuleBlockName != "grafana" {
		t.Errorf("block name: got %q", got.ModuleBlockName)
	}
	if got.Source != "git::https://github.com/org/repo.git//grafana?ref=v2" {
		t.Errorf("source: got %q", got.Source)
	}
	if v, ok := got.Values["enabled"]; !ok || v.True() != true {
		t.Errorf("enabled: got %v, want true", v)
	}
}

func TestReadMainForBlock_NotFound(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
module "a" {
  source = "./a"
}
`), 0644)
	got, err := ReadMainForBlock(dir, "nonexistent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got %v, want nil for missing block", got)
	}
}

func TestReadMainForBlock_NoFile(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadMainForBlock(dir, "anything", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

// --- rawAttr / rangeBytes integration ---

func TestRawAttr_PreservesExpression(t *testing.T) {
	src := []byte(`module "x" {
  source = "./foo"
  custom = "hello"
}`)
	parser := hclparse.NewParser()
	f, diags := parser.ParseHCL(src, "main.tf")
	if diags.HasErrors() {
		t.Fatal(diags)
	}
	body := f.Body.(*hclsyntax.Body)
	for _, block := range body.Blocks {
		for _, attr := range block.Body.Attributes {
			if attr.Name == "custom" {
				ra := rawAttr(src, attr)
				if string(ra.RawExpr) != `"hello"` {
					t.Errorf("RawExpr: got %q, want %q", string(ra.RawExpr), `"hello"`)
				}
			}
		}
	}
}
