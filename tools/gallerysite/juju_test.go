package main

import (
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/gallery"
)

// The three shapes Juju modules use to name a model. Atelier cannot infer which
// a module takes, so each renders its own form and the value must survive the
// shell and Terraform's expression parser.
const (
	uuidVar  = `model_uuid="$(juju show-model --format json | jq -r '.[]."model-uuid"')"`
	modelObj = `model={uuid=\"$(juju show-model --format json | jq -r '.[]."model-uuid"')\"}`
	modelNam = `model="$(juju show-model --format json | jq -r '.[]."short-name"')"`
)

func TestJujuArgs_modelShapePerEntry(t *testing.T) {
	cases := []struct {
		name string
		e    gallery.Entry
		want []string
	}{
		{
			// model_uuid is required here, so it is already in requires.
			name: "required uuid input",
			e:    gallery.Entry{Name: "loki-operators", Requires: []string{"model_uuid", "channel"}},
			want: []string{"atelier", "apply", "loki-operators",
				"--var", uuidVar,
				"--var", "channel=latest/stable"},
		},
		{
			// The manifest says nothing about the model; the pin is added.
			name: "optional uuid input absent from requires",
			e:    gallery.Entry{Name: "cos-lite"},
			want: []string{"atelier", "apply", "cos-lite", "--var", modelObj},
		},
		{
			// model is a name here, not a UUID: the pin must not use the uuid form.
			name: "model name",
			e:    gallery.Entry{Name: "charmarr", Requires: []string{"model", "storage_backend=storage-class"}},
			want: []string{"atelier", "apply", "charmarr",
				"--var", modelNam,
				"--var", "storage_backend=storage-class"},
		},
		{
			// A required model_uuid already in requires must not be pinned twice.
			name: "model pin already in requires is not repeated",
			e:    gallery.Entry{Name: "tempo-operators", Requires: []string{"model_uuid"}},
			want: []string{"atelier", "apply", "tempo-operators", "--var", uuidVar},
		},
		{
			name: "s3 credentials come from the environment",
			e:    gallery.Entry{Name: "cos", Requires: []string{"s3_access_key", "s3_secret_key", "s3_endpoint"}},
			want: []string{"atelier", "apply", "cos",
				"--var", `s3_access_key="$AWS_ACCESS_KEY_ID"`,
				"--var", `s3_secret_key="$AWS_SECRET_ACCESS_KEY"`,
				"--var", `s3_endpoint="$AWS_ENDPOINT_URL"`,
				"--var", modelObj},
		},
		{
			// Not a Juju-specific input: it keeps its placeholder.
			name: "an input with no juju value keeps its placeholder",
			e:    gallery.Entry{Name: "charmarr", Requires: []string{"vpn_provider"}},
			want: []string{"atelier", "apply", "charmarr",
				"--var", "vpn_provider=<vpn_provider>",
				"--var", modelNam},
		},
		{
			// A manifest value the entry ships is more specific than a Juju default.
			name: "a manifest default wins over the juju value",
			e:    gallery.Entry{Name: "charmarr", Requires: []string{"channel=2/stable"}},
			want: []string{"atelier", "apply", "charmarr",
				"--var", "channel=2/stable",
				"--var", modelNam},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := jujuArgs(c.e)
			if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
				t.Errorf("jujuArgs = %q\nwant %q", got, c.want)
			}
		})
	}
}

// Every shipped entry must be pin-able: a page whose commands are the point
// cannot carry an entry that silently deploys somewhere else.
func TestJujuModels_coverEveryShippedEntry(t *testing.T) {
	entries, err := gallery.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, ok := jujuModels[e.Name]; !ok {
			t.Errorf("entry %q has no Juju model pin", e.Name)
		}
	}
}

// The pin's value is what Terraform receives, so the shell must not eat the
// quotes an object value needs.
func TestJujuArgs_objectPinSurvivesTheShell(t *testing.T) {
	e := gallery.Entry{Name: "cos-lite"}
	args := jujuArgs(e)
	pin := args[len(args)-1]
	if !strings.HasPrefix(pin, `model={uuid=\"`) || !strings.HasSuffix(pin, `\"}`) {
		t.Errorf("object pin = %q, want the inner quotes escaped", pin)
	}
}

// jujuOptionalVars is the drift gate's input, so it must name exactly the pins
// the manifest does not already cover.
func TestJujuOptionalVars(t *testing.T) {
	cases := []struct {
		name string
		e    gallery.Entry
		want []string
	}{
		{"pin invented beyond requires", gallery.Entry{Name: "cos-lite"}, []string{"model"}},
		{"pin already covered by requires", gallery.Entry{Name: "loki-operators", Requires: []string{"model_uuid"}}, nil},
		{"no pin at all", gallery.Entry{Name: "unknown"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := jujuOptionalVars(c.e)
			if len(got) != len(c.want) {
				t.Fatalf("jujuOptionalVars = %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("jujuOptionalVars[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

// A card that implies it runs unattended when it does not is worse than one that
// says what is missing.
func TestJujuPlaceholders(t *testing.T) {
	got := jujuPlaceholders(gallery.Entry{Name: "charmarr", Requires: []string{"model", "vpn_provider", "cluster_cidrs"}})
	if len(got) != 2 || got[0] != "vpn_provider" || got[1] != "cluster_cidrs" {
		t.Errorf("jujuPlaceholders = %v, want [vpn_provider cluster_cidrs]", got)
	}
	if got := jujuPlaceholders(gallery.Entry{Name: "cos-lite"}); len(got) != 0 {
		t.Errorf("jujuPlaceholders(cos-lite) = %v, want none", got)
	}
}

func TestJujuCommand_indentsEveryLine(t *testing.T) {
	e := gallery.Entry{Name: "loki-operators", Requires: []string{"model_uuid", "channel"}}
	for _, line := range strings.Split(jujuCommand(e, "    "), "\n") {
		if !strings.HasPrefix(line, "    ") {
			t.Errorf("line %q is not indented, so it would leave the card's list item", line)
		}
	}
}

func TestRenderJujuPage(t *testing.T) {
	var b strings.Builder
	entries := []gallery.Entry{
		{Name: "cos-lite", Description: "COS Lite.", Module: "https://github.com/canonical/observability-stack",
			Subdir: "terraform/cos-lite", Ref: "d1598ff3bdf9a25af69145fd557a913e2a13a314"},
		{Name: "charmed-spark", Description: "Charmed Spark.", Module: "https://github.com/canonical/spark-k8s-bundle",
			Ref: "6a39d83883f92e0581f5c94144c6ddee2a16e140"},
	}
	if err := renderJujuPage(&b, entries); err != nil {
		t.Fatal(err)
	}
	got := b.String()

	for _, want := range []string{
		"# Juju modules",
		`<div class="grid cards" markdown>`,
		// The page states its prerequisites, since the commands need both tools.
		"You need `juju` and `jq` on your `PATH`",
		"-   __cos-lite__",
		"atelier apply cos-lite",
		// A behaviour change is called out rather than left to surprise.
		"!!! note",
		"instead of creating one",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered page missing %q\n\n%s", want, got)
		}
	}
	// The agnostic page is linked, not duplicated: the two pages coexist.
	if !strings.Contains(got, "(gallery.md)") {
		t.Error("the Juju page should link the provider-agnostic gallery")
	}
}
