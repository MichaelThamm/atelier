package main

import (
	"bytes"
	"testing"

	"github.com/MichaelThamm/atelier/internal/gallery"
)

// TestRenderGallery pins the human listing: name and description, the module
// with its subdirectory and short ref, and the apply command.
func TestRenderGallery(t *testing.T) {
	entries := []gallery.Entry{
		{
			Name: "aa", Description: "first",
			Module: "https://example.com/m", Subdir: "tf", Ref: "0123456789abcdef0123",
		},
	}
	var buf bytes.Buffer
	if err := renderGallery(&buf, entries, false); err != nil {
		t.Fatal(err)
	}
	want := "aa  first\n" +
		"    https://example.com/m//tf  @0123456789ab\n" +
		"    atelier apply aa\n"
	if got := buf.String(); got != want {
		t.Errorf("renderGallery:\n%q\nwant:\n%q", got, want)
	}
}

// A long command wraps one --var per line, each continued with a backslash so
// the whole thing still copies and pastes as one shell line.
func TestRenderGallery_wrapsRequires(t *testing.T) {
	entries := []gallery.Entry{
		{Name: "loki-operators", Description: "Loki", Module: "https://example.com/l", Ref: "abcdef0123456789", Requires: []string{"model_uuid", "s3_endpoint"}},
	}
	var buf bytes.Buffer
	if err := renderGallery(&buf, entries, false); err != nil {
		t.Fatal(err)
	}
	want := "loki-operators  Loki\n" +
		"    https://example.com/l  @abcdef012345\n" +
		"    atelier apply loki-operators \\\n" +
		"        --var model_uuid=<model_uuid> \\\n" +
		"        --var s3_endpoint=<s3_endpoint>\n"
	if got := buf.String(); got != want {
		t.Errorf("renderGallery:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderGallery_commands(t *testing.T) {
	entries := []gallery.Entry{{Name: "x"}}
	var buf bytes.Buffer
	if err := renderGallery(&buf, entries, true); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "atelier module add x --strict --yes\n"; got != want {
		t.Errorf("renderGallery --commands = %q, want %q", got, want)
	}
}
