# ADR-0012: Validation via `terraform validate`

## Status

Accepted

## Context

Terraform variables can declare `validation {}` blocks that constrain
acceptable values. The condition is an HCL expression; the `error_message`
is shown when the condition fails. Example from COS Lite:

```hcl
variable "external_certificates_offer_url" {
  type    = string
  default = null

  validation {
    condition = (
      (var.external_certificates_offer_url == null && var.external_ca_cert_offer_url == null) ||
      (var.external_certificates_offer_url != null && var.external_ca_cert_offer_url != null)
    )
    error_message = "external_certificates_offer_url and external_ca_cert_offer_url must be supplied together (either both set or both null)."
  }
}
```

This is a **cross-field validation**: it references two different variables.
To evaluate it correctly, Atelier needs HCL expression evaluation that
matches Terraform's semantics exactly.

Three approaches:

- **(a) No live validation.** Show the `error_message` text as informational
  help next to the variable; let the rule fire at plan time.
- **(b) Debounced `terraform validate`.** Run Terraform's own `validate`
  subcommand after the user stops editing for some debounce window; surface
  errors in the status pane.
- **(c) Embed an HCL expression evaluator** that re-implements Terraform's
  validation semantics inside Atelier.

## Decision

**Option (b): debounced `terraform validate`** with a 500ms idle window.

- Atelier writes the wrapper on every edit (auto-save).
- 500ms after the last edit, Atelier runs `terraform validate` against the
  wrapper directory.
- Results are surfaced in the status pane:
  - `✓ Valid` when no errors.
  - `N errors` with the first error's `error_message`; subsequent errors
    accessible via a status-pane expansion.
- Validation does not block editing. The user can continue typing while
  validate is in-flight; a new edit cancels the pending debounce and
  restarts the window.

## Alternatives considered

### No live validation (a)

Possible. The user would only see validation errors at plan time. Rejected
because:

- Validation rules often catch user mistakes that are immediately fixable
  (typo in a string, wrong type, out-of-range number). Surfacing them
  500ms after the edit, in the same status pane where errors are visible,
  is materially more helpful than waiting until plan.
- The cost of (b) is low: `terraform validate` is fast (sub-second on
  most modules), runs entirely locally without provider RPCs, and is
  already a Terraform feature Atelier doesn't need to re-implement.

### Embedded HCL evaluator (c)

Considered. There are partial HCL evaluators in Go (`hcl/v2/hclsyntax` can
evaluate expressions given an evaluation context). Rejected because:

- Matching Terraform's exact validation semantics is hard. Terraform's
  validation may use functions like `regex`, `cidrhost`, `lookup`, etc.,
  and edge-case behaviour is defined by Terraform's source code, not the
  HCL spec.
- Drift between Atelier's embedded evaluator and Terraform's actual
  behaviour would surface as "Atelier says valid, plan says invalid" or
  vice versa. Worst-of-both-worlds.
- `terraform validate` is the authoritative source of truth. Using it
  means we never drift.

## Consequences

- The status pane is a multi-purpose surface: it shows validation results,
  plan errors, and general session info (module info, key hints). Unified
  treatment of "Terraform telling you something is wrong" is consistent
  with the error-handling design from SPEC §13.4.
- Cost: each `terraform validate` invocation is sub-second on COS Lite-sized
  modules but not free. The 500ms debounce ensures we don't run validate
  for every keystroke. A run-in-progress is left to finish; new edits queue
  a new run after the existing one returns.
- Validation errors are non-blocking. The user can save invalid states (the
  wrapper is the file on disk; "invalid" doesn't mean "unsaved"). If the
  user tries to `terraform apply` an invalid wrapper, Terraform itself
  refuses.
- Module-level errors (syntax errors in the wrapper) are also caught by
  `terraform validate` and surface the same way.
- The debounce window is currently fixed at 500ms. A future config option
  (e.g., user can disable live validation or change the debounce) is
  possible but not part of v1.
