# ADR-0062: Typed, producer-defined module interfaces are the integration contract

## Status

Proposed.

## Context

A wrapper composes several modules ([ADR-0015](0015-multi-module-grouping.md),
[ADR-0044](0044-dir-names-the-wrapper.md)), and wiring one module's output into
another's input is left to the user ([ADR-0017](0017-inter-module-wiring.md)).
Today that wiring is a pasted value: an operator reads the live Juju offers and
writes `--var 'cos_offers={dashboard="admin/cos.dashboards",...}'`.

The ecosystem is converging on a `cos_offers` object for this (Kafka and
Charmed Spark both define one), but each consumer invents its own shape — Spark
requires `{dashboard, logging, metrics}`, Kafka makes
`{dashboard, metrics, logging, tracing}` optional — and `metrics` conflates
distinct interfaces in opposite directions: `prometheus_scrape` (COS scrapes
the consumer) versus `prometheus_remote_write` / `otlp` (the consumer pushes to
COS). The interfaces themselves are already declared by the charms in
`charmcraft.yaml` (`grafana_dashboard`, `loki_push_api`, `prometheus_scrape`,
`prometheus_remote_write`, `otlp`, `tracing`, …), yet none of that reaches the
Terraform contract: producers expose raw `juju_offer` resource objects, and the
interface name survives only in prose.

[ADR-0017](0017-inter-module-wiring.md) specified type-compatible wire
suggestions, but only for scalar types and without a producer-published
contract to match against. [ADR-0060](0060-module-contract-outputs.md) makes
outputs readable, and [ADR-0061](0061-root-graph.md) models the root; what is
missing is the contract those two enable.

## Decision

A module's integration contract is a **typed, interface-keyed output the
producer publishes**. A consumer declares a **matching input** and conforms to
the producer's names.

### The contract

A producer exposes an output whose keys are the **Juju interface names** the
backing charms declare, and whose values are the offer URLs:

```hcl
output "cos_offers" {
  type = object({
    grafana_dashboard       = string
    karma_dashboard         = string
    loki_push_api           = string
    otlp                    = string
    prometheus_remote_write = string
    prometheus_scrape       = string
  })
  value = { grafana_dashboard = local.offers.grafana_dashboards.url, ... }
}
```

- **Producer-defined.** The producer owns the interface and its names; every
  product integrating with it implements the matching input.
- **Names come from the ecosystem, not from Atelier.** They are the interface
  names the charms declare, so no Atelier-specific manifest is read from
  upstream (holding [ADR-0031](0031-presets-as-tfvars-bundles.md)).
- **Interface, not category.** The key is the interface, so the two directions
  of "metrics" stay distinct and the notion is future-proof.

### Matching

Atelier matches **whole objects by name and type**: a consumer input named `X`
and a producer output named `X` match when the consumer's declared type is
structurally compatible with the producer's — every attribute the consumer
requires exists in the producer with a compatible type. A consumer that names
a subset is fine: Terraform drops the attributes it does not name, so a
producer may offer every interface while a consumer takes only those it uses.

### Writing the wire

Atelier **proposes, it does not silently wire** (extending
[ADR-0017](0017-inter-module-wiring.md)): a proposed wire appears in the
topology view and is reported by `atelier add`/`apply`; it is written on
accept, or non-interactively with an explicit flag. What it writes is plain
`module.<producer>.<output>` HCL, so the wrapper stays standard Terraform and
the change is visible and reversible.

### Verification

- **Producer gate.** `atelier contract lint --module <dir>` verifies a module
  publishes a well-formed, interface-keyed catalogue; the gallery gate calls
  the same check over its pinned entries.
- **Subset gate.** When a subset is wired, Atelier checks it against the
  consumer's own declared type: an interface the consumer declares as required
  (not `optional`) must be selected. It reads only that type and never guesses.

Both use only interface names and Terraform types; neither reads charm
metadata.

### Boundaries

- **Only adopting modules are matched.** A module that does not publish a typed
  catalogue is not matched; there is no heuristic fallback
  ([ADR-0017](0017-inter-module-wiring.md)'s "too magical" name match stays
  rejected) and no curated interface table in v1.
- **Atelier carries no provider vocabulary.** Matching reads the keys a producer
  publishes and their declared types; Atelier knows nothing of Juju, charms,
  offers, or collectors. The interface names in the examples are the vocabulary
  the *products* choose; a non-Juju producer uses whatever role names it
  publishes, and the mechanism is identical. Atelier must not grow a curated
  interface vocabulary — that is the provider-specific drift to avoid.
- **Version boundary.** Reading the module contract is v0
  ([ADR-0060](0060-module-contract-outputs.md)); the root graph
  ([ADR-0061](0061-root-graph.md)) is v1; this matching, writing, and
  verification is v1.
- **Capability, not a visual layer.** The deliverable is read → match → write →
  verify. A visual surface (a topology view) is at most an aid to choosing a
  subset, not the point of the version; it ships only if it measurably improves
  that choice over the CLI.
- **Annotations are wire-level.** Any later "explain the plan" feature
  attributes a change to the wrapper's modules, arguments, and wires — never to
  a charm role or a collector type.

## Alternatives considered

- **Structural type matching alone.** Rejected: `metrics` shows that names
  carry semantics a type cannot — two `object({string,string,string})` values
  can mean opposite things.
- **A curated interface table for non-adopters.** Deferred: it restates the
  contract by hand and can rot, the failure mode the ROADMAP warns about. It
  becomes checkable (interfaces are now names+types), so it is a viable later
  bridge, not a v1 mechanism.
- **Semantic verification against `charmcraft.yaml`.** Rejected: it drags Juju
  charm metadata into Atelier and re-introduces the provider-specific
  knowledge the scope boundaries keep out ([ADR-0016](0016-scope-boundaries-no-orchestration.md)).
- **Auto-wiring.** Rejected: a semantically wrong match (right names and types,
  wrong meaning) would be a silent infrastructure change; explicit consent is
  kept ([ADR-0017](0017-inter-module-wiring.md)).
- **Per-interface composition** (sourcing interfaces from several producers into
  one input). Deferred to a later version; whole-object matching is the
  smallest thing that wires the observability ecosystem.
- **A "missing producer" check** (warn when no module in the wrapper provides an
  interface a consumer names). Rejected: without a marker, an ordinary object
  input (a `model` object, say) is indistinguishable from an interface
  requirement, so it would cry wolf. The subset gate catches the failure that
  matters without guessing.

## Consequences

- Supersedes the scalar-only and auto-wiring slice of
  [ADR-0017](0017-inter-module-wiring.md); its suggestion UX is subsumed here.
- The root graph ([ADR-0061](0061-root-graph.md)) is the substrate: matching is
  a computation over its nodes' contracts.
- Producers must adopt the convention for their modules to be matchable; the
  cost falls first on the products that own the interfaces (COS, Cos Lite),
  which is why `atelier contract lint` is producer-facing.
- Atelier gains a producer-side contract, but still composes **one** root and
  executes nothing: no orchestration, no cross-root DAG ([ADR-0016](0016-scope-boundaries-no-orchestration.md)).
