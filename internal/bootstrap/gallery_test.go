package bootstrap

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveVarFile_gallery(t *testing.T) {
	// The gallery resolves with no local or repo source available.
	got, ok := ResolveVarFile("", "", "", "cos-no-ingress")
	if !ok {
		t.Fatal("a gallery preset must resolve with no local or repo source")
	}
	if !strings.HasSuffix(got, "cos-no-ingress.tfvars") {
		t.Errorf("got %q", got)
	}
}

func TestResolveVarFile_localShadowsGallery(t *testing.T) {
	wrap := t.TempDir()
	local := filepath.Join(wrap, "atelier.presets", "cos-no-ingress.tfvars")
	writeAt(t, local, "ingress = {}\n")

	got, ok := ResolveVarFile(wrap, "", "", "cos-no-ingress")
	if !ok {
		t.Fatal("did not resolve")
	}
	if got != local {
		t.Errorf("a local bundle must win over the gallery: got %q, want %q", got, local)
	}
}

// TestGalleryVarFiles_descriptionsAreOneSentence pins the text a gallery preset
// puts in the picker row: its leading comment, read as the description. One
// sentence keeps the row one line, so any rationale about the values belongs
// below them, which the picker never reads.
func TestGalleryVarFiles_descriptionsAreOneSentence(t *testing.T) {
	files := GalleryVarFiles()
	if len(files) == 0 {
		t.Fatal("no gallery presets resolved")
	}
	for _, f := range files {
		switch {
		case strings.Contains(f.Description, ". "):
			t.Errorf("%s: more than one sentence: %q", f.Name, f.Description)
		case strings.Contains(f.Description, "  "):
			t.Errorf("%s: a second paragraph reached the description: %q", f.Name, f.Description)
		case len(f.Description) > 100:
			t.Errorf("%s: %d characters is too long for one row: %q", f.Name, len(f.Description), f.Description)
		}
	}
}

func TestListAllVarFiles_includesGallery(t *testing.T) {
	got := ListAllVarFiles(t.TempDir(), "", "", nil)
	for _, f := range got {
		if f.Source == "gallery" && f.Name == "cos-no-ingress" {
			if f.Path == "" {
				t.Error("gallery entry has no materialized path")
			}
			return
		}
	}
	t.Errorf("gallery preset missing from %+v", got)
}
