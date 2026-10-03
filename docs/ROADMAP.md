# Roadmap

What Atelier does today, what is not yet implemented, and what is parked
pending more thinking. Items in this document are not commitments — they
capture intent so we don't lose the institutional memory built up during
design.

## What Atelier does today

In one line: "configure a public Terraform module visually, write a runnable
wrapper, iterate against `terraform plan` inside the TUI."

- [`SPEC.md`](SPEC.md) is the source of truth for the surface and behaviour.
- All [ADRs](adr/) marked `Status: Accepted` are current decisions.

Concretely:

- The `atelier` CLI: open a wrapper (`atelier`), add/remove/list modules
  (`atelier add|rm|ls`), scaffold-and-deploy a module in one command
  (`atelier apply`), prune to sparse form (`atelier tidy`), list wrappers under
  a directory (`atelier wrappers`), and clean up (`atelier purge`).
- Public git source loading (`atelier add <url>`); local `source =
  "./..."` paths in a hand-authored `main.tf` are also supported.
- **Multi-module composition in one root.** A target that already holds a
  `main.tf` is the additive case for both `add` and `apply`, so a deployment is
  built one module per command: `atelier apply cos-lite`, then
  `atelier apply charmed-spark --dir cos-lite` appends and deploys both from one
  state. `--dir` names the wrapper, so it need not be the CWD
  ([ADR-0044](adr/0044-dir-names-the-wrapper.md)).
- Two-pane TUI with type-appropriate widgets for `string`, `bool`, `number`,
  `object`, `map(string)`, `map(object)`, `list(string)`, `list(object)`,
  `set(...)`, and nullable scalars.
- Sparse-plus-required wrapper writes via `hcl/v2`, with hand-edit
  round-tripping.
- Module candidate discovery (purely heuristic; no upstream manifest).
- A bundled gallery of pinned quick starts (`atelier gallery list`), usable
  wherever a URL is accepted, with `atelier gallery lint` and
  `atelier presets lint` gating each entry's required inputs against its module
  ([ADR-0039](adr/0039-composed-gallery-presets.md)).
- Presets: named `.tfvars` bundles discovered from an ancestor
  `atelier.presets/` directory (walk-up) and from the module repo's `presets/`,
  applied via `--var-file` or the TUI `F` picker, and saved with `S`.
- Debounced `terraform validate` for inline validation feedback.
- `terraform plan -json` rendering as a module-path tree with attribute diffs
  in a side pane.
- `terraform apply` from the plan view (`A` key): applies the cached plan
  file; errors surfaced in-TUI via `E`.
- Default-change surfacing on ref bump.
- In-TUI ref switching (`R` key): re-clone, `terraform init -upgrade`,
  preserve user overrides, enabling cross-ref upgrade comparison workflows.
- Single static Go binary.
- `atelier import [PROVIDER] [flags]`: import a running deployment into
  Terraform state. Discovers live resources via `terraform query`, matches
  them to module resource addresses by name, and runs `terraform import` for
  each. Provider-specific import steps (currently Juju only) handle null
  normalisation, schema version injection, offer defaults, and model UUID
  injection. See [ADR-0027](adr/0027-atelier-import.md) and
  [ADR-0028](adr/0028-provider-specific-import-ids.md).

## Not yet implemented

These have a clear shape but are out of the current scope to keep the surface
small.

### Streaming apply logs and cancellation

Apply and plan now stream terraform stdout to a `ProgressTracker` buffer,
visible via `L` (live logs view). Remaining work: `Ctrl+C` cancellation
during apply, partial-apply recovery, and post-apply state inspection.

### Authenticated git access

Only public repos are supported today. Future additions:

- SSH key auth (default if remote uses `git@…` form).
- `gh auth` integration for GitHub remotes.
- `GITHUB_TOKEN` / `GIT_ASKPASS` env-based auth.

### Terraform Registry module sources

Only git URLs and local paths are supported today. Registry sources
(`namespace/name/provider` form) would require:

