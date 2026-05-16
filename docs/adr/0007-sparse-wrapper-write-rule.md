# ADR-0007: Sparse-plus-required wrapper-write rule

## Status

Accepted

## Context

When the user configures a module via the TUI, Atelier must decide *what to
write into the wrapper's `main.tf`*. Three options:

- **(a) Full object.** Always emit every variable with its current value
  (whether default or user-set). Verbose; tracks defaults at *write time*,
  not at *apply time*.
- **(b) Sparse — user-touched only.** Track per-variable "did the user touch
  this?" state separately from current value. Emit only user-touched
  variables.
- **(c) Sparse — differs-from-default only.** Compare current value against
  the variable's declared default. Emit only values that differ.

For object types with `optional(T, default)` fields, the same question
applies recursively to each field.

A separate question is whether **required** variables (no `default` in the
declaration) behave differently. Required variables *must* be emitted,
regardless of comparison-to-default semantics, because Terraform will fail to
plan without them.

## Decision

The **sparse-plus-required** rule:

- **Required variables** (no `default` declared): always emitted. The TUI
  marks unset required variables with `[!]`; the status pane flags missing
  required values until the user supplies them.
- **Optional variables** (with `default`): emitted only if the current value
  differs from the default. Applied recursively to `optional(T, default)`
  fields inside object variables.

This collapses "set-to-default" into "default": if the user explicitly sets a
field to its declared default value, Atelier writes nothing for that field.
Terraform itself cannot distinguish unset from set-to-default for `optional()`
fields, so the loss of user intent is theoretical.

## Alternatives considered

### Full object (a)

- **Pros:** explicit; every value is visible in the wrapper; no surprise on
  module-upstream default changes (the wrapper has its own value).
- **Cons:** verbose (COS Lite wrappers would be hundreds of lines for any
  module with object variables); silently pins the module's defaults at
  write time, so if the maintainer updates a default later, the wrapper
  continues to use the old value indefinitely.

The "pinning" property cuts both ways. In some workflows it's desirable
(reproducibility). For Atelier, the user's intent is captured better by
recording *deviations from default* and letting defaults float as the module
evolves; this matches the "configure visually then iterate" mental model.

### Sparse, user-touched only (b)

Requires a separate "touched" bit per variable, persisted somewhere. We'd
need to encode it either in the wrapper (as comments?) or in
`.atelier/session.json`. Either way, "touched-but-reverted-to-default" is a
distinguishable state from "never touched", which is meaningful in principle
but vanishingly rare in practice.

Rejected because (c) achieves the same wrapper output for the typical case
with a simpler internal model.

### No required-always exception

If we applied the sparse-differs-from-default rule uniformly, required
variables would never be emitted (they have no default to compare against;
the implicit semantics depend on the variable's type). This is wrong:
Terraform requires the value to be supplied. The required-always exception
is necessary.

## Consequences

- For a COS Lite wrapper at zero overrides, the wrapper body collapses to:
  ```hcl
  module "cos_lite" {
    source     = "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=<sha>"
    model_uuid = "..."
  }
  ```
  Only the required `model_uuid` and the `source` reference appear.

- When the user changes `alertmanager.units = 3` (and nothing else), the
  wrapper grows to:
  ```hcl
  module "cos_lite" {
    source     = "git::...?ref=<sha>"
    model_uuid = "..."
    alertmanager = {
      units = 3
    }
  }
  ```
  Other `alertmanager` fields (app_name, config, constraints, revision,
  storage_directives) are not emitted because they remain at default.

- **Upstream default changes become invisible by default.** If the
  maintainer updates `alertmanager.constraints` default from `arch=amd64` to
  `arch=arm64`, a user re-opening their session picks up the new default for
  any field they haven't overridden. The wrapper diff against defaults
  doesn't change.

  This is *correct* behaviour for the "configure-by-diff" mental model, but
  potentially surprising. To mitigate, Atelier surfaces a one-shot
  default-change summary on session open after a ref bump (see SPEC §5.4).
  The user sees: "Module ref resolved to a new commit; defaults that changed:
  …" with the field-level diffs.

- The TUI computes "differs from default" per-field at session open time
  using parsed `variables.tf` defaults. For nested objects, comparison is
  structural (an object is at-default if every field is at-default).

- Comments and surrounding HCL the user added to `main.tf` are preserved
  across re-saves (driven by [ADR-0005](0005-implementation-language-go.md)'s
  AST-preserving writer).
