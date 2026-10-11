package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/integrate"
)

func TestWireExpr(t *testing.T) {
	p := integrate.Proposal{Producer: "cos_lite", Output: "cos_offers", Interfaces: []string{"a", "b", "c"}}
	if got := p.WireExpr([]string{"a", "b", "c"}); got != "module.cos_lite.cos_offers" {
		t.Errorf("all-interfaces expr = %q", got)
	}
	if got := p.WireExpr([]string{"b"}); got != "{ b = module.cos_lite.cos_offers.b }" {
		t.Errorf("subset expr = %q", got)
	}
}

func TestParseSelection(t *testing.T) {
	ifaces := []string{"grafana_dashboard", "loki_push_api", "prometheus_remote_write"}
	cases := []struct {
		in   string
		want []string
	}{
		{"", ifaces},
		{"2", []string{"loki_push_api"}},
		{"1 3", []string{"grafana_dashboard", "prometheus_remote_write"}},
		{"loki_push_api,prometheus_remote_write", []string{"loki_push_api", "prometheus_remote_write"}},
	}
	for _, tc := range cases {
		got, err := parseSelection(tc.in, ifaces)
		if err != nil {
			t.Fatalf("parseSelection(%q): %v", tc.in, err)
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("parseSelection(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	if _, err := parseSelection("9", ifaces); err == nil {
		t.Error("out-of-range number should error")
	}
}

func TestRunIntegrate_writesTheChosenSubset(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.tf", `module "cos_lite" {
  source = "./producer"
}
module "charmed_spark" {
  source = "./consumer"
}
`)
	write("producer/outputs.tf", `output "cos_offers" {
  type = object({ grafana_dashboard = string, loki_push_api = string, prometheus_remote_write = string })
  value = { grafana_dashboard = "g", loki_push_api = "l", prometheus_remote_write = "m" }
}
`)
	write("consumer/variables.tf", `variable "cos_offers" {
  type = object({ grafana_dashboard = optional(string), loki_push_api = optional(string), prometheus_remote_write = optional(string) })
}
`)

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })

	out, err := captureStdout(t, func() error {
		return runIntegrate([]string{"--yes", "--interfaces=loki_push_api"})
	})
	if err != nil {
		t.Fatalf("runIntegrate: %v\n%s", err, out)
	}

	got, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "cos_offers = { loki_push_api = module.cos_lite.cos_offers.loki_push_api }") {
		t.Errorf("subset wire not written, got:\n%s", got)
	}
	if strings.Contains(string(got), "grafana_dashboard =") {
		t.Errorf("unselected interface was written, got:\n%s", got)
	}
}
