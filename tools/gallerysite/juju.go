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

// jujuModels maps an entry to the `--var` argument that targets the reader's
// current model. Juju has no single convention for naming a model, and which one
// a module uses is not derivable from the manifest:
//
//   - `model_uuid` takes a UUID string (Loki, Mimir, Tempo, Charmed Spark,
//     HAProxy).
//   - `model` takes an object whose `uuid` selects an existing model (COS,
//     COS Lite). The value must reach Terraform as `{uuid="…"}`, so the inner
//     quotes are escaped and the shell still expands the substitution.
//   - `model` takes a model name (Charmarr).
//
// Each value is a complete shell-quoted token, so it pastes as-is. All forms
// need `jq` on PATH.
var jujuModels = map[string]string{
	"charmarr":        `model="$(juju show-model --format json | jq -r '.[]."short-name"')"`,
	"charmarr-plus":   `model="$(juju show-model --format json | jq -r '.[]."short-name"')"`,
	"charmed-spark":   `model_uuid="$(juju show-model --format json | jq -r '.[]."model-uuid"')"`,
	"cos":             `model={uuid=\"$(juju show-model --format json | jq -r '.[]."model-uuid"')\"}`,
	"cos-lite":        `model={uuid=\"$(juju show-model --format json | jq -r '.[]."model-uuid"')\"}`,
	"haproxy-product": `model_uuid="$(juju show-model --format json | jq -r '.[]."model-uuid"')"`,
	"loki-operators":  `model_uuid="$(juju show-model --format json | jq -r '.[]."model-uuid"')"`,
	"mimir-operators": `model_uuid="$(juju show-model --format json | jq -r '.[]."model-uuid"')"`,
	"tempo-operators": `model_uuid="$(juju show-model --format json | jq -r '.[]."model-uuid"')"`,
}

// jujuValues fills a placeholder with a value the reader's environment already
// holds. A name absent here keeps whatever the manifest declares.
//
// The channel is `dev/edge` because loki, mimir and tempo validate that the
// track is `dev/`; these modules reject `latest/stable`. The LGTM charms
// publish no stable track yet.
var jujuValues = map[string]string{
	"channel":       "dev/edge",
	"s3_access_key": `"$AWS_ACCESS_KEY_ID"`,
	"s3_secret_key": `"$AWS_SECRET_ACCESS_KEY"`,
	"s3_endpoint":   `"$AWS_ENDPOINT_URL"`,
}

// modelPin returns the entry's model pin and the variable carrying it.
func modelPin(e gallery.Entry) (varName, value string, ok bool) {
	v, ok := jujuModels[e.Name]
	if !ok {
		return "", "", false
	}
	name, _, _ := strings.Cut(v, "=")
	return name, v, true
}

// pinIsRequired reports whether the module already demands a model, leaving the
// reader no choice. When false, pinning overrides what the module would have
// done on its own — which is why it is offered rather than imposed.
func pinIsRequired(e gallery.Entry) bool {
	name, _, ok := modelPin(e)
	return ok && slices.Contains(e.RequiresNames(), name)
}

// jujuArgs builds the variant command for an entry: the model is pinned to the
// current one, and each remaining placeholder becomes a value the environment
// supplies. An input with neither keeps the manifest's own default, or failing
// that its placeholder.
func jujuArgs(e gallery.Entry) []string {
	modelName, model, _ := modelPin(e)

	args := []string{"atelier", "apply", e.Name}
	for _, r := range e.Requires {
		name, value, hasValue := strings.Cut(r, "=")
		switch {
		case name == modelName && model != "":
			args = append(args, "--var", model)
		case !hasValue && jujuValues[name] != "":
			args = append(args, "--var", name+"="+jujuValues[name])
		case !hasValue:
			args = append(args, "--var", name+"=<"+name+">")
		default:
			args = append(args, "--var", name+"="+value)
		}
	}
	// Add the pin where the manifest does not already carry the variable.
	if model != "" && !slices.Contains(e.RequiresNames(), modelName) {
		args = append(args, "--var", model)
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
	name, _, ok := modelPin(e)
	if !ok || slices.Contains(e.RequiresNames(), name) {
		return nil
	}
	return []string{name}
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
	b.WriteString("Every module in the [gallery](gallery.md) deploys with the " +
		"[Juju provider](https://registry.terraform.io/providers/juju/juju/latest). " +
		"The cards below are the gallery's own commands, identical to what " +
		"`atelier gallery list` prints and to the gallery page. What Juju adds — " +
		"resolving the model, S3 credentials, and a charm channel from your " +
		"environment — is a variant under each card, collapsed so the two are " +
		"never confused.\n\n")

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
	var required, chosen []string
	for _, e := range entries {
		if _, _, ok := modelPin(e); !ok {
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
	b.WriteString("A Juju module names its model in one of three ways, so the variant under each " +
		"card is shaped for that module: a UUID in `model_uuid`, an object whose `uuid` selects " +
		"an existing model, or a model *name*. The value is read at run time, so one command " +
		"works for whichever model you have switched to.\n\n")

	if len(required) > 0 {
		fmt.Fprintf(b, "For %s the module demands a model, so the variant only fills in which one.\n\n",
			codeList(required))
	}
	if len(chosen) > 0 {
		fmt.Fprintf(b, "For %s the module would otherwise create its own model. The variant overrides "+
			"that — useful when you want the workload in a model you already have, but it is a "+
			"change, not a no-op.\n\n", codeList(chosen))
	}

	b.WriteString("The variants also fill two inputs from your environment:\n\n")
	b.WriteString("- **S3 credentials** — `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and `AWS_ENDPOINT_URL`, for any S3-compatible store (Ceph, MinIO, OpenStack).\n")
	b.WriteString("- **The charm channel** — `dev/edge`. Loki, Mimir and Tempo validate that the track is `dev/` and reject `latest/stable`; the LGTM charms publish no stable track yet.\n\n")
	b.WriteString("Anything left as `<angle-brackets>` has no safe default and is yours to fill in.\n\n")
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

	writeJujuVariant(b, e)
}

// writeJujuVariant writes the collapsed Juju-specific command for a card.
func writeJujuVariant(b *strings.Builder, e gallery.Entry) {
	if _, _, ok := modelPin(e); !ok {
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
