package main

import (
	"bytes"
	"testing"

	"github.com/MichaelThamm/atelier/internal/gallery"
)

// TestRenderGallery pins the human listing: the name column is padded to the
// longest name, and the apply command aligns under the description.
func TestRenderGallery(t *testing.T) {
	entries := []gallery.Entry{
		{Name: "aa", Description: "first"},
		{Name: "bbbb", Description: "second"},
	}
	var buf bytes.Buffer
	if err := renderGallery(&buf, entries, false); err != nil {
		t.Fatal(err)
	}
	want := "aa    first\n      atelier apply aa\n\nbbbb  second\n      atelier apply bbbb\n"
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
