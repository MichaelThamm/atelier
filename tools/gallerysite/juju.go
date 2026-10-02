package main

// Juju-opinionated rendering for the site's Juju page. The manifest,
// `internal/gallery`, and the CLI stay provider-agnostic; this file is the one
// place that knows Juju's idioms. See ADR-0041.
//
// A card's command is `ApplyCommand`, byte-identical to what `atelier gallery
// list` prints and to the gallery page, so the page never shows a command the
// CLI cannot produce. What Juju adds is offered as a collapsed variant under
// each card rather than as the card's own command: pinning a model overrides a
// module's default behaviour on some entries, and that should be a choice the
// reader makes, not the default they are handed.

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/MichaelThamm/atelier/internal/gallery"
)

// jujuModels maps an entry to the `--var` arguments that target the reader's
// current model. Juju has no single convention for naming a model, and which one
// a module uses is not derivable from the manifest:
//
//   - `model_uuid` takes a UUID string (Loki, Mimir, Tempo, Charmed Spark,
//     HAProxy, NetBox, Superset, and most of the product modules).
//   - `model` takes an object whose `uuid` selects an existing model (COS,
//     COS Lite). The value must reach Terraform as `{uuid="…"}`, so the inner
//     quotes are escaped and the shell still expands the substitution.
//   - `model` takes a UUID string (Authentik, GitHub runner).
//   - `model` takes a model name (Charmarr), as does `model_name` (Trino).
//
// An entry carries more than one pin where the model cannot be targeted without
// also changing how the module behaves: Kubeflow only reads `model_uuid` when
// `create_model` is false, so pinning one and not the other deploys into a
// model the module made anyway.
//
// Each value is a complete shell-quoted token, so it pastes as-is. All forms
// need `jq` on PATH.
var jujuModels = map[string][]string{
	"airbyte":         {modelUUIDPin},
	"authentik":       {`model="$(juju show-model --format json | jq -r '.[]."model-uuid"')"`},
	"bingo":           {modelUUIDPin},
	"charmarr":        {modelNamePin},
	"charmarr-plus":   {modelNamePin},
	"charmed-spark":   {modelUUIDPin},
	"cos":             {modelObjectPin},
	"cos-lite":        {modelObjectPin},
	"datahub":         {`k8s_model_uuid="$(juju show-model --format json | jq -r '.[]."model-uuid"')"`},
	"github-runner":   {`model="$(juju show-model --format json | jq -r '.[]."model-uuid"')"`},
	"haproxy-product": {modelUUIDPin},
	"hrms":            {modelUUIDPin},
	"kubeflow":        {`create_model=false`, modelUUIDPin},
	"kubeflow-iam":    {`create_model=false`, modelUUIDPin, `iam_core_model_uuid="$(juju show-model --format json | jq -r '.[]."model-uuid"')"`},
	"loki-operators":  {modelUUIDPin},
	"mimir-operators": {modelUUIDPin},
	"netbox":          {modelUUIDPin},
	"saml-integrator": {modelUUIDPin},
	"superset":        {modelUUIDPin},
	"tempo-operators": {modelUUIDPin},
	"trino":           {`model_name="$(juju show-model --format json | jq -r '.[]."short-name"')"`},
}

// The three model shapes, named for the variable that carries them. A pin is a
// complete `--var` token, so a module calling its variable `model` or
// `model_name` spells its own rather than reusing these.
const (
	modelUUIDPin   = `model_uuid="$(juju show-model --format json | jq -r '.[]."model-uuid"')"`
	modelNamePin   = `model="$(juju show-model --format json | jq -r '.[]."short-name"')"`
	modelObjectPin = `model={uuid=\"$(juju show-model --format json | jq -r '.[]."model-uuid"')\"}`
)

// jujuOwnModel names the entries whose module creates its Juju model
// unconditionally, mapping each to why there is nothing to pin. The page states
// the reason rather than inventing a variant.
var jujuOwnModel = map[string]string{
	"indico": "its product module always creates a Juju model, and declares no input that targets an existing one",
}

// createsOwnModel reports whether the entry's module always creates its model.
func createsOwnModel(e gallery.Entry) bool {
	_, ok := jujuOwnModel[e.Name]
	return ok
}

// jujuValues fills a placeholder with a value the reader's environment already
// holds. A name absent here keeps whatever the manifest declares.
//
// The names are deliberately S3_* rather than the AWS_* names the AWS SDK
// happens to use: nothing here is AWS-specific, and these modules talk to any
// S3-compatible store. A charm channel is equally module-specific — a track is
// valid or not per module — so it keeps its placeholder rather than being
// guessed at.
var jujuValues = map[string]string{
	"s3_access_key": `"$S3_ACCESS_KEY"`,
	"s3_secret_key": `"$S3_SECRET_KEY"`,
	"s3_endpoint":   `"$S3_ENDPOINT"`,
}

