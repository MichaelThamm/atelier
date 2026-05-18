# Roadmap

What Atelier ships in v1, what is intentionally deferred, and what is parked
pending more thinking. Items in this document are not commitments — they
capture intent so we don't lose the institutional memory built up during
design.

## v1 scope (what we are committing to)

The v1 release is "configure a public Terraform module visually, write a
runnable wrapper, iterate against `terraform plan` inside the TUI."

- [`SPEC.md`](SPEC.md) is the source of truth for v1 surface and behaviour.
- All [ADRs](adr/) marked `Status: Accepted` are v1 decisions.

Concretely, v1 ships:

- The `atelier` CLI with `atelier` and `atelier init <source>` subcommands.
- Public git source loading; `--source <path>` for local development.
- Two-pane TUI with type-appropriate widgets for `string`, `bool`, `number`,
  `object`, `map(string)`, `map(object)`, `list(string)`, `list(object)`,
  `set(...)`, and nullable scalars.
- Sparse-plus-required wrapper writes via `hcl/v2`, with hand-edit
  round-tripping.
- Module candidate discovery (heuristic + `atelier.yaml` manifest override).
- Manifest-declared presets: named bundles of variable values users can apply
  in bulk via the `F` key, then customise per-variable.
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

## Deferred to v2 or later

These have a clear shape but are out of v1's scope to keep the surface small
and the timeline tight.

### Streaming apply logs and cancellation

v1's apply is fire-and-forget with a spinner. v2 may add streaming log
output, cancellation (`Ctrl+C` during apply), partial-apply recovery, and
post-apply state inspection.

### Authenticated git access

v1 supports public repos only. v2 adds:

- SSH key auth (default if remote uses `git@…` form).
- `gh auth` integration for GitHub remotes.
- `GITHUB_TOKEN` / `GIT_ASKPASS` env-based auth.

### Terraform Registry module sources

v1 supports git URLs and local paths. v2 adds registry sources
(`namespace/name/provider` form), which require:

- Talking to the registry API to resolve versions.
- Fetching versioned tarballs instead of cloning.
- Mapping the wrapper's `source =` to a registry reference.

### `any` and `tuple([...])` as first-class widgets

v1 renders these as read-only HCL with an "edit in `$EDITOR`" affordance. v2
adds:

- For `any`: a free-text HCL editor with parse validation.
- For `tuple`: a fixed-position row of widgets, one per declared type.

### Empty-vs-null collection toggle

v1 hides the distinction; the empty state of the widget follows the variable's
declared default. v2 may add an explicit `[ Empty ] [ Null ]` toggle on the
widget header for cases where users need to express the other interpretation.

### ~~Sparse output re-export~~ ✓ Implemented

~~v1 wrappers do not re-export module outputs.~~ Atelier now generates an
`outputs.tf` in the wrapper that forwards all of the module's declared
outputs. The in-TUI output view (`O` key) displays planned values before
apply and live state values after apply, with syntax-highlighted JSON
rendering.

### Conditional autoplan

v1 requires the user to press `P` to plan. v2 may add an adaptive autoplan:
the first plan is measured; if under a threshold (e.g. 500ms), subsequent
edits trigger debounced autoplans; otherwise stay manual. The current rationale
for not doing autoplan in v1 is documented in
[ADR-0002](adr/0002-author-and-plan-scope.md).

### Inline plan attribute diffs

v1 puts plan attribute diffs in a side pane on selection. v2 may render them
inline within the plan tree, with collapsible per-attribute rows and syntax
highlighting.

### ~~Visual design / aesthetics~~ ✓ Implemented

~~A deliberate post-spec design pass.~~ Atelier uses a Catppuccin Mocha/Latte
adaptive colour palette with semantic role mappings (primary/accent, info,
success, warning, danger). All panels, modals, header, and footer use
consistent rounded borders with focus highlighting. JSON output values have
syntax highlighting. See SPEC.md §14.3 for details.

### Manifest schema growth

v1's `atelier.yaml` schema is intentionally minimal (`modules:` list with
`path`, `name`, `description`, optional `presets`). v2
candidates:

- Variable annotations: friendly labels overriding raw variable names; richer
  per-variable descriptions; value hints / examples.
- Required Atelier version constraint.
- Test-driven preset discovery from `.tftest.hcl` run blocks.

## Parked

Threads we explored, set aside, and may revisit with more information.

### "Features" — maintainer-curated configuration surfaces

The original vision included module maintainers declaring **features** —
named higher-level toggles that map to one or more variable settings, with a
proposed mechanism of auto-discovery from `tftest.hcl` run blocks.

**Presets are now shipped.** Module maintainers declare `presets:` in
`atelier.yaml` and users apply them with `F` in the TUI. See [SPEC §11](SPEC.md#11-manifest-schema-atelieryaml)
for schema details and [docs/examples/](examples/) for a worked example.

What remains parked:

- Test-driven discovery: automatically deriving presets from `.tftest.hcl`
  run blocks. Works well for *enumerable scenarios* (a test file whose `run`
  blocks enumerate alternative configurations) but poorly for
  *contract-style tests*. Deferred until we have more field experience with
  manifest-declared presets.

This is speculative; the actual design will be informed by what users do with
v1.

### Multi-instance wrappers

v1 supports one module instance per wrapper directory. A user who wants two
COS Lite deployments uses two directories. Multi-instance (multiple `module
{}` blocks in one wrapper, distinguished by name or by `for_each`) is
plausible but the UX is unsettled and there are no concrete users asking for
it.

### Telemetry / opt-in usage reporting

Not present in v1. If added later, must be opt-in and clearly disclosed.

## Out of scope (likely never)

- A web UI. Atelier is a TUI. If a web tool is wanted, it's a different
  project.
- Replacing `terraform apply` with Atelier-native apply. The point of Atelier
  is to feed Terraform a config; it's not in the business of being Terraform.
- Configuration languages other than HCL (CDK, Pulumi YAML, etc.). Atelier is
  a Terraform tool.
- General-purpose form-filling for arbitrary YAML/JSON. Atelier's model is
  specifically Terraform's type system and provider schema.
