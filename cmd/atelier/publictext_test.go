package main

import (
	"strings"
	"testing"
)

// The CLI usage text is read by people who have not seen this repository, so it
// must not point at internal artefacts or restate design rationale. It tells a
// user what each command does; why it exists belongs in the ADR behind it. See
// the "Public-facing output" convention in AGENTS.md.
func TestPublicFacingUsage_hasNoInternalReferences(t *testing.T) {
	// Every help string a user can reach, not just the top-level one.
	help := map[string]string{
		"usage":         usage,
		"wrappersUsage": wrappersUsage,
		"galleryUsage":  galleryUsage,
		"presetsUsage":  presetsUsage,
	}
	for name, text := range help {
		if text == "" {
			continue // not every command carries a usage const
		}
		for _, ref := range []string{
			"ADR-", "docs/adr", "docs/SPEC", "SPEC.md", "docs/ROADMAP", "internal/",
		} {
			if strings.Contains(text, ref) {
				t.Errorf("%s mentions %q, which is internal to the repository", name, ref)
			}
		}
	}
}

// The commands a user is most likely to reach for must be discoverable from
// --help alone, in the flattened spellings the CLI actually accepts.
func TestUsage_documentsTheFlatCommands(t *testing.T) {
	for _, want := range []string{"atelier add ", "atelier rm ", "atelier ls ", "atelier apply ", "atelier wrappers "} {
		if !strings.Contains(usage, want) {
			t.Errorf("--help does not document %q", strings.TrimSpace(want))
		}
	}
	// The removed namespace must not survive in the help text, or the breaking
	// change is documented as still working.
	if strings.Contains(usage, "atelier module ") {
		t.Error("--help still documents the removed `atelier module` namespace")
	}
}
