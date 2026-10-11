# ADR-0060: Read the whole module contract — outputs are first-class

## Status

Accepted — ships ahead of the v1 work as the v0 module-contract reader.

## Context

Atelier parses a module's `variable` blocks and nothing else
([`internal/tfvars`](../../internal/tfvars/parse.go)). `output` blocks are
neither read nor written, which the ROADMAP names as the missing
provider-agnostic primitive.

Outputs are the other half of a module's API. A consumer wires a provider's
output into a consumer's input ([ADR-0017](0017-inter-module-wiring.md)), and a
module's outputs are what make it composable at all; without reading them,
Atelier can only guess at what a module produces.

A Terraform `output` block supports an optional `type` constraint, plus
`value`, `description`, `sensitive`, `deprecated`, `ephemeral`, `depends_on`,
and `precondition`. Most modules omit `type` — the type is then inferred by
Terraform from `value`. Atelier cannot and should not evaluate arbitrary
expressions; running `terraform validate` is the oracle
([ADR-0012](0012-validation-via-terraform-validate.md)). `terraform plan -json`
does expose a serialized output type in `planned_values.outputs`, but only
after a successful plan.

## Decision

The **module contract** is `internal/tfvars`'s responsibility, extended to read
both halves of the API from one directory walk:

- Add an `Output` type and `LoadModule(dir) (Module, error)` returning
  `Module{Variables []Variable; Outputs []Output}`. `LoadDir` stays and becomes
  a thin wrapper over `LoadModule`, so existing callers are unchanged and
  migrate opportunistically.
- `Output` carries `Name`, `Type`, `Description`, `Sensitive`, `Deprecated`,
  `Ephemeral`, `ValueRaw` (the source of `value`), and `DeclRange`. `Type` is
  `*tftypes.Type`; it is `KindAny` when the block declares no type or the
  constraint cannot be parsed.
- The declared `type`, when present, is parsed with `tftypes.ParseTypeExpr` —
  the same path variables use. No expression evaluation is added.
- Reading is **static-first**: the reader reads the cloned module's `.tf`
  files, so outputs are available offline and before the first plan. Plan
  enrichment (the serialized type from `planned_values.outputs`) is additive
  and does not change this contract.
- The package doc is broadened from "parses `variable` blocks" to "parses a
  module's declared contract: variables and outputs."

## Alternatives considered

- **A separate `internal/tfoutputs` package.** Rejected: it would walk the same
  directory and parse the same files a second time — the duplicated-parser
  shape the root reuse audit warns about.
- **Rename `internal/tfvars` to `internal/tfmodule` now.** Rejected for this
  change: it touches dozens of import sites and belongs in a separately scoped
  refactor that moves tests with the code, not in a feature.
- **Require a declared `type` before an output is usable.** Rejected: most
  modules omit it, so wiring would be mostly unavailable during authoring.
- **Best-effort static inference of the output type from `value`.** Rejected
  for v1: partial and occasionally wrong, and it still needs an "unknown" state,
  so it buys accuracy at the cost of a small evaluator.

## Consequences

- Outputs become discoverable offline, before any plan and without
  credentials.
- Wire suggestions ([ADR-0017](0017-inter-module-wiring.md)) can use a
  declared type where the module states one and offer an unknown-typed output
  ungated, with `terraform validate` as the oracle.
- The reader stays free of expression evaluation; Terraform remains the only
  evaluator.
- The package name is narrower than its contents. The package doc and the
  reuse table carry that, and a `tfmodule` rename is a later, separately scoped
  refactor.
