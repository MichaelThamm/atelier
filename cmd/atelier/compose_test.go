package main

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

const (
	mimirAtMain   = "git::https://github.com/canonical/mimir-operators.git//terraform?ref=main"
	mimirAtRev300 = "git::https://github.com/canonical/mimir-operators.git//terraform?ref=rev300"
	lokiSource    = "git::https://github.com/canonical/loki-operators.git//terraform?ref=main"
)

func req(source, as string) composeRequest {
	return composeRequest{source: source, requested: "mimir-operators", derivedName: "mimir", as: as}
}

// planCompose decides whether a compose updates a block or appends one, so the
// cases below are the whole rule: which blocks count as "the module", and what
// --as may do to that answer (ADR-0050).
func TestPlanCompose(t *testing.T) {
	cases := []struct {
		name     string
		mode     composeMode
		existing []wrapper.ModuleBlockInfo
		req      composeRequest
		wantName string // the block to update, or the name to append under
		appendIt bool
		wantAs   string   // an --as that named no block, so it was reported instead
		wantErr  []string // substrings the error must carry
	}{
		{
			name:     "apply updates the one block declaring the module",
			mode:     composeApply,
			existing: []wrapper.ModuleBlockInfo{{Name: "mimir", Source: mimirAtMain}},
			req:      req(mimirAtMain, ""),
			wantName: "mimir",
		},
		{
			// The case ADR-0050 exists for: this used to append mimir_2.
			name:     "apply re-points the block at a different ref",
			mode:     composeApply,
			existing: []wrapper.ModuleBlockInfo{{Name: "mimir", Source: mimirAtMain}},
			req:      req(mimirAtRev300, ""),
			wantName: "mimir",
		},
		{
			// The identity ignores how the URL is spelled, so a gallery entry
			// expanding to a differently-written source still matches.
			name:     "apply matches across spellings of the same source",
			mode:     composeApply,
			existing: []wrapper.ModuleBlockInfo{{Name: "mimir", Source: "https://GitHub.com/canonical/mimir-operators//terraform?ref=main"}},
			req:      req(mimirAtMain, ""),
			wantName: "mimir",
		},
		{
			name:     "apply appends when the module is absent",
			mode:     composeApply,
			existing: []wrapper.ModuleBlockInfo{{Name: "loki", Source: lokiSource}},
			req:      req(mimirAtMain, ""),
			wantName: "mimir",
			appendIt: true,
		},
		{
			// A sub-directory is a different module: two blocks of one repo, one
			// HA and one not, is a legitimate composition.
			name:     "apply appends when only the sub-directory differs",
			mode:     composeApply,
			existing: []wrapper.ModuleBlockInfo{{Name: "mimir_ha", Source: "git::https://github.com/canonical/mimir-operators.git//terraform/ha?ref=main"}},
			req:      req(mimirAtMain, ""),
			wantName: "mimir",
			appendIt: true,
		},
		{
			name:     "as selects the block to update",
			mode:     composeApply,
			existing: []wrapper.ModuleBlockInfo{{Name: "mimir", Source: mimirAtMain}, {Name: "mimir_2", Source: mimirAtRev300}},
			req:      req(mimirAtRev300, "mimir_2"),
			wantName: "mimir_2",
		},
		{
			name:     "as may create a block when the module is absent",
			mode:     composeApply,
			existing: []wrapper.ModuleBlockInfo{{Name: "loki", Source: lokiSource}},
			req:      req(mimirAtMain, "prod_mimir"),
			wantName: "prod_mimir",
			appendIt: true,
		},
		{
			name:     "as pointing at another module's block is refused",
			mode:     composeApply,
			existing: []wrapper.ModuleBlockInfo{{Name: "mimir", Source: mimirAtMain}, {Name: "loki", Source: lokiSource}},
			req:      req(mimirAtRev300, "loki"),
			wantErr:  []string{`--as loki`, `"loki"`, "different module"},
		},
		{
			// A wrapper composed by an older version may hold two blocks of one
			// module; guessing which to re-point would be a coin flip.
			name:     "two blocks for one module are refused by name",
			mode:     composeApply,
			existing: []wrapper.ModuleBlockInfo{{Name: "mimir", Source: mimirAtMain}, {Name: "mimir_2", Source: mimirAtRev300}},
			req:      req(mimirAtRev300, ""),
			wantErr:  []string{"mimir", "mimir_2", "--as", "atelier rm"},
		},
		{
			// add authors: a module already in the wrapper is a duplicate, not
			// something to overwrite behind the user's back.
			name:     "add refuses the module at the same ref",
			mode:     composeAdd,
			existing: []wrapper.ModuleBlockInfo{{Name: "mimir", Source: mimirAtMain}},
			req:      req(mimirAtMain, ""),
			wantErr:  []string{`"mimir"`, "atelier apply"},
		},
		{
			name:     "add refuses the module at a different ref too",
			mode:     composeAdd,
			existing: []wrapper.ModuleBlockInfo{{Name: "mimir", Source: mimirAtMain}},
			req:      req(mimirAtRev300, ""),
			wantErr:  []string{`"mimir"`, "--ref"},
		},
		{
			// A gallery entry supplies --as from its own entry, so the user never
			// typed it. If the wrapper holds the module under another name, the
			// unmatched name must not dead-end the command — and it is reported
			// rather than obeyed.
			name:     "as naming no block falls back to the one declaring the module",
			mode:     composeApply,
			existing: []wrapper.ModuleBlockInfo{{Name: "prod_cos", Source: mimirAtMain}},
			req:      req(mimirAtMain, "cos_lite"),
			wantName: "prod_cos",
			wantAs:   "cos_lite",
		},
		{
			// Nothing to fall back to: the name is honoured, and no block is
			// duplicated because the module is absent.
			name:     "as naming no block creates one when the module is absent",
			mode:     composeApply,
			existing: []wrapper.ModuleBlockInfo{{Name: "loki", Source: lokiSource}},
			req:      req(mimirAtMain, "cos_lite"),
			wantName: "cos_lite",
			appendIt: true,
		},
		{
			name:     "as naming no block with several matching blocks still refuses",
			mode:     composeApply,
			existing: []wrapper.ModuleBlockInfo{{Name: "mimir", Source: mimirAtMain}, {Name: "mimir_2", Source: mimirAtRev300}},
			req:      req(mimirAtRev300, "cos_lite"),
			wantErr:  []string{"mimir", "mimir_2", "--as"},
		},
		{
			// Withdrawing --as as a multiplier is the point: `add --as <free>`
			// used to be the documented escape hatch for a second instance.
			name:     "add refuses a second copy even with a free as",
			mode:     composeAdd,
			existing: []wrapper.ModuleBlockInfo{{Name: "mimir", Source: mimirAtMain}},
			req:      req(mimirAtRev300, "other"),
			wantErr:  []string{`"mimir"`, "main.tf"},
		},
		{
			name:     "add appends when the module is absent",
			mode:     composeAdd,
			existing: []wrapper.ModuleBlockInfo{{Name: "loki", Source: lokiSource}},
			req:      req(mimirAtMain, "mimir"),
			wantName: "mimir",
			appendIt: true,
		},
		{
			name:     "a block with no source is ignored",
			mode:     composeApply,
			existing: []wrapper.ModuleBlockInfo{{Name: "broken", Source: ""}},
			req:      req(mimirAtMain, ""),
			wantName: "mimir",
			appendIt: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, err := planCompose(c.mode, c.existing, c.req)
			if len(c.wantErr) > 0 {
				if err == nil {
					t.Fatalf("expected a refusal; got %+v", plan)
				}
				for _, want := range c.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error should mention %q; got:\n%s", want, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("planCompose: %v", err)
			}
			if c.appendIt {
				if plan.update.Name != "" {
					t.Fatalf("expected an append; got an update of %q", plan.update.Name)
				}
				if plan.blockName != c.wantName {
					t.Errorf("append name = %q, want %q", plan.blockName, c.wantName)
				}
				return
			}
			if plan.blockName != "" {
				t.Errorf("expected an update, but it would append %q", plan.blockName)
			}
			if plan.update.Name != c.wantName {
				t.Errorf("update target = %q, want %q", plan.update.Name, c.wantName)
			}
			if plan.ignoredAs != c.wantAs {
				t.Errorf("ignoredAs = %q, want %q", plan.ignoredAs, c.wantAs)
			}
		})
	}
}