// pinVar returns the variable a pin token sets.
func pinVar(pin string) string {
	name, _, _ := strings.Cut(pin, "=")
	return name
}

// pinFor returns the pin setting the named variable, if the entry has one.
func pinFor(e gallery.Entry, name string) (string, bool) {
	for _, p := range jujuModels[e.Name] {
		if pinVar(p) == name {
			return p, true
		}
	}
	return "", false
}

// pinIsRequired reports whether the module already demands a model, leaving the
// reader no choice. When false, pinning overrides what the module would have
// done on its own — which is why it is offered rather than imposed.
func pinIsRequired(e gallery.Entry) bool {
	for _, r := range e.RequiresNames() {
		if _, ok := pinFor(e, r); ok {
			return true
		}
	}
	return false
}

// jujuArgs builds the variant command for an entry: the model is pinned to the
// current one, and each remaining placeholder becomes a value the environment
// supplies. An input with neither keeps the manifest's own default, or failing
// that its placeholder.
func jujuArgs(e gallery.Entry) []string {
	pins := jujuModels[e.Name]

	args := []string{"atelier", "apply", e.Name}
	for _, r := range e.Requires {
		name, value, hasValue := strings.Cut(r, "=")
		if pin, ok := pinFor(e, name); ok {
			args = append(args, "--var", pin)
			continue
		}
		switch {
		case !hasValue && jujuValues[name] != "":
			args = append(args, "--var", name+"="+jujuValues[name])
		case !hasValue:
			args = append(args, "--var", name+"=<"+name+">")
		default:
			args = append(args, "--var", name+"="+value)
		}
	}
	// Add the pins the manifest does not already carry.
	for _, p := range pins {
		if !slices.Contains(e.RequiresNames(), pinVar(p)) {
			args = append(args, "--var", p)
		}
	}
	return args
}

// jujuCommand renders jujuArgs as a multi-line command, one `--var` per
// continuation line. Every line carries indent, which keeps a card's content
// inside the list item the four-space card indent establishes.
func jujuCommand(e gallery.Entry, indent string) string {
	args := jujuArgs(e)
	var b strings.Builder
	b.WriteString(indent + strings.Join(args[:3], " "))
	for i := 3; i+1 < len(args); i += 2 {
		b.WriteString(" \\\n" + indent + "  --var " + args[i+1])
	}
	return b.String()
}

// jujuOptionalVars reports the variables jujuArgs passes that the entry's
// manifest does not list in `requires`. Those are the ones no existing gate
// covers: `atelier gallery lint` already checks the manifest's own inputs
// against the module, so only a pin invented here can rot silently.
func jujuOptionalVars(e gallery.Entry) []string {
	var out []string
	for _, p := range jujuModels[e.Name] {
		if n := pinVar(p); !slices.Contains(e.RequiresNames(), n) {
			out = append(out, n)
		}
	}
	return out
}

// jujuPlaceholders lists the inputs the variant command still leaves to the
// reader, so it never implies it runs unattended when it does not.
func jujuPlaceholders(e gallery.Entry) []string {
	var out []string
	for _, arg := range jujuArgs(e)[3:] {
		if name, value, _ := strings.Cut(arg, "="); strings.HasPrefix(value, "<") {
			out = append(out, name)
		}
	}
	return out
}

// codeList renders names as a backticked, comma-separated list.
func codeList(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "`" + n + "`"
	}
	return strings.Join(out, ", ")
}

// renderJujuPage writes the Juju page: the provider-specific conventions stated
// once at the top, then one card per entry carrying the gallery's own command
// and the Juju variant collapsed beneath it.
func renderJujuPage(w io.Writer, entries []gallery.Entry) error {
	var b strings.Builder
	b.WriteString("# Juju modules\n\n")
	b.WriteString("Gallery entries that deploy with the " +
		"[Juju provider](https://registry.terraform.io/providers/juju/juju/latest) " +
		"take their model and S3 credentials from your environment. Each card shows " +
		"the command from the [gallery](gallery.md); the collapsed variant below it " +
		"resolves the model you have switched to.\n\n")

	writeJujuBanner(&b, entries)

	b.WriteString("<div class=\"grid cards\" markdown>\n\n")
	for _, e := range entries {
		writeJujuCard(&b, e)
	}
	b.WriteString("</div>\n")

	_, err := io.WriteString(w, b.String())
	return err
}

