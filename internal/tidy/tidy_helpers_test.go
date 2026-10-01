package tidy

import (
	"testing"
)

// --- normalize ---

func TestNormalize_TrailingWhitespacePerLine(t *testing.T) {
	in := []byte("line1   \nline2\t\nline3\n")
	got := string(normalize(in))
	want := "line1\nline2\nline3"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalize_TrailingNewlineOnly(t *testing.T) {
	in := []byte("content\n")
	got := string(normalize(in))
	if got != "content" {
		t.Errorf("got %q, want content", got)
	}
}

func TestNormalize_NoTrailingNewline(t *testing.T) {
	in := []byte("content")
	got := string(normalize(in))
	if got != "content" {
		t.Errorf("got %q, want content", got)
	}
}

func TestNormalize_CRLF(t *testing.T) {
	in := []byte("line1\r\nline2\r\n")
	got := string(normalize(in))
	if got != "line1\nline2" {
		t.Errorf("got %q", got)
	}
}

func TestNormalize_Empty(t *testing.T) {
	got := string(normalize([]byte("")))
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestNormalize_MultipleTrailingNewlines(t *testing.T) {
	in := []byte("line1\n\n\n")
	got := string(normalize(in))
	if got != "line1" {
		t.Errorf("got %q, want line1", got)
	}
}

func TestNormalize_PreservesLeadingWhitespace(t *testing.T) {
	in := []byte("  indented   \n\talso\t   \n")
	got := string(normalize(in))
	want := "  indented\n\talso"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- countModuleBlocks ---

func TestCountModuleBlocks_SingleModule(t *testing.T) {
	src := []byte(`module "cos" {
  source = "./cos"
}`)
	got, err := countModuleBlocks(src, "main.tf")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

func TestCountModuleBlocks_MultipleModules(t *testing.T) {
	src := []byte(`
module "a" { source = "./a" }
module "b" { source = "./b" }
module "c" { source = "./c" }
`)
	got, err := countModuleBlocks(src, "main.tf")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != 3 {
		t.Errorf("got %d, want 3", got)
	}
}

func TestCountModuleBlocks_NoModules(t *testing.T) {
	src := []byte(`
resource "null_resource" "x" {}
variable "name" { type = string }
`)
	got, err := countModuleBlocks(src, "main.tf")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestCountModuleBlocks_EmptyFile(t *testing.T) {
	got, err := countModuleBlocks([]byte(""), "empty.tf")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestCountModuleBlocks_InvalidHCL(t *testing.T) {
	_, err := countModuleBlocks([]byte(`{{{`), "bad.tf")
	if err == nil {
		t.Error("expected error for invalid HCL")
	}
}

func TestCountModuleBlocks_MixedBlocks(t *testing.T) {
	src := []byte(`
module "a" { source = "./a" }
resource "null_resource" "x" {}
module "b" { source = "./b" }
locals { x = 1 }
`)
	got, err := countModuleBlocks(src, "main.tf")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}
