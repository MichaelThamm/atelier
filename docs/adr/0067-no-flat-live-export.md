# ADR-0067: A live deployment exports as a module wrapper, never flat configuration

## Status

Accepted.

## Context

Juju 4.0 removed `juju export-bundle`, and its upgrade guide names the Juju
Terraform provider as the replacement; Charmhub stopped accepting new bundles in
November 2024 and points at the provider too. Users therefore ask Atelier to
fill the hole with an `atelier export` (or `juju export`) that turns a running
model into Terraform.

That capability already exists upstream, and it is not Atelier's shape.
`terraform query --generate-config-out` emits flat `resource` blocks plus
`import` blocks straight from live objects, and the provider extends it one
list-resource at a time. Its output is deliberately literal — hardcoded UUIDs,
no cross-references between resources, provider defaults and implicit resources
included. The provider does not present it as finished: its own guidance routes
the cleanup (cross-referencing, pruning un-settable attributes and defaults,
dropping unmanageable resources) through a downloadable AI-agent skill, not
through code. HashiCorp likewise calls config generation experimental and "only
meant as a starting point", saying a precise result needs per-resource support
the provider does not have.

Atelier's durable artifact is the wrapper ([ADR-0001](0001-wrapper-as-durable-artifact.md)):
a `module {}` call, a pinned ref, and the user's values. `atelier import`
already reaches into live state, but only to reconstruct that wrapper's *state*
for a module the user **explicitly names** ([ADR-0027](0027-atelier-import.md),
[ADR-0028](0028-provider-specific-import-ids.md)). It deliberately does not
infer topology ("Topology inference is deliberately not attempted") and does not
generate flat configuration; `terraform query -generate-config-out` was rejected
there as a competing, literal-valued duplicate.

Requests recur to reuse import's discovery — which already runs `terraform
query` and writes `imports.tf` — to emit flat config "for reproducers". This ADR
draws the boundary so those requests have one answer.

## Decision

**Atelier does not generate flat Terraform configuration from a live
deployment.** Permanently out of scope:

1. **Flat config generation.** Emitting `resource` blocks, with or without
   `import` blocks, from live objects — the `terraform query
   -generate-config-out` surface. It belongs to the provider, and it moves with
   the provider's schema, which is not a surface Atelier can track or verify.
2. **Generated-config cleanup.** Cross-referencing resources, pruning provider
   defaults, and dropping implicit resources. This is provider-specific graph
   knowledge with no cheap oracle — verifying a result needs a second controller
   and an apply — and the provider itself declines to encode it.
3. **Module inference.** Deciding which module a live deployment "is", including
   any best-match of a model against the gallery. A deployed bundle leaves no
   marker in the model identifying it, products share charms, and a wrong
   module's plan can replace or destroy the deployment ([ADR-0028](0028-provider-specific-import-ids.md)).
   The module is always named by the user, as it already is for `import
   --source`.

Unchanged and in scope:

- `atelier import` reconstructs state for an explicitly named module, and
  `--dry-run` writes a reviewable `imports.tf` ([ADR-0027](0027-atelier-import.md)).
- The gallery is a catalogue to **choose** a module from, not an engine to infer
  one ([ADR-0039](0039-composed-gallery-presets.md), [ADR-0047](0047-import-gallery-name.md)).

The only admissible future direction for turning a live deployment into
configuration is a **reproducer that emits the wrapper artifact for an
explicitly chosen module** — the inverse of apply, verified by planning to zero,
reporting anything it cannot derive. This ADR fixes the shape such work must
take; it does not commit to building it.

## Alternatives considered

- **A flat `atelier export`.** Rejected. It duplicates a surface the provider is
  actively expanding, needs perpetual per-provider schema and reference-graph
  knowledge, and has no verifiable gate; the provider's own answer to the hard
  part is a prompt, not code.
- **Reuse import's discovery to emit `resource` blocks.** Rejected. Import's
  discovery is provider-agnostic precisely because it only *lists* live objects
  to build import IDs; turning those into configuration needs the provider
  knowledge [ADR-0028](0028-provider-specific-import-ids.md) keeps behind the
  `Provider` interface, plus the cross-referencing [ADR-0027](0027-atelier-import.md)
  rejected.
- **Best-match a model against gallery products.** Rejected. There is no oracle
  for identity, the match is ambiguous across charms shared by many products,
  and a mismatched configuration is destructive rather than merely wrong.
- **A wrapper reproducer that infers variable values.** Deferred, not rejected:
  identity is derivable and already derived ([ADR-0028](0028-provider-specific-import-ids.md)),
  but topology and fidelity are not generic ("Topology inference is deliberately
  not attempted"; dataflow inference dead-ends at `locals`). Any such work names
  its module explicitly.

## Consequences

- `juju export` and "export my model as flat Terraform" requests have a
  documented answer: the provider's `terraform query` and its cleanup path.
  Atelier points there rather than reimplementing it, the pattern
  [ADR-0016](0016-scope-boundaries-no-orchestration.md) sets for adjacent tools.
- The provider-agnostic claim keeps its single bounded, import-scoped exception
  ([ADR-0028](0028-provider-specific-import-ids.md)); `juju` stays an example
  provider, not a product surface.
- A reproducer remains possible without contradicting this ADR, but only as a
  wrapper for a named module whose result is verified by a plan.
- The gallery stays a catalogue of modules to choose, never an inference input.
