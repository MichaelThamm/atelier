package main

// Juju-opinionated rendering for the gallery page. The manifest,
// `internal/gallery`, and the CLI stay provider-agnostic; this file is the one
// place that knows Juju's idioms. See ADR-0041 and ADR-0057.
//
// A card's command is `ApplyCommand`, byte-identical to what `atelier gallery
// list` prints, so the page never shows a command the CLI cannot produce. What
// Juju adds is offered as a collapsed variant under each card rather than as the
// card's own command: pinning a model overrides a module's default behaviour on
// some entries, and that should be a choice the reader makes, not the default
// they are handed. The conventions every variant shares are stated once in a
// collapsed block above the grid.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelThamm/atelier/internal/gallery"
)

// jujuModels maps an entry to the `--var` arguments that target the reader's
// current model. Juju has no single convention for naming a model, and which one
// a module uses is not derivable from the manifest: `model_uuid` takes a UUID
// string, `model` takes an object whose `uuid` selects an existing model (COS,
// COS Lite), and `model` or `model_name` takes a model *name* (Charmarr, Trino).
// Authentik and the GitHub runner take a UUID string in `model`.
//
// An entry carries more than one pin where the model cannot be targeted without
// also changing how the module behaves: Kubeflow only reads `model_uuid` when
// `create_model` is false, so pinning one and not the other deploys into a
// model the module made anyway.
//
// Each pin reads the model from the environment, which the banner exports once.
var jujuModels = map[string][]string{
	"airbyte":         {uuidPin("model_uuid")},
	"authentik":       {uuidPin("model")},
	"bingo":           {uuidPin("model_uuid")},
	"charmarr":        {modelNamePin},
	"charmarr-plus":   {modelNamePin},
	"charmed-spark":   {uuidPin("model_uuid")},
	"cos":             {modelObjectPin},
	"cos-lite":        {modelObjectPin},
	"datahub":         {uuidPin("k8s_model_uuid")},
	"github-runner":   {uuidPin("model")},
	"haproxy-product": {uuidPin("model_uuid")},
	"hrms":            {uuidPin("model_uuid")},
	"kubeflow":        {`create_model=false`, uuidPin("model_uuid")},
	"kubeflow-iam":    {`create_model=false`, uuidPin("model_uuid"), uuidPin("iam_core_model_uuid")},
	"loki-operators":  {uuidPin("model_uuid")},
	"mimir-operators": {uuidPin("model_uuid")},
	"netbox":          {uuidPin("model_uuid")},
	"saml-integrator": {uuidPin("model_uuid")},
	"superset":        {uuidPin("model_uuid")},
	"tempo-operators": {uuidPin("model_uuid")},
	"trino":           {`model_name="$CURRENT_MODEL_NAME"`},
}

// uuidPin sets the named variable to the current model's UUID. The model is
// exported once by the banner, so a pin names a variable rather than resolving
// the model itself.
func uuidPin(name string) string { return name + `="$CURRENT_MODEL"` }

// The remaining model shapes, named for the variable that carries them. A pin is
// a complete `--var` token, so a module calling its variable `model` or
// `model_name` spells its own rather than reusing these. The object's inner
// quotes are escaped so the shell still expands the variable.
const (
	modelNamePin   = `model="$CURRENT_MODEL_NAME"`
	modelObjectPin = `model={uuid=\"$CURRENT_MODEL\"}`
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

// writeJujuBanner states the Juju conventions once, in a collapsed block above
// the grid, so a card need not repeat them and the page's neutral intro stays
// the default. It says plainly which entries have no choice about the model and
// which are choosing it for the reader.
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

	var inner strings.Builder
	inner.WriteString("Switch to the model you want to deploy into, then export it once for every variant below — `juju` and `jq` must be on your `PATH`:\n\n")
	inner.WriteString("```bash\njuju switch <your-model>\n")
	inner.WriteString("export CURRENT_MODEL=\"$(juju show-model --format json | jq -r '.[].\"model-uuid\"')\"\n")
	inner.WriteString("export CURRENT_MODEL_NAME=\"$(juju show-model --format json | jq -r '.[].\"short-name\"')\"\n")
	inner.WriteString("```\n\n")
	inner.WriteString("Juju modules name their model in one of three ways — a UUID in `model_uuid`, an object whose `uuid` selects a model, or a model *name* — so the variant under each card passes `$CURRENT_MODEL`, or `$CURRENT_MODEL_NAME` for the modules that want a name.\n\n")

	// Counts, not name lists: as the gallery grows these two groups reach
	// fifteen and seven entries, and a card already states its own case in the
	// variant's label. Naming them here was redundant and stopped being readable.
	if len(required) > 0 {
		fmt.Fprintf(&inner, "On %d of these the module demands a model, so the variant only fills in which one.\n\n", len(required))
	}
	if len(chosen) > 0 {
		fmt.Fprintf(&inner, "On %d the module would otherwise create its own model. The variant overrides that — a change, not a no-op.\n\n", len(chosen))
	}
	if len(own) > 0 {
		inner.WriteString("These entries have no variant:\n\n")
		for _, n := range own {
			fmt.Fprintf(&inner, "-   `%s` — %s\n", n, jujuOwnModel[n])
		}
		inner.WriteString("\n")
	}

	inner.WriteString("Variants also read `S3_ACCESS_KEY`, `S3_SECRET_KEY`, and `S3_ENDPOINT` from your environment. Anything left as `<angle-brackets>` is yours to fill in.\n\n")

	fmt.Fprintf(b, "??? \"Deploying into a Juju model\"\n\n%s", indentBlock(inner.String(), "    "))
}

// indentBlock prefixes every non-blank line with prefix, so a block nests inside
// an admonition or a card's list item. Blank lines stay empty, which Markdown
// needs between paragraphs.
func indentBlock(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
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
