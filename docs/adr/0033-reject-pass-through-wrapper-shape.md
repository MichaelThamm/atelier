# ADR-0033: Reject the pass-through wrapper shape; keep the classic wrapper

## Status

Accepted

Supersedes [ADR-0031](0031-tfvars-passthrough-mode.md). Amends the
implementation scope of [ADR-0032](0032-upstream-tfvars-discovery.md): `.tfvars`
files are preset bundles only, not a wrapper shape.

## Context

[ADR-0031](0031-tfvars-passthrough-mode.md) proposed an **opt-in pass-through
wrapper shape** (`--tfvars`): a generated `variables.tf` mirroring the module's
inputs, a generated `main.tf` forwarding `name = var.name`, and user values in
`terraform.tfvars`. It was motivated by wanting standard Terraform value
tooling (`-var-file`, `*.auto.tfvars`, `TF_VAR_*`) to apply to the wrapper.

On review it does not earn its maintenance cost:

- **It duplicates the module's API.** `variables.tf` re-emits the module's
  variable blocks and must be regenerated as they change — a second source of
  truth that can drift.
- **It ruins the wrapper's readability.** `main.tf` stops being a concise
  statement of intent and becomes a mechanical wall of `var.*` forwards for
  every input, most of which the user never touched. That legibility is the
  core of the tool's value.
- **It adds a second read/write path.** Mode detection from a marker in
  `main.tf`, ref-switch mode propagation, a `tidy` refusal, a single-module
  constraint, and a parallel set of tests all exist only to support the second
  shape.
- **The preset goal never needed it.** [ADR-0032](0032-upstream-tfvars-discovery.md)
  introduced `.tfvars` **preset bundles** consumed by `--var-file` and the TUI
  `F`/`S`. Those apply to the classic wrapper's module arguments and work
  without any pass-through interface.

A product-repo example remains a Terraform-native file: it can be applied to
the **module** directly with `terraform -var-file`, independent of Atelier.
Users who want a root `variables.tf` interface can hand-write one; Atelier does
not need to generate and maintain it.

## Decision

Remove the pass-through shape entirely. Atelier has exactly one wrapper shape:
the classic sparse `main.tf` (ADR-0007).

Removed: `--tfvars`, `TFVarsMode`, generated `variables.tf`, the managed
`terraform.tfvars`, the `atelier:tfvars` marker and mode detection,
`FilterPassthroughAttrs`, ref-switch mode propagation, and the `tidy` /
existing-wrapper guards that existed for it.

`.tfvars` files remain, but only as **preset bundles**: personal bundles in a
walk-up `atelier.presets/` directory and product examples under
`<module>/examples/`, applied with `--var-file` / `--list-var-files` and the TUI
`F` picker, and authored with `S`.

## Alternatives considered

- **Keep it opt-in.** Rejected: it is maintenance without current, clear value;
  the diff and the moving parts are not worth it (see the scope discipline in
  `AGENTS.md`).
- **Make pass-through the default.** Rejected: the same costs, plus a breaking
  migration of every existing wrapper and a multi-module namespacing scheme.

## Consequences

- One wrapper shape, one read/write path, fewer guards and tests.
- Users who want standard Terraform value layering apply `.tfvars` to the
  module, or hand-write a root interface. This is a deliberate capability we
  choose not to own.
- [ADR-0031](0031-tfvars-passthrough-mode.md) is superseded.
  [ADR-0032](0032-upstream-tfvars-discovery.md)'s discovery and preset decision
  stands; only its references to the pass-through shape are withdrawn.
