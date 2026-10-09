package main

import (
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/gallery"
)

// TestRender_cards pins the page shape: a Material grid with one card per
// entry, carrying the module link, the pinned short ref, the presets, and the
// derived apply command.
func TestRender_cards(t *testing.T) {
	entries := []gallery.Entry{
		{
			Name: "cos-lite", Description: "COS Lite.",
			Module: "https://github.com/canonical/observability-stack",
			Subdir: "terraform/cos-lite", Ref: "d1598ff3bdf9a25af69145fd557a913e2a13a314",
		},
		{
			Name: "cos", Description: "The full stack.",
			Module: "https://github.com/canonical/observability-stack",
			Subdir: "terraform/cos", Ref: "d1598ff3bdf9a25af69145fd557a913e2a13a314",
			Presets:          []string{"cos-grafana-single-unit"},
			AvailablePresets: []string{"cos-single-unit", "cos-no-ingress"},
			Block:            "cos",
			Requires:         []string{"s3_access_key", "s3_secret_key"},
		},
	}

	var b strings.Builder
	if err := render(&b, entries); err != nil {
		t.Fatal(err)
	}
	got := b.String()

	for _, want := range []string{
		`<div class="grid cards" markdown>`,
		"-   __cos-lite__",
		"[:octicons-mark-github-16: canonical/observability-stack](https://github.com/canonical/observability-stack)",
		"`terraform/cos-lite` · pinned `d1598ff3bdf9`",
		"atelier apply cos-lite",
		"presets `cos-grafana-single-unit` · block `cos`",
		"Also available: `cos-single-unit`, `cos-no-ingress`",
		"atelier apply cos --var s3_access_key=<s3_access_key> --var s3_secret_key=<s3_secret_key>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered page missing %q\n\n%s", want, got)
		}
	}
}

// A required input appears in the card's own command once, as a `--var`. The
// collapsed Juju variant is a different command, with the values resolved, and
// the card never restates the list in prose (ADR-0046).
func TestRender_cardStatesRequiresOnlyInTheCommand(t *testing.T) {
	e := gallery.Entry{
		Name: "tempo-operators", Description: "Tempo.",
		Module: "https://github.com/canonical/observability-stack",
		Subdir: "terraform/tempo-operators", Ref: "d1598ff3bdf9a25af69145fd557a913e2a13a314",
		Requires: []string{
			"channel=dev/edge", "model_uuid", "s3_access_key", "s3_secret_key", "s3_endpoint",
		},
	}

	var b strings.Builder
	if err := render(&b, []gallery.Entry{e}); err != nil {
		t.Fatal(err)
	}
	got := b.String()

	if n := strings.Count(got, e.ApplyCommand()); n != 1 {
		t.Errorf("the card's own command appears %d times, want 1\n\n%s", n, got)
	}
	if strings.Contains(got, "Needs ") {
		t.Errorf("the card restates its required inputs as prose\n\n%s", got)
	}
}

func TestRepoLabel(t *testing.T) {
	cases := map[string]string{
		"https://github.com/canonical/observability-stack": "canonical/observability-stack",
		"git@github.com:canonical/spark-k8s-bundle.git":    "canonical/spark-k8s-bundle",
		"https://example.com/m.git":                        "example.com/m",
	}
	for in, want := range cases {
		if got := repoLabel(in); got != want {
			t.Errorf("repoLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
