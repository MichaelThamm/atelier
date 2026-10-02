package main

// Juju-opinionated rendering of the gallery. The manifest, `internal/gallery`,
// and the CLI stay provider-agnostic; this file is the one place that knows
// Juju's idioms, so the site can offer commands that deploy into the model the
// reader is already switched to. See ADR-0041.

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/MichaelThamm/atelier/internal/gallery"
)

// jujuModels maps an entry to the `--var` argument pinning the deployment to the
// reader's current model. Juju has no single convention for naming a model, and
// which one a module uses is not derivable from the manifest:
//
//   - `model_uuid` takes a UUID string (Loki, Mimir, Tempo, Charmed Spark,
//     HAProxy).
//   - `model` takes an object whose `uuid` selects an existing model (COS,
//     COS Lite). The value must reach Terraform as `{uuid="…"}`, so the inner
//     quotes are escaped and the shell still expands the substitution.
//   - `model` takes a model name (Charmarr).
//
// Each value is a complete shell-quoted token, so a card pastes as-is. Both
// forms need `jq` on PATH.
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

// jujuValues gives a required input a value the reader's environment already
// holds, so the command has no placeholder to fill in. A name absent here keeps
// whatever the entry's own manifest declares.
var jujuValues = map[string]string{
	"channel":       "latest/stable",
	"s3_access_key": `"$AWS_ACCESS_KEY_ID"`,
	"s3_secret_key": `"$AWS_SECRET_ACCESS_KEY"`,
	"s3_endpoint":   `"$AWS_ENDPOINT_URL"`,
}

// jujuNotes flag an entry whose behaviour changes once the model is pinned, so
// pinning it never reads as a no-op it is not.
var jujuNotes = map[string]string{
	"charmed-spark": "Pinning the model makes this module deploy into your current model instead of creating one.",
}

// jujuArgs builds the apply command for an entry with Juju idioms applied: the
// model is always pinned to the current one, and each remaining placeholder
// becomes a value the environment supplies. Inputs with neither keep the
// manifest's own default or, failing that, their placeholder.
func jujuArgs(e gallery.Entry) []string {
	model, hasModel := jujuModels[e.Name]
	modelName, _, _ := strings.Cut(model, "=")

	args := []string{"atelier", "apply", e.Name}
	for _, r := range e.Requires {
		name, value, hasValue := strings.Cut(r, "=")
		switch {
		case hasModel && name == modelName:
			args = append(args, "--var", model)
		case !hasValue && jujuValues[name] != "":
			args = append(args, "--var", name+"="+jujuValues[name])
		case !hasValue:
			args = append(args, "--var", name+"=<"+name+">")
		default:
			args = append(args, "--var", name+"="+value)
		}
	}
	// Pin the model even when the manifest does not list it: a module may take
	// it as an optional input, and the point of this page is the current model.
	if hasModel && !slices.Contains(e.RequiresNames(), modelName) {
		args = append(args, "--var", model)
	}
	return args
}

// jujuCommand renders jujuArgs as a multi-line command, one `--var` per
// continuation line, so a long card stays readable and copyable. Every line is
// prefixed with indent, which keeps a card's command inside the list item the
// four-space card indent establishes.
func jujuCommand(e gallery.Entry, indent string) string {
	args := jujuArgs(e)
	var b strings.Builder
	b.WriteString(indent + args[0] + " " + args[1] + " " + args[2])
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
	model, ok := jujuModels[e.Name]
	if !ok {
		return nil
	}
	name, _, _ := strings.Cut(model, "=")
	if slices.Contains(e.RequiresNames(), name) {
		return nil
	}
	return []string{name}
}

// renderJujuPage writes the Juju page: the prerequisites, the convention the
// commands follow, and one Material grid card per entry.
func renderJujuPage(w io.Writer, entries []gallery.Entry) error {
	var b strings.Builder
	b.WriteString("# Juju modules\n\n")
	b.WriteString("Every module below deploys with the [Juju provider](https://registry.terraform.io/providers/juju/juju/latest), " +
		"so unlike the [gallery](gallery.md) — which stays provider-agnostic and leaves a " +
		"placeholder for every value you must supply — the commands here are ready to run. " +
		"Each one deploys into the model you are already switched to.\n\n")

	b.WriteString("## Before you start\n\n")
	b.WriteString("You need `juju` and `jq` on your `PATH`, a controller you can reach, and a current model:\n\n")
	b.WriteString("```bash\njuju switch <your-model>   # the model to deploy into\n```\n\n")

	b.WriteString("## What the commands assume\n\n")
	b.WriteString("- **The model is your current one.** The model UUID is read at run time, so the same command works for every model you switch to.\n")
	b.WriteString("- **S3 credentials come from the environment.** Set `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and `AWS_ENDPOINT_URL` for any S3-compatible store (Ceph, MinIO, OpenStack).\n")
	b.WriteString("- **Charms deploy from `latest/stable`.** Change `--var channel` to `latest/edge` or a pinned track like `2/stable` to move off it.\n")
	b.WriteString("- **Anything else is left to you.** A placeholder like `--var vpn_provider=<vpn_provider>` marks an input with no safe default; Atelier opens the editor on it.\n\n")

	b.WriteString("<div class=\"grid cards\" markdown>\n\n")
	for _, e := range entries {
		writeJujuCard(&b, e)
	}
	b.WriteString("</div>\n")

	_, err := io.WriteString(w, b.String())
	return err
}

// writeJujuCard writes one entry as a card carrying its model pin and command.
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
		meta = append(meta, "presets `"+strings.Join(e.Presets, "`, `")+"`")
	}
	fmt.Fprintf(b, "    %s\n\n", strings.Join(meta, " · "))

	if note, ok := jujuNotes[e.Name]; ok {
		fmt.Fprintf(b, "    !!! note\n        %s\n\n", note)
	}

	if placeholders := jujuPlaceholders(e); len(placeholders) > 0 {
		fmt.Fprintf(b, "    You supply %s.\n\n", codeList(placeholders))
	}

	fmt.Fprintf(b, "    ```bash\n%s\n    ```\n\n", jujuCommand(e, "    "))
}

// jujuPlaceholders lists the inputs the command still leaves to the reader, so
// a card never implies it runs unattended when it does not.
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
