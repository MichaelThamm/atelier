# ADR-0033: Provider-specific matching lives behind the Provider interface

## Status

Accepted. Amends [ADR-0027](0027-atelier-import.md) (its import pipeline step 5,
"Match by identity, then name, then attributes") and [ADR-0028](0028-provider-specific-import-ids.md)
(the provider extension points).

## Context

[ADR-0027](0027-atelier-import.md) and `internal/importer/AGENTS.md` state that
the importer core is **provider-agnostic**: importable resource types and their
arguments come from the provider's own schema, and provider-specific behaviour
lives behind the `Provider` interface so "adding a provider must not touch core
files".

Matching was the one place that did not hold. `internal/importer/match.go`
resolved planned resources to live objects in three phases:

1. exact provider-declared identity match;
2. display-name match;
3. **attribute-based match for Juju integrations and offers** — hardcoded in
   the core: `juju_integration` keyed by `(app, endpoint)` pairs parsed from a
   five-part identity string, and `juju_offer` keyed by an offer URL, matched
   from planned `application` / `application_name` / `name` attributes.

Phase 3 (roughly ninety lines plus its helpers) named Juju resource types,
encoded Juju's identity formats, and was tested in package `importer` — so the
"provider-agnostic core" claim was false precisely where the logic was most
provider-specific.

## Decision

Move phase 3 behind the `Provider` interface as an optional fallback matcher.

- The core defines `importer.FallbackMatcher`: given a resource type, the
  planned attributes, and the live set, it returns the indexes of matching
  unused live objects.
- `importer.Match` runs the two generic phases first and calls the fallback
  only when they are inconclusive. With no fallback (a provider-supplied `nil`),
  behaviour is exactly the two generic phases.
- `Provider` gains `MatchFallback() importer.FallbackMatcher`; the Juju
  implementation (`internal/importer/providers/juju/match.go`) owns
  `juju_integration` and `juju_offer` matching and the identity parsing it
  needs. Its tests move with it.
- The CLI wires `opts.MatchFallback` from the selected provider, alongside the
  existing `PreflightSteps`, `PlanChecks`, `PostImportSteps`, and
  `BuildImportID`.

The generic phases (identity, name) stay in the core: they are
provider-independent and must run for every provider.

## Alternatives considered

- **Leave phase 3 in the core.** Rejected: it contradicts the stated invariant,
  and every new provider with composite identities would add another special
  case to `match.go`.
- **Move all three phases behind the interface.** Rejected: identity and
  name matching are generic; only the composite-identity case needs provider
  knowledge. Keeping the generic phases in the core means a provider with no
  fallback still matches correctly without writing one.
- **Also move `hints.go`'s Juju error advice behind the interface.** Left for a
  separate change. Error classification is a different concern from matching,
  most of `hints.go` is generic Terraform advice, and folding both into one
  change would be larger and harder to review.

## Consequences

- `internal/importer/match.go` no longer names any Juju resource type; the
  provider-agnostic claim holds for matching.
- Adding a provider with composite identities is now a new `MatchFallback`
  implementation and a registry entry, not an edit to the core.
- Matching behaviour is unchanged: the fallback runs at the same point in the
  same order, on the same inputs, and the moved tests (offer-by-name,
  integration-by-composite-ID, distinct-endpoints) pass unchanged.
- ADR-0027 step 5 is now "match by identity, then name, then the provider's
  fallback". ADR-0028 is unaffected in substance.
