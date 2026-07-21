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

// --- isLocalSource ---

func TestIsLocalSource_DotSlash(t *testing.T) {
	if !isLocalSource("./local") {
		t.Error("expected true for ./")
	}
}

func TestIsLocalSource_DotDotSlash(t *testing.T) {
	if !isLocalSource("../parent") {
		t.Error("expected true for ../")
	}
}

func TestIsLocalSource_Absolute(t *testing.T) {
	if !isLocalSource("/absolute/path") {
		t.Error("expected true for /")
	}
}

func TestIsLocalSource_GitURL(t *testing.T) {
	if isLocalSource("git::https://example.com/repo.git?ref=v1.0") {
		t.Error("expected false for git URL")
	}
}

func TestIsLocalSource_Registry(t *testing.T) {
	if isLocalSource("registry.terraform.io/hashicorp/aws") {
		t.Error("expected false for registry")
	}
}

func TestIsLocalSource_Empty(t *testing.T) {
	if isLocalSource("") {
		t.Error("expected false for empty")
	}
}

func TestIsLocalSource_GitHubURL(t *testing.T) {
	if isLocalSource("github.com/hashicorp/terraform-aws/modules/vpc?ref=v5.0") {
		t.Error("expected false for GitHub URL")
	}
}

func TestIsLocalSource_SlashButNotRelative(t *testing.T) {
	if isLocalSource("/absolute") {
		// This IS local source (starts with /)
		return
	}
	t.Error("/absolute should be local source")
}

// --- isHexSHA ---

func TestIsHexSHA_Valid40Chars(t *testing.T) {
	if !isHexSHA("a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2") {
		t.Error("expected true")
	}
}

func TestIsHexSHA_UpperCase(t *testing.T) {
	if isHexSHA("A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2") {
		t.Error("expected false (uppercase)")
	}
}

func TestIsHexSHA_TooShort(t *testing.T) {
	if isHexSHA("a1b2c3") {
		t.Error("expected false (too short)")
	}
}

func TestIsHexSHA_TooLong(t *testing.T) {
	if isHexSHA("a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b20") {
		t.Error("expected false (too long)")
	}
}

func TestIsHexSHA_InvalidChars(t *testing.T) {
	if isHexSHA("g1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2") {
		t.Error("expected false (g is not hex)")
	}
}

func TestIsHexSHA_Empty(t *testing.T) {
	if isHexSHA("") {
		t.Error("expected false for empty")
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
