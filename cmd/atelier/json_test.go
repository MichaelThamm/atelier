package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/bootstrap"
	"github.com/MichaelThamm/atelier/internal/importer"
	"github.com/MichaelThamm/atelier/internal/tfexec"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// These pin the wire format. A --json payload is a contract with consumers that
// this repository cannot see, so the exact bytes are the assertion; a field
// added for a good reason should change the expectation deliberately.

func render(t *testing.T, command string, data any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := renderJSON(&buf, command, data); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	return buf.String()
}

func TestRenderJSON_envelope(t *testing.T) {
	got := render(t, "ls", jsonLs{IsWrapper: true, Modules: []jsonModule{}})
	want := "{\n" +
		"  \"schema\": 1,\n" +
		"  \"command\": \"ls\",\n" +
		"  \"data\": {\n" +
		"    \"isWrapper\": true,\n" +
		"    \"modules\": []\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Errorf("renderJSON:\n%s\nwant:\n%s", got, want)
	}
}

// A consumer iterating a list should not have to handle null; every array in a
// payload is [] when empty. Only the optional objects may be null.
func TestRenderJSON_emptyArraysAreNeverNull(t *testing.T) {
	got := render(t, "import", importPayload(&importer.Result{}, false))
	for _, field := range []string{
		"imported", "unresolved", "unmatchedModule", "unmatchedLive", "queriedTypes", "skippedTypes",
	} {
		if !strings.Contains(got, fmt.Sprintf("%q: []", field)) {
			t.Errorf("%s is not an empty array:\n%s", field, got)
		}
	}
	if !strings.Contains(got, `"matched": {}`) {
		t.Errorf("matched is not an empty object:\n%s", got)
	}
}

// The payload keeps the //subdir, which the text table drops: losing it would
// make a caller re-parse the address to pass it back to `atelier add`.
func TestLsPayload_decomposesTheAddress(t *testing.T) {
	blocks := []wrapper.ModuleBlockInfo{
		{Name: "cos_lite", Source: "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=main"},
		{Name: "prom", Source: "./modules/prom"},
	}
	got := render(t, "ls", lsPayload(true, blocks))
	want := "{\n" +
		"  \"schema\": 1,\n" +
		"  \"command\": \"ls\",\n" +
		"  \"data\": {\n" +
		"    \"isWrapper\": true,\n" +
		"    \"modules\": [\n" +
		"      {\n" +
		"        \"name\": \"cos_lite\",\n" +
		"        \"source\": \"https://github.com/canonical/observability-stack.git\",\n" +
		"        \"modulePath\": \"terraform/cos-lite\",\n" +
		"        \"ref\": \"main\"\n" +
		"      },\n" +
		"      {\n" +
		"        \"name\": \"prom\",\n" +
		"        \"source\": \"./modules/prom\",\n" +
		"        \"modulePath\": null,\n" +
		"        \"ref\": null\n" +
		"      }\n" +
		"    ]\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Errorf("lsPayload:\n%s\nwant:\n%s", got, want)
	}
}

// A directory that holds no main.tf is not an empty wrapper, and the text
// report says so; the payload has to as well.
func TestLsPayload_notAWrapper(t *testing.T) {
	got := render(t, "ls", lsPayload(false, nil))
	if !strings.Contains(got, `"isWrapper": false`) {
		t.Errorf("isWrapper false missing:\n%s", got)
	}
}

func TestAddPayload(t *testing.T) {
	ref := "0123456789abcdef"
	path := "terraform/cos-lite"
	out := addOutcome{
		Dir:   "/srv/stacks/cos-lite",
		Block: jsonModule{Name: "cos_lite", Source: "https://example.com/cos", ModulePath: &path, Ref: &ref},
		All:   []wrapper.ModuleBlockInfo{{Name: "cos_lite"}, {Name: "prom"}},
	}
	got := render(t, "add", addPayload(out))
	want := "{\n" +
		"  \"schema\": 1,\n" +
		"  \"command\": \"add\",\n" +
		"  \"data\": {\n" +
		"    \"wrapper\": \"/srv/stacks/cos-lite\",\n" +
		"    \"added\": {\n" +
		"      \"name\": \"cos_lite\",\n" +
		"      \"source\": \"https://example.com/cos\",\n" +
		"      \"modulePath\": \"terraform/cos-lite\",\n" +
		"      \"ref\": \"0123456789abcdef\"\n" +
		"    },\n" +
		"    \"blocks\": [\n" +
		"      \"cos_lite\",\n" +
		"      \"prom\"\n" +
		"    ]\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Errorf("addPayload:\n%s\nwant:\n%s", got, want)
	}
}

