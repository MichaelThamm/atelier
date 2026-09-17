package main

import (
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/wrapper"
)

const mimirSource = "git::https://github.com/canonical/mimir-operators.git//terraform?ref=main"

func TestFindExistingInstances_exactDuplicate(t *testing.T) {
	existing := []wrapper.ModuleBlockInfo{{Name: "mimir", Source: mimirSource}}
	same, otherRef := findExistingInstances(existing, mimirSource)
	if len(same) != 1 || same[0].Name != "mimir" {
		t.Fatalf("expected the existing mimir block to match; got %+v", same)
	}
	if len(otherRef) != 0 {
		t.Errorf("expected no different-ref matches; got %+v", otherRef)
	}
}

func TestFindExistingInstances_unpinnedDuplicate(t *testing.T) {
	// The case that actually happens: the same command run twice, neither with
	// --ref, so both sources are unpinned.
	src := "git::https://github.com/canonical/mimir-operators.git//terraform"
	existing := []wrapper.ModuleBlockInfo{{Name: "mimir", Source: src}}
	same, _ := findExistingInstances(existing, src)
	if len(same) != 1 {
		t.Fatalf("expected a duplicate; got %+v", same)
	}
}

func TestFindExistingInstances_normalisesSpelling(t *testing.T) {
	// The same module written differently must not read as a different module.
	existing := []wrapper.ModuleBlockInfo{
		{Name: "mimir", Source: "https://GitHub.com/canonical/mimir-operators//terraform?ref=main"},
	}
	same, _ := findExistingInstances(existing, mimirSource)
	if len(same) != 1 {
		t.Fatalf("expected git::/.git/case differences to normalise away; got %+v", same)
	}
}

func TestFindExistingInstances_differentRefIsNotADuplicate(t *testing.T) {
	existing := []wrapper.ModuleBlockInfo{
		{Name: "mimir", Source: "git::https://github.com/canonical/mimir-operators.git//terraform?ref=track/2"},
	}
	same, otherRef := findExistingInstances(existing, mimirSource)
	if len(same) != 0 {
		t.Errorf("a different ref is a supported second instance, not a duplicate; got %+v", same)
	}
	if len(otherRef) != 1 {
		t.Errorf("expected the different-ref block to be reported; got %+v", otherRef)
	}
}

func TestFindExistingInstances_differentSubdirIsNotADuplicate(t *testing.T) {
	existing := []wrapper.ModuleBlockInfo{
		{Name: "mimir_ha", Source: "git::https://github.com/canonical/mimir-operators.git//terraform/ha?ref=main"},
	}
	same, otherRef := findExistingInstances(existing, mimirSource)
	if len(same) != 0 || len(otherRef) != 0 {
		t.Errorf("a different sub-directory is a different module; got same=%+v otherRef=%+v", same, otherRef)
	}
}

func TestFindExistingInstances_differentRepoIsNotADuplicate(t *testing.T) {
	existing := []wrapper.ModuleBlockInfo{
		{Name: "loki", Source: "git::https://github.com/canonical/loki-operators.git//terraform?ref=main"},
	}
	same, otherRef := findExistingInstances(existing, mimirSource)
	if len(same) != 0 || len(otherRef) != 0 {
		t.Errorf("expected no match across repositories; got same=%+v otherRef=%+v", same, otherRef)
	}
}

func TestFindExistingInstances_ignoresSourcelessBlocks(t *testing.T) {
	existing := []wrapper.ModuleBlockInfo{{Name: "broken", Source: ""}}
	same, otherRef := findExistingInstances(existing, mimirSource)
	if len(same) != 0 || len(otherRef) != 0 {
		t.Errorf("a block with no source cannot match; got same=%+v otherRef=%+v", same, otherRef)
	}
}

func TestFindExistingInstances_repoRootModule(t *testing.T) {
	src := "git::https://example.com/m.git?ref=v1"
	existing := []wrapper.ModuleBlockInfo{{Name: "m", Source: src}}
	same, _ := findExistingInstances(existing, src)
	if len(same) != 1 {
		t.Fatalf("expected a root-module duplicate to be detected; got %+v", same)
	}
}

func TestNormaliseRemote(t *testing.T) {
	cases := map[string]string{
		"https://github.com/o/r.git":  "https://github.com/o/r",
		"https://GitHub.com/o/r":      "https://github.com/o/r",
		"https://github.com/o/r/":     "https://github.com/o/r",
		"https://github.com/o/r.git/": "https://github.com/o/r",
	}
	for in, want := range cases {
		if got := normaliseRemote(in); got != want {
			t.Errorf("normaliseRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBlockNameTaken(t *testing.T) {
	existing := []wrapper.ModuleBlockInfo{{Name: "mimir"}}
	if !blockNameTaken("mimir", existing) {
		t.Error("expected mimir to be taken")
	}
	if blockNameTaken("loki", existing) {
		t.Error("expected loki to be free")
	}
}

func TestDuplicateModuleError_isActionable(t *testing.T) {
	dups := []wrapper.ModuleBlockInfo{{Name: "mimir", Source: mimirSource}}
	err := duplicateModuleError(dups, mimirSource, "https://github.com/canonical/mimir-operators.git")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{`"mimir"`, "--as", "--ref", mimirSource} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q; got:\n%s", want, msg)
		}
	}
}

func TestDuplicateModuleError_namesEveryDuplicate(t *testing.T) {
	dups := []wrapper.ModuleBlockInfo{
		{Name: "mimir", Source: mimirSource},
		{Name: "mimir_extra", Source: mimirSource},
	}
	msg := duplicateModuleError(dups, mimirSource, "url").Error()
	if !strings.Contains(msg, `"mimir"`) || !strings.Contains(msg, `"mimir_extra"`) {
		t.Errorf("expected both block names; got:\n%s", msg)
	}
	if !strings.Contains(msg, "modules ") {
		t.Errorf("expected plural phrasing; got:\n%s", msg)
	}
}
