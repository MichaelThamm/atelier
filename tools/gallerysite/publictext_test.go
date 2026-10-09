package main

import (
	"os"
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/gallery"
)

// siteIndexPath is the hand-written home page, relative to this package.
const siteIndexPath = "../../website/docs/index.md"

// The generated site is read by people who have never seen this repository, so
// no page may name an internal artefact. The gallery page carries the same
// content `atelier gallery list` prints, plus the Juju variants; it is held to
// the rule. See the "Public-facing output" convention in AGENTS.md.
func TestPublicFacingSite_hasNoInternalReferences(t *testing.T) {
	entries, err := gallery.List()
	if err != nil {
		t.Fatal(err)
	}
	var page strings.Builder
	if err := render(&page, entries); err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, ref := range []string{
		"ADR-", "docs/adr", "docs/SPEC", "SPEC.md", "docs/ROADMAP", "internal/",
	} {
		if strings.Contains(page.String(), ref) {
			t.Errorf("the gallery page mentions %q", ref)
		}
	}
}

// The site's quick start must show the two entry points separately: `atelier
// apply` deploys, `atelier add` configures in the TUI. Collapsing them into one
// command sends a reader to the deploy path when they asked to configure.
func TestSiteIndex_documentsBothEntryPoints(t *testing.T) {
	index := readSiteIndex(t)
	for _, want := range []string{"atelier gallery list", "atelier apply", "atelier add"} {
		if !strings.Contains(index, want) {
			t.Errorf("the home page's quick start does not mention %q", want)
		}
	}
	if !strings.Contains(index, "TUI") {
		t.Error("the quick start should say that `atelier add` opens the TUI")
	}
}

// The home page is hand-written rather than generated, so it needs the same
// internal-reference check as the generated pages.
func TestSiteIndex_hasNoInternalReferences(t *testing.T) {
	index := readSiteIndex(t)
	for _, ref := range []string{
		"ADR-", "docs/adr", "docs/SPEC", "SPEC.md", "docs/ROADMAP", "internal/",
	} {
		if strings.Contains(index, ref) {
			t.Errorf("the home page mentions %q", ref)
		}
	}
}

func readSiteIndex(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(siteIndexPath)
	if err != nil {
		t.Fatalf("read %s: %v", siteIndexPath, err)
	}
	return string(data)
}
