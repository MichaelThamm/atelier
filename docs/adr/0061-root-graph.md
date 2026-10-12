# ADR-0061: The wrapper is a root graph

## Status

Accepted.

## Context

A wrapper may hold several `module` blocks
([ADR-0015](0015-multi-module-grouping.md)) and is composed one module per
command ([ADR-0044](0044-dir-names-the-wrapper.md)). Wiring one module's output
into another's input is specified ([ADR-0017](0017-inter-module-wiring.md)) but
only half-realised, and there is no model of the root as a whole — so there is
no way to see the composition, nor to know what a ref bump does to the
interfaces already wired together.

Four things all need the same fact — *which argument of which module
references which output of which other module*: wire suggestions, a topology
view, plan annotation, and a cross-ref API diff.

[ADR-0016](0016-scope-boundaries-no-orchestration.md) keeps Atelier to a single
root and out of orchestration. A root graph models *within-root composition*;
it does not execute or order anything.

## Decision

Introduce `internal/rootgraph`, a **read-only** domain package sibling to
`wrapper`.

- **Nodes** are the wrapper's `module` blocks.
- **Edges** are argument-level and direct: parse each block argument with
  `hclsyntax`, keep traversals of the form `module.<name>.<output>`, and record
  `(consumer block, argument, producer block, output)`. An expression with no
  module reference contributes no edge.
- Module meta-arguments (`depends_on`, `count`, `for_each`) are recorded under a
  **distinct edge kind**, so a dependency is visible in the topology and plan
  views but is never offered as a data-flow wire.
- `wrapper` gains exactly one exported reader that returns every module block
  with its arguments and resolved references, so `main.tf` keeps a single
  parser. `rootgraph` consumes that plus `tfvars.LoadModule`
  ([ADR-0060](0060-module-contract-outputs.md)); it never writes files — writes
  stay `wrapper.RenderMain` and `State.Write`.
- The graph is derived on demand and not persisted; `.atelier/` remains
  regenerable ([ADR-0001](0001-wrapper-as-durable-artifact.md)).
- Plan enrichment — the `configuration` references and output types from
  `terraform show -json` — validates and completes the model additively.

**v1 non-goals**, recorded as limits rather than silent gaps: `local`/`var`
indirection, `dynamic` blocks, and references through files Atelier does not
parse. A missed edge is "no edge," never an error.

## Alternatives considered

- **Module-level edges only** (`consumer → producer`, deduplicated). Rejected:
  it loses which argument depends on which output, so it cannot drive plan
  annotation or the API diff.
- **A full resource/data/provider expression graph.** Rejected: it
  re-implements a slice of Terraform's own dependency graph and falls outside
  the "configure a module's API" boundary.
- **Put the graph inside `wrapper`.** Rejected: it grows that package past
  reading/writing `main.tf` and applying the sparse rule.
- **A second `main.tf` parser inside `rootgraph`.** Rejected: `wrapper` already
  parses `main.tf`; the graph should consume a reader, not duplicate one.
- **Follow `locals` indirection.** Deferred: it is a small evaluator, and the
  plan-enrichment path is the place to catch what direct references miss.

## Consequences

- One model serves wire suggestions, the topology view, plan annotation, and
  the API diff.
- The graph is available offline and before the first plan. It is approximate
  where locals or `dynamic` are used, and this ADR records that as a known
  limit.
- Wiring still writes plain `module.<name>.<output>` HCL, so the wrapper stays
  standard Terraform and independently runnable.
- Atelier gains no orchestration: the graph describes one root and nothing
  executes it.