- Talking to the registry API to resolve versions.
- Fetching versioned tarballs instead of cloning.
- Mapping the wrapper's `source =` to a registry reference.

### `any` and `tuple([...])` as first-class widgets

These render as read-only HCL with an "edit in `$EDITOR`" affordance today.
Future additions:

- For `any`: a free-text HCL editor with parse validation.
- For `tuple`: a fixed-position row of widgets, one per declared type.

### Empty-vs-null collection toggle

The distinction is currently hidden; the empty state of the widget follows the
variable's declared default. A future version may add an explicit
`[ Empty ] [ Null ]` toggle on the widget header for cases where users need to
express the other interpretation.

### Provider configuration from schema

[ADR-0008](adr/0008-provider-schema-discovery.md) specifies reading the
provider's configuration schema via `terraform providers schema -json` and
presenting its attributes as a `Provider: <name>` pane. Today Atelier only
writes empty stub `provider {}` blocks derived from the module's declared
`required_providers`; the schema-driven pane is not implemented.

### In-TUI output view and `outputs.tf`

An `O`-keyed modal showing planned and live output values, and a generated
`outputs.tf` re-exporting the module's outputs, were specified but are not
implemented. `terraform output` works by running it directly in the wrapper.

### Conditional autoplan

Planning is manual today — the user presses `P`. A future version may add an
adaptive autoplan: the first plan is measured; if under a threshold (e.g.
500ms), subsequent edits trigger debounced autoplans; otherwise stay manual.
The rationale for keeping planning manual for now is documented in
[ADR-0002](adr/0002-author-and-plan-scope.md).

### Inline plan attribute diffs

Plan attribute diffs currently appear in a side pane on selection. A future
version may render them inline within the plan tree, with collapsible
per-attribute rows and syntax highlighting.

### ~~Visual design / aesthetics~~ ✓ Implemented

~~A deliberate post-spec design pass.~~ Atelier uses a Catppuccin Mocha/Latte
adaptive colour palette with semantic role mappings (primary/accent, info,
success, warning, danger). All panels, modals, header, and footer use
consistent rounded borders with focus highlighting. JSON output values have
syntax highlighting. See SPEC.md §14.3 for details.

### Undo / redo

The TUI has no undo. Today the safety net is git: every edit is written to
`main.tf` immediately (§13.1), so `git diff` and `git checkout` are the
recovery path. A future in-memory stack of ~20 logical edit actions
(`Ctrl+Z`/`Ctrl+Shift+Z`), coalescing keystrokes into actions, would need the
ADR treatment first — the auto-save loop makes the action boundary a design
question, not a mechanical one.

### Preset bundle discovery growth

Presets are `.tfvars` bundles ([ADR-0031](adr/0031-presets-as-tfvars-bundles.md)).
Candidates for later:

- A user-global bundle store (e.g. `~/.config/atelier/`) keyed by source URL,
  complementing walk-up `atelier.presets/` directories.
- A `--var-file-dir <path>` override.
- Test-driven bundle discovery from `.tftest.hcl` run blocks.

## Parked

Threads we explored, set aside, and may revisit with more information.

### "Features" — maintainer-curated configuration surfaces

The original vision included module maintainers declaring **features** —
named higher-level toggles that map to one or more variable settings, with a
proposed mechanism of auto-discovery from `tftest.hcl` run blocks.

**Presets are now shipped as `.tfvars` bundles.** Users keep personal bundles
in an ancestor `atelier.presets/` directory, and product repos commit presets
under `<module>/presets/`; both are applied with `--var-file` or the TUI `F`
picker (see [ADR-0031](adr/0031-presets-as-tfvars-bundles.md) and
[SPEC §11](SPEC.md)).

What remains parked:

- Test-driven discovery: automatically deriving presets from `.tftest.hcl`
  run blocks. Works well for *enumerable scenarios* (a test file whose `run`
  blocks enumerate alternative configurations) but poorly for
  *contract-style tests*. Parked until we have more field experience with
  presets.

This is speculative; the actual design will be informed by what users do in
practice.

### Cross-module integration wiring (`atelier integrate`)

