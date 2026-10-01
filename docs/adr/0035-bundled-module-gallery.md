# ADR-0035: Bundled module gallery with a scaffold-and-validate CI gate

## Status

Accepted

## Context

Atelier's value is easiest to see on a real module, but the repository ships no
runnable example of one. The README demos reference
[loki-operators](https://github.com/canonical/loki-operators) and COS Lite, and
the integration tests exercise `prometheus-k8s-operator`, but there is no place
a user can look to find "here is a module, here is the preset that satisfies its
required inputs, here is the command that deploys it".

The product teams already publish known-good values as Terraform-native
`.tfvars` bundles (ADR-0031), and Atelier can apply them with `--var-file`. A
gallery of such presets, paired with the one-liner that uses them, would make the
"quick start" concrete and keep the presets from rotting. Two constraints shape
it:

Throughout, a **preset** is a Terraform-native `.tfvars` bundle (ADR-0031); the
**gallery** is Atelier's curated set of module quick starts, and a gallery entry
may name a preset. The two are deliberately separate nouns.

- Contributing presets to upstream module repositories is out of scope for now;
  the gallery is hosted in Atelier.
- `terraform plan`/`apply` on these modules need a Juju controller on Canonical
  Kubernetes. That is assumed available to users, but it is not available to a
  hosted CI runner without the expensive, flaky Concierge setup the cloud tiers
  already use.

## Decision

### The gallery is an embedded manifest, not committed Markdown

The gallery lives in `internal/gallery/`: a JSON manifest (`gallery.json`) and
the preset bundles under `presets/`, both compiled into the binary with
`go:embed`. Each entry names a module, a pinned full-SHA ref, an optional `--as`
block name, and an optional preset. The deploy command is **derived** from those
fields, never stored, so it cannot drift from them.

The gallery is deliberately machine-first. The human interface is the CLI and the
TUI, not committed per-example Markdown:

- `atelier gallery list` renders each entry — description, module, ref, preset,
  and the `atelier apply <name>` one-liner;
- `atelier module add` and `atelier apply` accept the entry **name** in place of
  a URL, expanding to the entry's module, ref, block, and preset (an explicit
  flag still wins, and a URL or local path is never treated as a name);
- `atelier gallery list --commands` prints the non-applying scaffold form, which
  is what CI runs; and
- the TUI `F` picker offers gallery presets alongside local and repo bundles.

A module that deploys with its defaults needs no preset: the entry simply omits
it.

### Deployment-specific inputs are metadata, not preset values

Some modules declare an input that is real but cannot be known statically — a
Juju model UUID, S3 credentials. A gallery entry lists them under `requires`; its
preset stays free of fake values, because a seeded placeholder looks configured
(the failure ADR-0007's sparse rule exists to avoid). `gallery list` renders a
`--var` placeholder for each required input, so they are visible before anything
runs, and `gallery-check` fills them with placeholders so it can still validate
the entry. Atelier does not synthesize these values and does not probe a provider
for them: how to obtain a model UUID is provider-specific know-how that belongs
to the user's deployment, not to the gallery. `gallery requires <name>` prints
the list, one per line, for the check.

### The gallery is the lowest-precedence `--var-file` source

`--var-file <name>` resolves in four steps, first match wins (ADR-0031
extended):

1. an explicit filesystem path,
2. a personal walk-up `atelier.presets/<name>.tfvars` (nearest ancestor),
3. a bundle committed to the module repository, and
4. a preset bundled with the gallery.

The ordering is most-specific-wins: a user's local bundle overrides everything,
the product team's committed preset overrides the gallery, and the gallery fills
the gap when neither exists. Embedded presets are materialized to a
content-addressed cache directory for the path-based readers; the embedded bytes
remain the source of truth.

### The CI gate: run the scaffold command, then validate

`just gallery-check` (run by a dedicated, Juju-free `gallery` CI job) reads
`atelier gallery list --commands` and, for each entry, runs that exact scaffold
command in a scratch directory, then `terraform init -backend=false && terraform
validate`. Because the command applies the preset, `validate` catches a preset
that names a removed variable or nested key; `--strict` makes top-level binding
problems fatal first.

It deliberately does **not** plan or apply. A pinned SHA freezes the module
schema, so the gate only fails when the module changes or a pin is deliberately
moved; the fix is to bump the `--ref` and refresh the preset. A `gallery-bump`
skill documents that workflow.

### Change filtering

A gallery-only change exercises the gallery, not the binary, so it is excluded
from the integration-tier change filter (the `gallery` job has its own condition,
which runs for any non-docs change).

## Alternatives considered

- **Committed per-example Markdown.** Rejected: verbose, and the command has to
  be parsed back out, with the README acting as a manifest that can drift from
  the fields.
- **A manifest file outside the binary** (e.g. `examples/gallery.json`).
  Rejected: `go:embed` cannot reach a parent directory from an `internal/`
  package, so the gallery could not ship inside the static binary.
- **Commit the generated wrapper per example.** Rejected: it duplicates `module
  add` output and bloats every bump diff.
- **Fetch the gallery from GitHub at runtime.** Rejected: it needs a network and
  ties behaviour to a tag rather than the installed binary.
- **Plan the examples in CI.** Rejected: the Juju provider needs a live
  controller, which is why the existing prometheus/import tiers are `cloud` jobs
  with 90-minute budgets.
- **Contribute the presets upstream.** Rejected for now: it couples the gallery
  to maintainer acceptance and their release cadence.

## Consequences

- The gallery is a maintenance surface: every entry carries a pinned ref and a
  preset that must be refreshed when the module moves. Keeping the count small
  and curated bounds it; the check and the bump skill make the work routine
  rather than silent.
- The gallery becomes a product surface: `atelier gallery list` and the TUI
  picker, not a file a user has to find and copy.
- `atelier presets lint` remains for checking a bundle that is not being applied
  (for example a preset a product team is about to commit upstream).
- Registry module sources remain unsupported, so gallery entries must be git
  URLs; adding registry support is a separate decision.
