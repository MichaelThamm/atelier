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
				"--var", "channel=dev/edge"},
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

// A card must show the manifest's own command, so the site never displays a
// command `atelier gallery list` would not print.
func TestJujuCard_showsTheGalleryCommand(t *testing.T) {
	var b strings.Builder
	e := gallery.Entry{
		Name:        "charmarr",
		Description: "Charmarr.",
		Module:      "https://github.com/charmarr/charmarr",
		Ref:         "e01391758b45e50a1b018154490bec1b99636434",
		Requires:    []string{"model", "vpn_provider"},
	}
	writeJujuCard(&b, e)
	got := b.String()

	if want := "atelier apply charmarr --var model=<model> --var vpn_provider=<vpn_provider>"; !strings.Contains(got, want) {
		t.Errorf("card must carry the gallery's own command %q\n\n%s", want, got)
	}
	// The Juju variant is present but collapsed and labelled as such. An
	// admonition renders as <details>; raw <details> inside a card's list item
	// is wrapped in a <p>, which closes the element early.
	if !strings.Contains(got, "??? \"Deploy into your current model\"") {
		t.Errorf("the variant must be a collapsed, labelled block\n\n%s", got)
	}
	if strings.Contains(got, "<details>") {
		t.Errorf("raw <details> leaks onto the page; use an admonition\n\n%s", got)
	}
}

// Pinning a model that the module would have created is a change in behaviour,
// so the card says so rather than presenting it as equivalent.
func TestJujuCard_flagsAChangeInBehaviour(t *testing.T) {
	var b strings.Builder
	writeJujuCard(&b, gallery.Entry{Name: "cos-lite", Module: "https://x/y", Ref: "abc"})
	got := b.String()
	if !strings.Contains(got, "instead of creating one") {
		t.Errorf("cos-lite's pin overrides its default; the card must say so\n\n%s", got)
	}
	if !strings.Contains(got, "not a no-op") {
		t.Errorf("the override must be stated inside the collapsed block\n\n%s", got)
	}

	b.Reset()
	writeJujuCard(&b, gallery.Entry{Name: "loki-operators", Module: "https://x/y", Ref: "abc",
		Requires: []string{"model_uuid"}})
	got = b.String()
	// A required model leaves no choice, so the variant must not claim to change
	// anything, and must not offer to create one.
	if strings.Contains(got, "not a no-op") || strings.Contains(got, "instead of creating one") {
		t.Errorf("a required model is not a behaviour change; the card overstates it\n\n%s", got)
	}
}

// The banner states the Juju conventions once, and is explicit about which
// entries have no choice about the model.
func TestRenderJujuPage_bannerStatesConventionsOnce(t *testing.T) {
	var b strings.Builder
	entries := []gallery.Entry{
		{Name: "loki-operators", Module: "https://x/y", Ref: "abc", Requires: []string{"model_uuid"}},
		{Name: "cos-lite", Module: "https://x/y", Ref: "abc"},
	}
	if err := renderJujuPage(&b, entries); err != nil {
		t.Fatal(err)
	}
	got := b.String()

	for _, want := range []string{
		"# Juju modules",
		"## Deploying into your current model",
		"You need `juju` and `jq` on your `PATH`",
		"For `loki-operators` the module demands a model",
		"For `cos-lite` the module would otherwise create its own model",
		"`AWS_ENDPOINT_URL`",
		"dev/edge",
		`<div class="grid cards" markdown>`,
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

// Every shipped entry gets a variant, or the page is inconsistent about which
// cards can deploy into an existing model.
func TestJujuCard_everyShippedEntryHasAVariant(t *testing.T) {
	entries, err := gallery.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, _, ok := modelPin(e); !ok {
			t.Errorf("entry %q has no Juju model variant", e.Name)
		}
	}
}