// writeJujuBanner states the Juju conventions once, so a card need not repeat
// them. It says plainly which entries have no choice about the model and which
// are choosing it for the reader.
func writeJujuBanner(b *strings.Builder, entries []gallery.Entry) {
	var required, chosen, own []string
	for _, e := range entries {
		if createsOwnModel(e) {
			own = append(own, e.Name)
			continue
		}
		if pinIsRequired(e) {
			required = append(required, e.Name)
		} else {
			chosen = append(chosen, e.Name)
		}
	}

	b.WriteString("## Deploying into your current model\n\n")
	b.WriteString("You need `juju` and `jq` on your `PATH`, and a model to deploy into:\n\n")
	b.WriteString("```bash\njuju switch <your-model>\n```\n\n")
	b.WriteString("Juju modules name their model in one of three ways — a UUID in `model_uuid`, " +
		"an object whose `uuid` selects a model, or a model *name* — so the variant " +
		"under each card is shaped for that module.\n\n")

	// Counts, not name lists: as the gallery grows these two groups reach
	// fifteen and seven entries, and a card already states its own case in the
	// variant's label. Naming them here was redundant and stopped being readable.
	if len(required) > 0 {
		fmt.Fprintf(b, "On %d of these the module demands a model, so the variant only fills in "+
			"which one.\n\n", len(required))
	}
	if len(chosen) > 0 {
		fmt.Fprintf(b, "On %d the module would otherwise create its own model. The variant "+
			"overrides that — a change, not a no-op.\n\n", len(chosen))
	}
	if len(own) > 0 {
		b.WriteString("These entries have no variant:\n\n")
		for _, n := range own {
			fmt.Fprintf(b, "-   `%s` — %s\n", n, jujuOwnModel[n])
		}
		b.WriteString("\n")
	}

	b.WriteString("Variants also read `S3_ACCESS_KEY`, `S3_SECRET_KEY`, and `S3_ENDPOINT` from " +
		"your environment. Anything left as `<angle-brackets>` is yours to fill in.\n\n")
}

// writeJujuCard writes one entry: the gallery's own command, then the Juju
// variant in a collapsed block.
func writeJujuCard(b *strings.Builder, e gallery.Entry) {
	fmt.Fprintf(b, "-   __%s__\n\n", e.Name)
	b.WriteString("    ---\n\n")
	fmt.Fprintf(b, "    %s\n\n", e.Description)

	fmt.Fprintf(b, "    [:octicons-mark-github-16: %s](%s)\n\n", repoLabel(e.Module), e.Module)

	meta := make([]string, 0, 4)
	if e.Subdir != "" {
		meta = append(meta, "`"+e.Subdir+"`")
	}
	meta = append(meta, "pinned `"+e.ShortRef()+"`")
	if len(e.Presets) > 0 {
		quoted := make([]string, len(e.Presets))
		for i, p := range e.Presets {
			quoted[i] = "`" + p + "`"
		}
		meta = append(meta, "presets "+strings.Join(quoted, ", "))
	}
	fmt.Fprintf(b, "    %s\n\n", strings.Join(meta, " · "))

	// The card's own command is the manifest's derivation, unaltered.
	fmt.Fprintf(b, "    ```bash\n    %s\n    ```\n\n", e.ApplyCommand())

	if createsOwnModel(e) {
		fmt.Fprintf(b, "    No variant: %s.\n\n", jujuOwnModel[e.Name])
		return
	}

	writeJujuVariant(b, e)
}

// writeJujuVariant writes the collapsed Juju-specific command for a card.
func writeJujuVariant(b *strings.Builder, e gallery.Entry) {
	// An entry with no pin has no variant to offer. Reaching here without one
	// would render an empty "Deploy into your current model" block.
	if len(jujuModels[e.Name]) == 0 {
		return
	}

	label := "Deploy into your current model"
	if !pinIsRequired(e) {
		label += " (instead of creating one)"
	}
	// An admonition rather than raw <details>: raw HTML inside a card's list
	// item is wrapped in a <p>, which closes the element early and leaks the
	// body onto the page.
	fmt.Fprintf(b, "    ??? \"%s\"\n\n", label)

	if !pinIsRequired(e) {
		b.WriteString("        This overrides the module's own default; it is not a no-op.\n\n")
	}
	if placeholders := jujuPlaceholders(e); len(placeholders) > 0 {
		fmt.Fprintf(b, "        You still supply %s.\n\n", codeList(placeholders))
	}
	fmt.Fprintf(b, "        ```bash\n%s\n        ```\n\n", jujuCommand(e, "        "))
}
