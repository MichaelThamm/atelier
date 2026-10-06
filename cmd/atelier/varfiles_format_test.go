package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/bootstrap"
)

// TestPrintVarFiles_alignsDescriptions is the regression test for the listing's
// columns: a fixed name width shifted by one space for any name that filled it
// exactly, which `saml-integrator-defaults` did.
func TestPrintVarFiles_alignsDescriptions(t *testing.T) {
	tests := []struct {
		name  string
		files []bootstrap.VarFile
	}{
		{
			name: "a name exactly the old fixed width",
			files: []bootstrap.VarFile{
				{Source: "repo", Name: "saml-integrator-defaults", Display: "first"},
				{Source: "gallery", Name: "short", Display: "second"},
			},
		},
		{
			name: "names of every length around the boundary",
			files: []bootstrap.VarFile{
				{Source: "repo", Name: "a", Display: "one"},
				{Source: "gallery", Name: "exactly-twenty-four-chars", Display: "two"},
				{Source: "local", Name: "much-longer-bundle-name-here", Display: "three"},
			},
		},
		{
			name: "all the same length",
			files: []bootstrap.VarFile{
				{Source: "repo", Name: "aaaa", Display: "first"},
				{Source: "gallery", Name: "bbbb", Display: "second"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			printVarFilesTo(&buf, tc.files)

			lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
			if len(lines) != len(tc.files) {
				t.Fatalf("printed %d lines for %d bundles:\n%s", len(lines), len(tc.files), buf.String())
			}

			// Every description must begin in the same column.
			col := strings.Index(lines[0], tc.files[0].Display)
			if col < 0 {
				t.Fatalf("first description not found in %q", lines[0])
			}
			for i, line := range lines {
				got := strings.Index(line, tc.files[i].Display)
				if got != col {
					t.Errorf("row %d description starts at column %d; want %d:\n%s",
						i, got, col, buf.String())
				}
			}

			// A name exactly filling the column still needs a separating space, so
			// the description is never flush against it.
			for i, line := range lines {
				if !strings.Contains(line, tc.files[i].Name+" ") {
					t.Errorf("row %d = %q; want the name followed by a space", i, line)
				}
			}
		})
	}
}

// TestPrintVarFiles_emptyReportsWhatWasSearched keeps the no-bundles case
// explaining itself rather than printing an empty table.
func TestPrintVarFiles_emptyReportsWhatWasSearched(t *testing.T) {
	var buf bytes.Buffer
	printVarFilesTo(&buf, nil)

	got := buf.String()
	if !strings.Contains(got, "No .tfvars bundles found") {
		t.Errorf("got %q; want it to say no bundles were found", got)
	}
}
