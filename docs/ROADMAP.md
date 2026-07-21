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
  (`atelier module …`), prune to sparse form (`atelier tidy`), and clean up
  (`atelier purge`).
- Public git source loading (`atelier module add <url>`); local `source =
  "./..."` paths in a hand-authored `main.tf` are also supported.
- Two-pane TUI with type-appropriate widgets for `string`, `bool`, `number`,
  `object`, `map(string)`, `map(object)`, `list(string)`, `list(object)`,
  `set(...)`, and nullable scalars.
- Sparse-plus-required wrapper writes via `hcl/v2`, with hand-edit
  round-tripping.
- Module candidate discovery (purely heuristic; no upstream manifest).
- Local presets: named bundles of variable values declared in a wrapper-local
  `atelier.local.yaml` (walk-up discovery), applied in bulk via the `F` key,
  then customised per-variable. The upstream module repo is never read for
  Atelier files.
- Provider configuration via `terraform providers schema -json`, including
  sensitive-attribute handling via variable indirection and gitignored
  `secrets.auto.tfvars`.
- Debounced `terraform validate` for inline validation feedback.
- `terraform plan -json` rendering as a module-path tree with attribute diffs
  in a side pane.
- `terraform apply` from the plan view (`A` key): applies the cached plan
  file; errors surfaced in-TUI via `E`.
- Default-change surfacing on ref bump.
- In-TUI ref switching (`R` key): re-clone, `terraform init -upgrade`,
  preserve user overrides, enabling cross-ref upgrade comparison workflows.
- In-TUI output viewing (`O` key): shows planned output values before apply,
  live state values after apply, with syntax-highlighted JSON and scrollable
  navigation. Auto-generates `outputs.tf` to re-export module outputs.
- Single static Go binary; snap packaging.
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

Apply is currently fire-and-forget with a spinner. A future version may add
streaming log output, cancellation (`Ctrl+C` during apply), partial-apply
recovery, and post-apply state inspection.

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

### ~~Sparse output re-export~~ ✓ Implemented

~~Wrappers do not re-export module outputs.~~ Atelier now generates an
`outputs.tf` in the wrapper that forwards all of the module's declared
outputs. The in-TUI output view (`O` key) displays planned values before
apply and live state values after apply, with syntax-highlighted JSON
rendering.

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

### Local presets schema growth

The `atelier.local.yaml` schema is intentionally minimal (`modules:` list with
`path` + `presets`). Candidates for later:

- A user-global presets store (e.g. `~/.config/atelier/`) keyed by source URL,
  complementing walk-up local files.
- A `--presets <path>` override flag.
- Test-driven preset discovery from `.tftest.hcl` run blocks.

## Parked

Threads we explored, set aside, and may revisit with more information.

### "Features" — maintainer-curated configuration surfaces

The original vision included module maintainers declaring **features** —
named higher-level toggles that map to one or more variable settings, with a
proposed mechanism of auto-discovery from `tftest.hcl` run blocks.

**Presets are now shipped, but user-owned.** Users declare `presets:` in a
wrapper-local `atelier.local.yaml` and apply them with `F` in the TUI. Atelier
does not read presets from the upstream module repo (see
[ADR-0022](adr/0022-local-presets.md), superseding ADR-0010). See
[SPEC §11](SPEC.md#11-local-presets-atelierlocalyaml) for schema details and
[examples/atelier.local.yaml](examples/atelier.local.yaml) for a worked
example.

What remains parked:

- Test-driven discovery: automatically deriving presets from `.tftest.hcl`
  run blocks. Works well for *enumerable scenarios* (a test file whose `run`
  blocks enumerate alternative configurations) but poorly for
  *contract-style tests*. Parked until we have more field experience with
  presets.

This is speculative; the actual design will be informed by what users do in
practice.

### Multi-instance wrappers

A wrapper directory holds one module instance today. A user who wants two
COS Lite deployments uses two directories. Multi-instance (multiple `module
{}` blocks in one wrapper, distinguished by name or by `for_each`) is
plausible but the UX is unsettled and there are no concrete users asking for
it.

### Telemetry / opt-in usage reporting

Not present. If added later, must be opt-in and clearly disclosed.

## Out of scope (likely never)

- A web UI. Atelier is a TUI. If a web tool is wanted, it's a different
  project.
- Replacing `terraform apply` with Atelier-native apply. The point of Atelier
  is to feed Terraform a config; it's not in the business of being Terraform.
- Configuration languages other than HCL (CDK, Pulumi YAML, etc.). Atelier is
  a Terraform tool.
- General-purpose form-filling for arbitrary YAML/JSON. Atelier's model is
  specifically Terraform's type system and provider schema.
