# ADR-0031: Opt-in `.tfvars` pass-through wrapper mode

## Status

Proposed

This ADR scopes an **opt-in** alternative to the classic wrapper shape. It does
not supersede [ADR-0007](0007-sparse-wrapper-write-rule.md) or
[ADR-0022](0022-local-presets.md); the classic module-argument shape remains the
default. A follow-up ADR would be needed to make the pass-through shape the
default or to retire the classic one.

## Context

The design review captured in issue #17 asked whether Atelier's bespoke values
format should be replaced by Terraform's own. Today Atelier writes user values
as arguments inside `main.tf`'s `module {}` block and adds a separate
`atelier.local.yaml` for named presets. Both reinvent something Terraform
already provides:

- `.tfvars` is a standard, typed-by-Terraform, lintable, CI-native values file
  with native layering (`terraform.tfvars` → `*.auto.tfvars` → later
  `-var-file`), `TF_VAR_*` env support, and first-class tooling.
- The "sparse" property Atelier enforces with ADR-0007 machinery is inherent to
  `.tfvars`: an absent entry means "use the declared default".

The blocker is structural. The wrapper is a **root module that calls a child
module**, and `.tfvars` sets the **root** module's `variable` blocks — it cannot
set a child module's arguments directly. Adopting `.tfvars` therefore requires
generating a pass-through root interface.

Two further constraints bound the design:

- **`.tfvars` is constants-only.** Reference expressions (`module.a.x`,
  `data.y.z`) are not valid there, so wired values must stay in HCL.
- **`.tfvars` is root-scoped**, and `-var-file` is global. A multi-module
  wrapper would need namespaced root variables.

## Decision

Add an **opt-in pass-through mode** (`TFVarsMode`), selected with
`atelier module add <url> --tfvars` and detected on later opens by a marker in
`main.tf` (so it survives deleting `.atelier/`). In this mode:

1. **`variables.tf` is generated** by concatenating the module's verbatim
   `variable` blocks (`tfvars.Variable.Raw`). Re-emitting the raw blocks
   preserves type constraints, `optional()` defaults, `validation`,
   `sensitive`, and `nullable` exactly. It is regenerated on every save so it
   tracks the module's input API.
2. **`main.tf` is generated** as a forwarding interface plus preserved
   meta-arguments: every declared variable becomes `name = var.name`, except a
   variable carrying a preserved wired expression, which is re-emitted verbatim
   and therefore overrides the forward. `count`, `for_each`, `providers`, and
   similar are preserved from `UnknownAttrs`.
3. **Values live in `terraform.tfvars`**, written sparsely by the existing
   ADR-0007 `ShouldEmit`/`SparseValue` rule, read back with a small HCL parser.
   Expressions are excluded because `.tfvars` cannot hold them.
4. **Presets are unchanged in this ADR.** `atelier.local.yaml`, the `F`/`S`
   keys, and the manifest package keep working; applied values simply land in
   `terraform.tfvars` instead of `main.tf`. (Superseded by
   [ADR-0032](0032-upstream-tfvars-discovery.md), which retires the YAML in
   favor of `.tfvars`; staged.)
5. **Scope is a single module.** The additive `module add` path refuses
   `--tfvars` against an existing `main.tf`, and `atelier tidy` refuses to run
   on a pass-through wrapper (there are no defaulted module arguments to prune).
6. **`--var-file <path>` seeds values** from an existing Terraform variable
   file of any name, repeatable with later files winning. It is the
   `terraform -var-file` analogue and complements `--preset`, so an existing
   values file can onboard without renaming to `terraform.tfvars`.

The classic shape is untouched: default bootstrap, reads, writes, tests, and
docs keep their current behavior.

## Alternatives considered

- **Make pass-through + `.tfvars` the only shape.** Rejected for now: it
  rewrites every existing wrapper, splits the config between `.tfvars`
  (constants) and `main.tf` (expressions), forces a multi-module namespacing
  scheme, and obsoletes ADR-0007/ADR-0022 in one step. The opt-in prototype
  gathers UX evidence first.
- **Keep `atelier.local.yaml` as the values sink and add a `.tfvars` exporter.**
  Rejected: exporting a second copy of the values creates drift without giving
  the standard-tooling benefits.
- **Per-module `.tfvars` files for multi-module wrappers.** Not possible with
  Terraform's root-scoped `-var-file`; the eventual multi-module design needs
  namespaced root variables instead.

## Consequences

- Users can try the standard-Terraform values workflow (`terraform plan`,
  `-var-file`, `*.auto.tfvars`, `TF_VAR_*`, linters) on an Atelier-authored
  wrapper without committing to it.
- `variables.tf` and `main.tf` become generated files; hand edits to them are
  overwritten on save (unlike the classic shape, which preserves them). This is
  the main behavioral difference to document.
- The config is split: constants in `terraform.tfvars`, wired expressions in
  `main.tf`. Wired variables are preserved but no longer have a single sink.
- A required variable with no default is forwarded as `var.x`; if unset,
  Terraform reports the missing root variable clearly, rather than the module
  being planned with an omitted argument.
- Multi-module wrappers, preset-as-`.tfvars`-file discovery, and retiring the
  classic shape are explicitly deferred and must be settled before this becomes
  the default.