// An update must target the block whose source it read, so the write lands in
// that block rather than a sibling's.
func TestPlanCompose_updateCarriesTheExistingSource(t *testing.T) {
	existing := []wrapper.ModuleBlockInfo{{Name: "mimir", Source: mimirAtMain}}
	plan, err := planCompose(composeApply, existing, req(mimirAtRev300, ""))
	if err != nil {
		t.Fatal(err)
	}
	if plan.update.Source != mimirAtMain {
		t.Errorf("update source = %q, want the block's own source %q", plan.update.Source, mimirAtMain)
	}
}

// prunedArgs reports what the sparse rule is about to remove, so an argument
// disappearing from the user's file is never silent (ADR-0007, ADR-0050).
func TestPrunedArgs(t *testing.T) {
	state := &wrapper.State{
		Vars: []tfvars.Variable{
			{Name: "at_default", HasDefault: true, Default: cty.StringVal("d")},
			{Name: "changed", HasDefault: true, Default: cty.StringVal("d")},
			{Name: "required"},
		},
		Values: map[string]cty.Value{
			"at_default": cty.StringVal("d"),
			"changed":    cty.StringVal("edited"),
			"required":   cty.StringVal("v"),
		},
	}
	prior := map[string]bool{"at_default": true, "changed": true, "required": true, "not_a_var": true}

	got := prunedArgs(state, prior)
	if len(got) != 1 || got[0] != "at_default" {
		t.Errorf("prunedArgs = %v, want [at_default]", got)
	}
}