A wrapper can hold several modules ([ADR-0015](adr/0015-multi-module-grouping.md)),
and Canonical's product modules integrate through Juju offers — the producer
exposes them, the consumer takes an offer URL. The request was for Atelier to
close that gap: `atelier integrate cos-lite charmed-spark`, reading both
modules' `outputs.tf` and writing the `juju_integration` resources that connect
them.

**Blocked on the modules, which do not declare interfaces yet.** The join key is
the Juju *interface* name, and neither side of a pairing states it as data:

- **Producers** expose *endpoint* names, and only incidentally — an `output
  "offers"` holding whole `juju_offer` resources also carries `.url` and
  `.endpoints`. Even the endpoint is not a literal: `offers.tf` writes
  `endpoints = [module.loki.provides.logging]`, so it resolves through a
  submodule. The interface behind that endpoint is `loki_push_api`, behind
  Grafana's `grafana-dashboard` it is `grafana_dashboard`; interfaces live in
  each charm's `charmcraft.yaml`, which the Terraform module references by
  revision and never surfaces.
- **Consumers** declare a URL or an object of URLs — `cos_offers` as
  `{dashboard, logging, metrics}`, or a single `postgresql_offer_url` — and name
  the interface in the variable's `description` prose. Nothing parses that.

A live `juju show-offers` would map endpoint to interface, but only for a
producer already applied, and it still cannot say what the consumer *requires*.

So a pairing would be a hand-written assertion with no oracle: `terraform
validate` type-checks it, and a wrong pairing fails only later, when Juju
creates the integration, reported in the consuming charm's terms rather than
the module's. That also rules out the gate that keeps the gallery manifest
trustworthy ([ADR-0039](adr/0039-composed-gallery-presets.md)) — a pair table
would be the one class of curated entry in the repo that `just gallery-check`
cannot assert, and therefore the first thing to rot.

Two further reasons not to build it:

- [ADR-0017](adr/0017-inter-module-wiring.md) already rejected automatic wiring
  by name match as "too magical; users should explicitly opt into cross-module
  dependencies". A curated table is that same automatic wiring, with a lookup
  table standing in for the name match.
- **The composition already works, with a person in the loop.** The upstream
  convention is "consumer declares a URL variable, operator pastes the URL", and
  Atelier already carries that value across modules:
  `atelier apply cos-lite --dir stack`, then `atelier apply charmed-spark
  --dir stack --var 'cos_offers={dashboard="admin/<model>.grafana-dashboards",…}'`,
  with object overrides deep-merged ([ADR-0044](adr/0044-dir-names-the-wrapper.md)).
  The missing actor is someone who can read both modules, which is the right
  home for that knowledge until the modules declare it.

**Unblock:** product modules declaring the interface on both sides — a producer
output pairing each offer with its interface, and a consumer input declaring
what it requires — at which point a generic matcher exists and a curated mapping
becomes checkable against the pinned ref. Worth raising with the module teams;
it is their modelling gap, not an Atelier one.

Independently of that, **Atelier cannot read `output` blocks at all**:
`internal/tfvars` parses `variable` blocks and nothing else, and `outputs.tf` is
neither written nor read (SPEC §7.6). Parsing them is the missing
provider-agnostic primitive under ADR-0017's wire suggestions — specified in
SPEC §15, with no implementation. That is worth doing on its own merits: it
surfaces `module.<name>.offers` and friends as referenceable, which makes the
manual wiring above fast without asserting anything the modules do not say.

### Multi-instance wrappers

This is about running **the same module more than once**, not about holding
several modules: a wrapper may already declare `cos_lite` and `charmed_spark`
([ADR-0015](adr/0015-multi-module-grouping.md),
[ADR-0044](adr/0044-dir-names-the-wrapper.md)), and that is supported today.

What is not supported is one module twice. A user who wants two COS Lite
deployments uses two directories. Repeating a module inside one wrapper —
distinguished by block name or by `for_each` — is plausible but the UX is
unsettled and there are no concrete users asking for it.
