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

func TestListAllVarFiles_includesGallery(t *testing.T) {
	got := ListAllVarFiles(t.TempDir(), "", "")
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