func TestVarFilesPayload(t *testing.T) {
	got := render(t, "add", varFilesPayload([]bootstrap.VarFile{
		{Name: "no-ingress", Path: "/tmp/scratch/presets/no-ingress.tfvars", Source: "repo",
			Display: "terraform/cos-lite/presets/no-ingress.tfvars", Description: "Disable ingress."},
	}))
	want := "{\n" +
		"  \"schema\": 1,\n" +
		"  \"command\": \"add\",\n" +
		"  \"data\": {\n" +
		"    \"bundles\": [\n" +
		"      {\n" +
		"        \"name\": \"no-ingress\",\n" +
		"        \"path\": \"/tmp/scratch/presets/no-ingress.tfvars\",\n" +
		"        \"source\": \"repo\",\n" +
		"        \"display\": \"terraform/cos-lite/presets/no-ingress.tfvars\",\n" +
		"        \"description\": \"Disable ingress.\"\n" +
		"      }\n" +
		"    ]\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Errorf("varFilesPayload:\n%s\nwant:\n%s", got, want)
	}
}

// A wrapper that declares no modules is [], not null: the text table prints
// `-` for it, and a consumer should not have to translate that back.
func TestWrappersPayload(t *testing.T) {
	got := render(t, "wrappers", wrappersPayload([]childWrapper{{Name: "empty"}}, "/srv/stacks"))
	want := "{\n" +
		"  \"schema\": 1,\n" +
		"  \"command\": \"wrappers\",\n" +
		"  \"data\": {\n" +
		"    \"wrappers\": [\n" +
		"      {\n" +
		"        \"name\": \"empty\",\n" +
		"        \"path\": \"/srv/stacks/empty\",\n" +
		"        \"modules\": []\n" +
		"      }\n" +
		"    ]\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Errorf("wrappersPayload:\n%s\nwant:\n%s", got, want)
	}
}

// The three empty-looking import runs are the case this payload exists to make
// unambiguous: something matched, everything is already there, or nothing
// matched. Only the counts cannot tell them apart.
func TestImportPayload_separatesTheThreeEmptyRuns(t *testing.T) {
	tests := []struct {
		name          string
		result        importer.Result
		alreadyInStat bool
		matchedNone   bool
	}{
		{name: "something matched", result: importer.Result{IDs: map[string]string{"module.cos.a": "id:a"}}},
		{name: "already in state", result: importer.Result{MatchedCount: 7}, alreadyInStat: true},
		{name: "matched nothing", result: importer.Result{}, matchedNone: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := importPayload(&tc.result, false)
			if got.AlreadyInState != tc.alreadyInStat {
				t.Errorf("AlreadyInState = %v, want %v", got.AlreadyInState, tc.alreadyInStat)
			}
			if got.MatchedNothing != tc.matchedNone {
				t.Errorf("MatchedNothing = %v, want %v", got.MatchedNothing, tc.matchedNone)
			}
		})
	}
}

func TestImportPayload_reportsWhatAnApplyWouldCreate(t *testing.T) {
	res := &importer.Result{
		Selected:         []importer.ListResource{{Type: "juju_application"}},
		Skipped:          []string{"juju_secret"},
		IDs:              map[string]string{"module.cos.a": "id:a", "module.cos.b": "id:b"},
		Imported:         []importer.ImportResult{{Address: "module.cos.a"}},
		UnmatchedPlanned: []importer.PlannedResource{{Address: "module.cos.c", Type: "juju_application"}},
		UnresolvedIDs:    []importer.MatchedImport{{Address: "module.cos.d", ResourceType: "juju_offer"}},
		UnmatchedLive: []tfexec.LiveResource{
			{ResourceType: "juju_secret", DisplayName: "cert"},
			{ResourceType: "juju_secret", DisplayName: "admin-cert"},
		},
		TerraformVersion: "1.14.0",
	}
	got := render(t, "import", importPayload(res, false))
	want := "{\n" +
		"  \"schema\": 1,\n" +
		"  \"command\": \"import\",\n" +
		"  \"data\": {\n" +
		"    \"matched\": {\n" +
		"      \"module.cos.a\": \"id:a\",\n" +
		"      \"module.cos.b\": \"id:b\"\n" +
		"    },\n" +
		"    \"imported\": [\n" +
		"      \"module.cos.a\"\n" +
		"    ],\n" +
		"    \"alreadyInState\": false,\n" +
		"    \"matchedNothing\": false,\n" +
		"    \"unresolved\": [\n" +
		"      {\n" +
		"        \"address\": \"module.cos.d\",\n" +
		"        \"type\": \"juju_offer\"\n" +
		"      }\n" +
		"    ],\n" +
		"    \"unmatchedModule\": [\n" +
		"      {\n" +
		"        \"address\": \"module.cos.c\",\n" +
		"        \"type\": \"juju_application\"\n" +
		"      }\n" +
		"    ],\n" +
		"    \"unmatchedLive\": [\n" +
		"      {\n" +
		"        \"type\": \"juju_secret\",\n" +
		"        \"count\": 2,\n" +
		"        \"names\": [\n" +
		"          \"admin-cert\",\n" +
		"          \"cert\"\n" +
		"        ]\n" +
		"      }\n" +
		"    ],\n" +
		"    \"queriedTypes\": [\n" +
		"      \"juju_application\"\n" +
		"    ],\n" +
		"    \"skippedTypes\": [\n" +
		"      \"juju_secret\"\n" +
		"    ],\n" +
		"    \"dryRun\": false,\n" +
		"    \"preview\": null,\n" +
		"    \"importsFile\": \"\",\n" +
		"    \"queryFile\": \"\",\n" +
		"    \"terraformVersion\": \"1.14.0\"\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Errorf("importPayload:\n%s\nwant:\n%s", got, want)
	}
}

