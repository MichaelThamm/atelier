# ADR-0059: Gallery candidate discovery is an on-demand tool

## Status

Accepted.

## Context

The bundled gallery is a curated set of quick starts, not a coverage matrix: an
entry is a standing commitment — a pinned ref, a preset to refresh, a `requires`
list to keep true — gated by `gallery lint` and `gallery check`
([ADR-0039](0039-composed-gallery-presets.md)). It can still lag the org, and
nothing notices a product module that was never added.

The scheduled workflow that keeps pins current ([ADR-0040](0040-automated-gallery-refresh.md))
is deliberately mechanical: it rewrites only `ref` and validates before opening a
reviewed PR. Deciding that a module belongs in the gallery, and writing its
subdir, preset, and `requires`, is a human judgement, so the scheduled-PR shape
does not fit discovery.

## Decision

Discovery is an on-demand tool, `tools/gallerycandidates` (`just gallery-candidates`),
not a scheduled workflow. It queries GitHub code search for `variables.tf` files
under a `terraform/` path, keeps the product/solution module roots the gallery's
own entries use, subtracts the repo/subdir pairs already covered, and prints the
rest for a maintainer to triage. It never edits the manifest and runs on no
schedule.

Code search is the source because it is cheap and needs no clone: it already
excludes archived repositories, and it reports file paths, so a hit's directory
is a candidate root. Child modules and test/example trees are filtered out; a
bare `terraform/` root or a differently named module directory is reachable with
`-all`, at the cost of noise from single-charm operator test modules.

## Alternatives considered

- **A scheduled workflow that opens a PR or issue of candidates.** Rejected: the
  output is a triage list, not a reviewable diff. The tool cannot write a preset
  or `requires`, so a PR would be an unmergeable stub; and the org adds product
  modules far more slowly than the fortnightly pin bump, so most runs would be
  empty.
- **Clone every org repository and reuse `internal/candidate.Discover`.**
  Rejected: the org holds thousands of repositories, so cloning is far heavier
  than a code search, and the heuristic still cannot tell a product module from
  an operator's test module without inspecting content.
- **Track candidates in the manifest or a committed list.** Rejected: it
  duplicates the gallery and rots; the source of truth is the org.

## Consequences

- A maintainer can find unadded product modules in one command, without a new CI
  surface or a recurring PR to triage.
- The tool is a prompt, not a gate: it does not run in `just check`, and a stale
  or noisy result costs nothing.
- The product/solution convention is a heuristic, so a module that does not
  follow it is missed unless `-all` is used, and archived or renamed repositories
  are not suggested.
