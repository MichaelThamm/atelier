package wrapper

import (
	"strings"
	"testing"
)

// Public-facing text is what a user reads. It must not name Atelier's internal
// development artefacts — an ADR, the SPEC, or a package path — because a reader
// of a generated wrapper has never seen this repository and cannot act on any of
// them. See the "Public-facing output" convention in AGENTS.md.
func TestPublicFacingWrapperReadme_hasNoInternalReferences(t *testing.T) {
	for _, ref := range []string{
		"ADR-", "docs/adr", "docs/SPEC", "SPEC.md", "docs/ROADMAP", "internal/",
	} {
		if strings.Contains(readmeTemplate, ref) {
			t.Errorf("the generated wrapper README mentions %q, which is internal to the repository:\n%s",
				ref, readmeTemplate)
		}
	}
}

// The README tells the user what the directory is and how to run it. It must
// also stay true on its own terms: the usage block is plain Terraform, so a
// reader is never told to reach for Atelier to deploy it.
func TestPublicFacingWrapperReadme_standsAlone(t *testing.T) {
	if !strings.Contains(readmeTemplate, "terraform init") ||
		!strings.Contains(readmeTemplate, "terraform apply") {
		t.Errorf("the wrapper README must document plain Terraform usage:\n%s", readmeTemplate)
	}
}
