package importer

import (
	"errors"
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
)

func TestPostImportSummary(t *testing.T) {
	drift := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		change("juju_application.a", "juju_application", tfjson.Actions{tfjson.ActionCreate}, false),
		change("terraform_data.b", "terraform_data", tfjson.Actions{tfjson.ActionCreate}, false),
	}}

	tests := []struct {
		name     string
		asked    bool
		plan     func() (*tfjson.Plan, error)
		wantNil  bool
		wantAdd  int
		wantNote bool
	}{
		{
			// The gate is the point: without a reader there is no reason to spend
			// a plan, and Terraform's provider RPCs are not free.
			name:  "not asked, no plan run",
			asked: false,
			plan: func() (*tfjson.Plan, error) {
				t.Error("plan must not run when the answer was not requested")
				return nil, nil
			},
			wantNil: true,
		},
		{
			name:  "asked, drift reported",
			asked: true,
			plan:  func() (*tfjson.Plan, error) { return drift, nil },
			// Add counts every create; AddAddresses lists only the importable
			// ones, so a caller reading the addresses sees one entry and knows the
			// other was bookkeeping.
			wantAdd: 2,
		},
		{
			name:  "plan failure does not become an import failure",
			asked: true,
			plan: func() (*tfjson.Plan, error) {
				return nil, errors.New("terraform init: no such provider")
			},
			// The imports are already in state here. Reporting nothing, loudly, is
			// correct; returning an error would misreport a completed import.
			wantNil:  true,
			wantNote: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr strings.Builder
			got := postImportSummary(tt.asked, tt.plan, &stderr)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("got %+v, want nil", got)
				}
			} else {
				if got == nil {
					t.Fatal("got nil, want a summary")
				}
				if got.Add != tt.wantAdd {
					t.Errorf("Add = %d, want %d", got.Add, tt.wantAdd)
				}
				if len(got.AddAddresses) != 1 || got.AddAddresses[0] != "juju_application.a" {
					t.Errorf("AddAddresses = %v, want [juju_application.a]", got.AddAddresses)
				}
				if got.UnimportableAdds != 1 {
					t.Errorf("UnimportableAdds = %d, want 1", got.UnimportableAdds)
				}
			}
			if noted := strings.Contains(stderr.String(), "Could not plan"); noted != tt.wantNote {
				t.Errorf("stderr noted the failure = %v (%q), want %v", noted, stderr.String(), tt.wantNote)
			}
		})
	}
}
