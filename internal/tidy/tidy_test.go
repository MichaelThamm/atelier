package tidy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// variablesTF mirrors the shape of a real module schema: a required object, an
// optional object with optional() field defaults, a scalar with a default, and
// a nullable string. tidy must prune values equal to these defaults and keep
// everything else.
const variablesTF = `
variable "model" {
  type = object({
    name = string
  })
}

variable "grafana" {
  type = object({
    app_name = optional(string, "grafana")
    units    = optional(number, 1)
  })
  default = {}
}

variable "internal_tls" {
  type    = bool
  default = true
}

variable "external_ca_cert_offer_url" {
  type    = string
  default = null
}
`

// writeFixture lays down a local "module" directory (the schema) and a wrapper
// main.tf whose module source points at it. Returns the wrapper dir.
func writeFixture(t *testing.T, mainTF string) (wrapperDir, moduleDir string) {
	t.Helper()
	moduleDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(moduleDir, "variables.tf"), []byte(variablesTF), 0o644); err != nil {
		t.Fatal(err)
	}
	wrapperDir = t.TempDir()
	rendered := strings.ReplaceAll(mainTF, "__SOURCE__", moduleDir)
	if err := os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	return wrapperDir, moduleDir
}

// clutteredMain has two prunable items (grafana.units at default 1, and
// internal_tls at default true) plus values that must survive: the required
// model, the overridden grafana.app_name, and an expression attribute.
const clutteredMain = `module "mod" {
  source = "__SOURCE__"
  model = {
    name = "my-model"
  }
  grafana = {
    app_name = "custom-grafana"
    units    = 1
  }
  internal_tls               = true
  external_ca_cert_offer_url = local.cert_url
}
`

func TestRun_dryRunDoesNotWrite(t *testing.T) {
	dir, _ := writeFixture(t, clutteredMain)
	before, _ := os.ReadFile(filepath.Join(dir, "main.tf"))

	res, err := Run(context.Background(), Options{Dir: dir, Write: false})
	if err != nil {
		t.Fatal(err)
	}
	if res.AlreadyTidy {
		t.Fatal("expected a prune to be available, got AlreadyTidy")
	}
	if !strings.Contains(res.Diff, "- ") {
		t.Errorf("diff should contain removed lines:\n%s", res.Diff)
	}
	if !strings.Contains(res.Diff, "units") || !strings.Contains(res.Diff, "internal_tls") {
		t.Errorf("diff should remove units and internal_tls:\n%s", res.Diff)
	}
	if res.BackupPath != "" {
		t.Errorf("dry run must not create a backup, got %q", res.BackupPath)
	}

	after, _ := os.ReadFile(filepath.Join(dir, "main.tf"))
	if string(before) != string(after) {
		t.Error("dry run modified main.tf")
	}
}

func TestRun_writePrunesAndPreserves(t *testing.T) {
	dir, _ := writeFixture(t, clutteredMain)

	res, err := Run(context.Background(), Options{Dir: dir, Write: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.BackupPath == "" {
		t.Fatal("--write must create a backup")
	}
	if _, err := os.Stat(res.BackupPath); err != nil {
		t.Errorf("backup not written: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(dir, "main.tf"))
	s := string(got)

	// Pruned: values equal to their declared defaults.
	if strings.Contains(s, "units") {
		t.Errorf("units (at default) should be pruned:\n%s", s)
	}
	if strings.Contains(s, "internal_tls") {
		t.Errorf("internal_tls (at default) should be pruned:\n%s", s)
	}
	// Preserved: required, overrides, and expressions.
	for _, want := range []string{`name = "my-model"`, `app_name = "custom-grafana"`, "external_ca_cert_offer_url", "local.cert_url"} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q to survive tidy:\n%s", want, s)
		}
	}
}

func TestRun_idempotent(t *testing.T) {
	dir, _ := writeFixture(t, clutteredMain)
	if _, err := Run(context.Background(), Options{Dir: dir, Write: true}); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), Options{Dir: dir, Write: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.AlreadyTidy {
		t.Errorf("second tidy should be a no-op, got diff:\n%s", res.Diff)
	}
	if res.BackupPath != "" {
		t.Error("no-op tidy must not create a backup")
	}
}

func TestRun_alreadyTidy(t *testing.T) {
	const minimal = `module "mod" {
  source = "__SOURCE__"
  model = {
    name = "my-model"
  }
}
`
	dir, _ := writeFixture(t, minimal)
	res, err := Run(context.Background(), Options{Dir: dir, Write: false})
	if err != nil {
		t.Fatal(err)
	}
	if !res.AlreadyTidy {
		t.Errorf("minimal wrapper should already be tidy, got diff:\n%s", res.Diff)
	}
}

func TestRun_refusesMultipleModuleBlocks(t *testing.T) {
	const twoBlocks = `module "a" {
  source = "__SOURCE__"
}
module "b" {
  source = "__SOURCE__"
}
`
	dir, _ := writeFixture(t, twoBlocks)
	_, err := Run(context.Background(), Options{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "single-module") {
		t.Errorf("expected refusal for multiple module blocks, got %v", err)
	}
}

func TestRun_noMainTF(t *testing.T) {
	_, err := Run(context.Background(), Options{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "not a wrapper") {
		t.Errorf("expected 'not a wrapper' error, got %v", err)
	}
}

func TestRefWarning(t *testing.T) {
	cases := []struct {
		name     string
		source   string
		ref      string
		wantWarn bool
	}{
		{"local source", "/tmp/mod", "", false},
		{"unpinned HEAD", "git::https://x/y.git", "", true},
		{"branch ref", "git::https://x/y.git", "main", true},
		{"tag ref", "git::https://x/y.git", "v1.2.0", true},
		{"sha ref", "git::https://x/y.git", "0123456789abcdef0123456789abcdef01234567", false},
	}
	for _, c := range cases {
		got := refWarning(c.source, c.ref) != ""
		if got != c.wantWarn {
			t.Errorf("%s: refWarning(%q,%q) warn=%v, want %v", c.name, c.source, c.ref, got, c.wantWarn)
		}
	}
}
