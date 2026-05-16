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
- Provider configuration via `terraform providers schema -json`, including
  sensitive-attribute handling via variable indirection and gitignored
  `secrets.auto.tfvars`.
- Debounced `terraform validate` for inline validation feedback.
- `terraform plan -json` rendering as a module-path tree with attribute diffs
  in a side pane.
- Default-change surfacing on ref bump.
- Single static Go binary; snap packaging.

## Deferred to v2 or later

These have a clear shape but are out of v1's scope to keep the surface small
and the timeline tight.

### Inline `terraform apply` in the TUI

v1 stops at plan. Apply happens in the user's shell. v2 may add an in-TUI
apply with streaming logs, cancellation, state-lock handling, and post-apply
error inspection. The wrapper format will not change; this is purely
additive.

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

### Sparse output re-export

v1 wrappers do not re-export module outputs. v2 may add a per-output checkbox
in an "Outputs" tab that generates `output "x" { value = module.<m>.x }`
blocks for selected outputs.

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

### Visual design / aesthetics

A deliberate post-spec design pass. Covers colour palette, focus and dimming,
borders and separators, iconography (Unicode glyphs vs ASCII fallback),
dark/light theme support, and any motion/animation. Reference: Charm's own
ecosystem (gum, glow, soft-serve) for design language.

### Manifest schema growth

v1's `atelier.yaml` schema is intentionally minimal (`modules:` list with
`path`, `name`, `description`, optional `groups`). v2 candidates:

- Variable annotations: friendly labels overriding raw variable names; richer
  per-variable descriptions; value hints / examples.
- Required Atelier version constraint.
- Comment-marker parsing as an alternative to manifest groups (e.g.,
  `## section: TLS` in `variables.tf` as an implicit group divider).

## Parked

Threads we explored, set aside, and may revisit with more information.

### "Features" — maintainer-curated configuration surfaces

The original vision included module maintainers declaring **features** —
named higher-level toggles that map to one or more variable settings, with a
proposed mechanism of auto-discovery from `tftest.hcl` run blocks. We parked
this during design after concluding the concept was under-specified.

What we currently believe:

- Features map most naturally to manifest-declared **presets**: named bundles
  of variable values the user can apply with one action and then tweak.
- Test-driven discovery works well for *enumerable scenarios* (a test file
  whose `run` blocks enumerate alternative configurations, e.g.
  `conditional_ingress.tftest.hcl` with `ingress_all_disabled`,
  `ingress_only_grafana`, etc.). Each `run` block's `variables {}` overrides
  become a candidate preset.
- Test-driven discovery works *poorly* for *contract-style tests*
  (`revision_pin.tftest.hcl` tests an invariant, not a preset). We'd need to
  either ignore those or distinguish them somehow.
- The v1 manifest does not include features; the v1 TUI does not surface
  them. Adding them later does not break the v1 wrapper format.

When we revisit, the v2 manifest extension might look like:

```yaml
modules:
  - path: terraform/cos-lite
    name: "COS Lite"
    features:
      - name: "Ingress all disabled"
        description: "Turns off all ingress integrations."
        derived_from: tests/conditional_ingress.tftest.hcl#ingress_all_disabled
        sets:
          ingress:
            alertmanager: false
            catalogue: false
            grafana: false
            loki: false
            prometheus: false
```

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
