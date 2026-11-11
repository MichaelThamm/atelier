# ADR-0067: Integration interface keys are the charm interface names

## Status

Accepted.

## Context

Products in the Juju Terraform ecosystem compose by publishing `juju_offer`
URLs that a consumer's module takes as input. A producer publishes a **catalog**
— a typed output whose attributes are the integration keys and whose values are
offer URLs — and a consumer declares a matching input, taking the subset it
needs. The one open question is the key vocabulary, because both sides must
agree on it or the wire does not type-check.

Two vocabularies compete:

- **Charm interface names** — the exact string each charm declares for a
  relation in its `charmcraft.yaml` (`interface:`): `grafana_dashboard`,
  `loki_push_api`, `prometheus_remote_write`, `prometheus_scrape`, `otlp`,
  `tracing`. This is the name Juju itself joins on.
- **Product categories** — a product's own colloquial grouping: `metrics`,
  `logs`, `traces`. Shorter, but lossy: `metrics` conflates `prometheus_scrape`
  (COS scrapes the consumer) with `prometheus_remote_write` and `otlp` (the
  consumer pushes to COS) — relations in opposite directions, satisfied by
  different charms; `logs` cannot say whether the consumer pushes to Loki
  directly (`loki_push_api`) or through an OTel collector (`otlp`); and the same
  category would be redefined per product, so two catalogs cannot interoperate
  without a curated mapping.

The concrete case that forced the question: a product exposes both OTLP and
Prometheus remote-write. A `metrics` key cannot express "remote-write but not
OTLP", so a consumer naming `metrics` cannot avoid pushing duplicate series into
COS, nor say which of the two it needs.

## Decision

**The catalog key is the Juju interface name, exactly as the backing charms
declare it in `charmcraft.yaml` — never a product-defined category.**

- The key names the *contract*, not the payload. `prometheus_remote_write` is
  the Prometheus remote-write relation, whoever implements it — Mimir in COS,
  Prometheus in Cos Lite. It does not mean "metrics".
- A product that carries the same signal over several transports exposes one
  key per interface (`prometheus_remote_write`, `otlp`, `prometheus_scrape`) and
  lets the consumer choose.
- A consumer names the exact interfaces it requires, in its input's type — an
  `object({ … })` for whole-or-none, or `map(string)` plus
  `contains(keys(…))` for a subset. Because it names transports, not signals, it
  selects one metrics path and does not double-count.
- The vocabulary is producer- and ecosystem-owned. The name is defined by the
  relation, not by Atelier and not by an individual module, so Atelier carries
  no provider vocabulary.
- The catalog's output name (`cos_offers`) is part of the contract, so
  interchangeable producers publish the same name and keys and a consumer works
  against either.

The full convention is specified in
[SPEC-interfaces.md](../SPEC-interfaces.md).

## Alternatives considered

- **Product category keys (`metrics`, `logs`, `traces`).** Rejected:
  direction-blind, transport-blind, and re-defined per product, so it needs a
  curated mapping that restates the contract and rots. It is also what makes
  duplicate signals unavoidable, because the category becomes "all of the
  above".
- **Shortened interface names (`remote_write` for `prometheus_remote_write`).**
  Rejected: a second vocabulary that must be mapped back to the real one, and
  ambiguous next to `otlp`. The verbosity is the precision.
- **An Atelier-curated interface vocabulary.** Rejected: it would put
  provider-specific knowledge in a provider-agnostic tool and diverge from the
  charms.
- **Read `charmcraft.yaml` to verify the names at lint time.** Rejected: it
  would make Atelier depend on Juju charm metadata; a producer asserts its own
  keys and the catalog shape is verified on its own.

## Consequences

- A consumer works against any producer that satisfies the same interfaces —
  Mimir or Prometheus for `prometheus_remote_write`, COS or Cos Lite for the
  shared set — which is the interoperability the convention exists to provide.
- Interface names are stable and greppable: they appear in `charmcraft.yaml`, in
  the relation spec, and now in the Terraform contract.
- A producer resolves the interface name for each offer it publishes; endpoint
  and offer names (`grafana-dashboards`, `grafana_dashboards`) stay out of the
  contract.
- Duplicate signals become an explicit consumer choice: it names the transports
  it uses, at most one per signal per producer.
- Dropping or renaming a key is a breaking change owned and announced by the
  producer; adding one is additive and consumers typed with a subset are
  unaffected.
- This is the vocabulary half of the typed-interface convention the products
  adopt; the shape and the matching are specified in
  [SPEC-interfaces.md](../SPEC-interfaces.md).