// The text report shows three names per group and elides long ones; a machine
// consumer gets all of them, unabbreviated.
func TestImportPayload_keepsEveryLiveName(t *testing.T) {
	long := strings.Repeat("x", 80)
	res := &importer.Result{UnmatchedLive: []tfexec.LiveResource{{ResourceType: "juju_secret", DisplayName: long}}}
	got := importPayload(res, false)
	if len(got.UnmatchedLive) != 1 {
		t.Fatalf("UnmatchedLive = %+v, want one group", got.UnmatchedLive)
	}
	if got.UnmatchedLive[0].Names[0] != long {
		t.Errorf("name was elided: %.20q...", got.UnmatchedLive[0].Names[0])
	}
}

func TestImportPayload_dryRunPreview(t *testing.T) {
	res := &importer.Result{
		ImportsFilePath: "/tmp/wrapper/imports.tf",
		Preview: &importer.PlanSummary{
			Import: 12, Add: 3, UnimportableAdds: 3,
			AddAddresses: []string{"module.cos.terraform_data.x"},
		},
	}
	got := importPayload(res, true)
	if !got.DryRun {
		t.Error("DryRun = false, want true")
	}
	if got.Preview == nil {
		t.Fatal("Preview = nil")
	}
	if got.Preview.ToImport != 12 || got.Preview.Add != 3 || got.Preview.UnimportableAdds != 3 {
		t.Errorf("Preview = %+v", got.Preview)
	}
	if got.ImportsFile != "/tmp/wrapper/imports.tf" {
		t.Errorf("ImportsFile = %q", got.ImportsFile)
	}
	// A dry run imports nothing, and says so.
	if len(got.Imported) != 0 {
		t.Errorf("Imported = %v, want empty", got.Imported)
	}
}

func TestListResourcesPayload(t *testing.T) {
	got := render(t, "import", listResourcesPayload(&importer.Result{
		TerraformVersion: "1.14.0",
		Available: []importer.ListResource{{
			Type:          "juju_application",
			ProviderKey:   "registry.terraform.io/juju/juju",
			ProviderLocal: "juju",
			ConfigAttrs:   []importer.ConfigAttr{{Name: "model_uuid", Required: true}},
		}},
	}))
	want := "{\n" +
		"  \"schema\": 1,\n" +
		"  \"command\": \"import\",\n" +
		"  \"data\": {\n" +
		"    \"terraformVersion\": \"1.14.0\",\n" +
		"    \"available\": [\n" +
		"      {\n" +
		"        \"type\": \"juju_application\",\n" +
		"        \"providerKey\": \"registry.terraform.io/juju/juju\",\n" +
		"        \"providerLocal\": \"juju\",\n" +
		"        \"configAttrs\": [\n" +
		"          {\n" +
		"            \"name\": \"model_uuid\",\n" +
		"            \"required\": true\n" +
		"          }\n" +
		"        ]\n" +
		"      }\n" +
		"    ]\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Errorf("listResourcesPayload:\n%s\nwant:\n%s", got, want)
	}
}

// `--json` on a command that reports Terraform's own output would be a silent
// no-op, which a CI job reads as success.
func TestJSONUnsupported(t *testing.T) {
	if err := jsonUnsupported("atelier apply", moduleOpts{}); err != nil {
		t.Errorf("without --json: %v", err)
	}
	err := jsonUnsupported("atelier apply", moduleOpts{JSON: true})
	if err == nil {
		t.Fatal("--json on atelier apply was accepted")
	}
	if !strings.Contains(err.Error(), "atelier apply") {
		t.Errorf("error does not name the command: %v", err)
	}
}

func TestParseModuleArgs_json(t *testing.T) {
	opts, err := parseModuleArgs([]string{"URL", "--json"})
	if err != nil {
		t.Fatalf("parseModuleArgs: %v", err)
	}
	if !opts.JSON {
		t.Error("JSON = false, want true")
	}
}

func TestCandidatesChannel(t *testing.T) {
	// Interactively the list is the answer, so it prints and the run succeeds.
	w, err := candidatesChannel(moduleOpts{})
	if err != nil {
		t.Errorf("without --json: %v", err)
	}
	if w != os.Stdout {
		t.Errorf("without --json the list went to %v, want stdout", w)
	}

	// Under --json stdout is the payload channel and exiting 0 would be
	// indistinguishable from success.
	w, err = candidatesChannel(moduleOpts{JSON: true})
	if err == nil {
		t.Error("an ambiguous source reported success under --json")
	}
	if w != os.Stderr {
		t.Errorf("under --json the list went to %v, want stderr", w)
	}
}
