package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/gallery"
)

func TestIsModuleRootDir(t *testing.T) {
	tests := []struct {
		dir  string
		all  bool
		want bool
	}{
		{"terraform/product", false, true},
		{"terraform/product/replica_set", false, true},
		{"terraform/products/kubeflow", false, true},
		{"terraform/solution", false, true},
		{"terraform", false, false},
		{"terraform/charmarr", false, false},
		{"terraform/cos-lite", false, false},
		{"terraform/product/modules/redis", false, false},
		{"terraform/product/modules/redis", true, false},
		{"terraform/tests/foo", true, false},
		{"terraform/examples/foo", true, false},
		{"terraform", true, true},
		{"terraform/charmarr", true, true},
		{"docs", false, false},
		{".", false, false},
	}
	for _, tt := range tests {
		if got := isModuleRootDir(tt.dir, tt.all); got != tt.want {
			t.Errorf("isModuleRootDir(%q, %v) = %v, want %v", tt.dir, tt.all, got, tt.want)
		}
	}
}

func TestCandidateModules_filtersDedupesAndSorts(t *testing.T) {
	results := []codeResult{
		{Repo: "canonical/b", Path: "terraform/product/variables.tf"},
		{Repo: "canonical/a", Path: "terraform/solution/variables.tf"},
		{Repo: "canonical/a", Path: "terraform/solution/variables.tf"},
		{Repo: "canonical/a", Path: "terraform/product/modules/x/variables.tf"},
		{Repo: "canonical/c", Path: "terraform/variables.tf"},
	}

	got := candidateModules(results, false)
	want := []candidate{
		{Repo: "canonical/a", Dir: "terraform/solution"},
		{Repo: "canonical/b", Dir: "terraform/product"},
	}
	assertCandidates(t, got, want)

	gotAll := candidateModules(results, true)
	wantAll := []candidate{
		{Repo: "canonical/a", Dir: "terraform/solution"},
		{Repo: "canonical/b", Dir: "terraform/product"},
		{Repo: "canonical/c", Dir: "terraform"},
	}
	assertCandidates(t, gotAll, wantAll)
}

func TestUncoveredCandidates(t *testing.T) {
	entries := []gallery.Entry{
		{Module: "https://github.com/canonical/observability-stack", Subdir: "terraform/cos"},
		{Module: "https://github.com/canonical/observability-stack", Subdir: "terraform/cos-lite"},
		{Module: "https://github.com/canonical/airbyte-k8s-operator.git", Subdir: "terraform/product"},
	}
	candidates := []candidate{
		{Repo: "canonical/observability-stack", Dir: "terraform/cos"},
		{Repo: "canonical/observability-stack", Dir: "terraform/cos-lite"},
		{Repo: "canonical/observability-stack", Dir: "terraform/seaweedfs"},
		{Repo: "canonical/airbyte-k8s-operator", Dir: "terraform/product/modules/x"},
		{Repo: "canonical/new-operator", Dir: "terraform/product"},
	}
	got := uncoveredCandidates(candidates, entries)
	want := []candidate{
		{Repo: "canonical/observability-stack", Dir: "terraform/seaweedfs"},
		{Repo: "canonical/new-operator", Dir: "terraform/product"},
	}
	assertCandidates(t, got, want)
}

func TestRepoSlug(t *testing.T) {
	tests := []struct {
		module string
		want   string
	}{
		{"https://github.com/canonical/airbyte-k8s-operator", "canonical/airbyte-k8s-operator"},
		{"https://github.com/canonical/airbyte-k8s-operator.git", "canonical/airbyte-k8s-operator"},
		{"https://github.com/Azure/terraform-azurerm-aks/", "Azure/terraform-azurerm-aks"},
	}
	for _, tt := range tests {
		if got := repoSlug(tt.module); got != tt.want {
			t.Errorf("repoSlug(%q) = %q, want %q", tt.module, got, tt.want)
		}
	}
}

func TestRun_reportsOnlyUncoveredProductModules(t *testing.T) {
	search := func(context.Context, string) ([]codeResult, error) {
		return []codeResult{
			{Repo: "canonical/observability-stack", Path: "terraform/cos/variables.tf"},
			{Repo: "canonical/observability-stack", Path: "terraform/products/new-product/variables.tf"},
			{Repo: "canonical/brand-new-operator", Path: "terraform/product/variables.tf"},
			{Repo: "canonical/alertmanager-k8s-operator", Path: "terraform/variables.tf"},
			{Repo: "canonical/opencti-operator", Path: "terraform/product/modules/redis/variables.tf"},
		}, nil
	}
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, search); code != 0 {
		t.Fatalf("run exited %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"canonical/observability-stack\tterraform/products/new-product",
		"canonical/brand-new-operator\tterraform/product",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"\tterraform/cos\t", "alertmanager", "redis"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("output should not contain %q:\n%s", unwanted, out)
		}
	}
}

func TestRun_quietWhenNothingIsNew(t *testing.T) {
	search := func(context.Context, string) ([]codeResult, error) {
		return []codeResult{{Repo: "canonical/airbyte-k8s-operator", Path: "terraform/product/variables.tf"}}, nil
	}
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, search); code != 0 {
		t.Fatalf("run exited %d: %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("expected no candidates, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "no new candidates") {
		t.Errorf("expected a quiet notice, got: %s", stderr.String())
	}
}

func assertCandidates(t *testing.T, got, want []candidate) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d candidates %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}
