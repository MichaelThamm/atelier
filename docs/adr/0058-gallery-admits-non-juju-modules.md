# ADR-0058: The gallery admits first-party, non-Juju modules

## Status

Accepted.

## Context

Every entry in the bundled gallery targets the Juju provider and almost all are
Canonical product modules. The provider-agnostic claim Atelier makes is therefore
invisible where it matters most: a reader browsing the gallery sees only Juju, and
could reasonably conclude Atelier is a Juju tool.

A gallery entry is a standing commitment, not a link: a pinned ref, the
required-input coverage lint ([ADR-0039](0039-composed-gallery-presets.md)), the
scaffold-and-validate check, and a scheduled bump PR. Admitting a module means
owning that upkeep.

The coverage lint defines a **required** input as a variable with no `default`,
and `requires` may name only such variables — any other name is reported as
`stale`. That rules out modules whose meaningful input is defaulted. The
`terraform-aws-modules` family names its clusters `default = ""`, so an entry for
one has no required inputs: its one-liner would omit the name, and naming the
name in `requires` fails the lint.

## Decision

The gallery may include a non-Juju module when it is maintained by the cloud
vendor or a comparable first party, and it declares at least one required
(no-default) input, so the published one-liner is complete.

Add two entries:

- `Azure/terraform-azurerm-aks` — Microsoft's Azure Verified Module for AKS.
  Required inputs: `resource_group_name`, `location`.
- `terraform-google-modules/terraform-google-kubernetes-engine` — Google's GKE
  module. Required inputs: `project_id`, `name`, `network`, `subnetwork`,
  `ip_range_pods`.

Both are vendor-maintained and declare required inputs, so they need no preset and
no change to the lint or the manifest model. They render like any other card, and
because they carry no Juju model there is no variant beneath them.

## Alternatives considered

- **An AWS module (`terraform-aws-modules/terraform-aws-eks`).** Rejected: it
  declares no required input, so `atelier apply eks` would omit the cluster name,
  and listing that name under `requires` fails the coverage lint's `stale` check.
  Admitting it means redefining what `requires` may name — a change to the one
  gate that keeps the gallery trustworthy, not an example. Deferred as its own
  decision.
- **A community module with no vendor owner.** Rejected: the bump PR would track
  an unowned repository's cadence, which the gallery cannot afford.
- **A hand-written site example rather than an entry.** Rejected: that is exactly
  the drift [ADR-0037](0037-gallery-pages-site.md) avoids, and the quick start
  would not be reachable as `atelier apply <name>`.

## Consequences

- The gallery shows the provider-agnostic claim with two concrete, runnable
  examples; both are cheap to validate in CI (`terraform validate` needs no
  credentials).
- The maintenance surface grows by two vendor cadences. The scheduled bump still
  opens a reviewed PR, and a broken pin still fails `gallery lint`/`gallery
  check`.
- The admission criterion — vendor-maintained and declares a required input — is
  stated once, so the next candidate is judged by it rather than re-litigated.
