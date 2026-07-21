package importer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelThamm/atelier/internal/tfexec"
)

func TestListBlockLinesAndFailedTypes(t *testing.T) {
	dir := t.TempDir()
	all := []ListResource{
		{Type: "juju_application", ProviderLocal: "juju", ConfigAttrs: []ConfigAttr{{Name: "model_uuid"}}},
		{Type: "juju_space", ProviderLocal: "juju", ConfigAttrs: []ConfigAttr{{Name: "model_uuid"}}},
		{Type: "juju_ssh_key", ProviderLocal: "juju", ConfigAttrs: []ConfigAttr{{Name: "model_uuid"}}},
	}
	qf := filepath.Join(dir, "q.tfquery.hcl")
	if err := os.WriteFile(qf, RenderQueryFile(all, map[string]string{"model_uuid": "x"}), 0o644); err != nil {
		t.Fatal(err)
	}

	lineType := listBlockLines(qf)
	if len(lineType) == 0 {
		t.Fatal("expected line->type mapping")
	}

	// Find a line belonging to juju_space and juju_ssh_key, then build a
	// synthetic QueryError pointing at those lines.
	var spaceLine, sshLine int
	for ln, typ := range lineType {
		switch typ {
		case "juju_space":
			spaceLine = ln
		case "juju_ssh_key":
			sshLine = ln
		}
	}
	if spaceLine == 0 || sshLine == 0 {
		t.Fatalf("did not locate lines for juju_space (%d) / juju_ssh_key (%d)", spaceLine, sshLine)
	}

	qerr := &tfexec.QueryError{Diagnostics: []tfexec.QueryDiagnostic{
		{Severity: "error", Summary: "Client Error", Filename: "q.tfquery.hcl", Line: spaceLine},
		{Severity: "error", Summary: "Client Error", Filename: "q.tfquery.hcl", Line: sshLine},
	}}
	failed := failedTypes(qerr, qf)
	if len(failed) != 2 {
		t.Fatalf("expected 2 failed types, got %v", failed)
	}
	got := map[string]bool{failed[0]: true, failed[1]: true}
	if !got["juju_space"] || !got["juju_ssh_key"] {
		t.Errorf("expected juju_space and juju_ssh_key, got %v", failed)
	}
}

func TestFailedTypesUnattributable(t *testing.T) {
	dir := t.TempDir()
	qf := filepath.Join(dir, "q.tfquery.hcl")
	_ = os.WriteFile(qf, RenderQueryFile([]ListResource{{Type: "juju_model", ProviderLocal: "juju"}}, nil), 0o644)

	// A diagnostic with no matching line (e.g. a connection error) is not
	// attributable to any type, so failedTypes returns nothing and the caller
	// treats it as fatal.
	qerr := &tfexec.QueryError{Diagnostics: []tfexec.QueryDiagnostic{
		{Severity: "error", Summary: "connection refused", Line: 9999},
	}}
	if failed := failedTypes(qerr, qf); len(failed) != 0 {
		t.Errorf("expected no attributable types, got %v", failed)
	}
}

func TestDropTypes(t *testing.T) {
	active := []ListResource{{Type: "a"}, {Type: "b"}, {Type: "c"}}
	kept, skipped := dropTypes(active, []string{"b"}, nil)
	if len(kept) != 2 || kept[0].Type != "a" || kept[1].Type != "c" {
		t.Errorf("kept = %v", typeNames(kept))
	}
	if len(skipped) != 1 || skipped[0] != "b" {
		t.Errorf("skipped = %v", skipped)
	}
}
