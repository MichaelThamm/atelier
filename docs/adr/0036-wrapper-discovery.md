# ADR-0036: Read-only wrapper discovery (`atelier wrappers`)

## Status

Accepted

## Context

`atelier module add` and `atelier module apply` create a wrapper directory
named after the module when the current directory is not already a wrapper
(ADR-0034, SPEC §6.2). A common layout is therefore a scratch parent holding
sibling wrappers — for example `tf-testing/{cos,cos-lite}` — and the walk-up
`atelier.presets/` discovery already treats such a parent as meaningful
(ADR-0031).

`atelier module list` lists the modules *in the current wrapper* (ADR-0018).
Run from the parent it finds no `main.tf`, and because
`wrapper.ReadModuleBlocks` returns an empty slice for a missing file, it
printed "No modules found in this wrapper." That is misleading: there is no
wrapper there at all. Users want to see which wrappers exist under the parent
without `cd`-ing into each one.

## Decision

Add `atelier wrappers [PATH]` (default: the current directory). It lists each
immediate child directory holding a `main.tf` or an `.atelier/`, with the
module block names that wrapper declares. Hidden directories are skipped and
results are sorted by name.

It is deliberately **read-only and one level deep**: it does not open, plan,
apply, or otherwise address a child, and it does not recurse. `atelier module
list` stays scoped to the current wrapper; its no-wrapper case now says so and
points at `atelier wrappers`.

The boundary is the point. ADR-0016 keeps Atelier single-root: a *view* of
sibling wrappers does not orchestrate them, but an *addressed* model — a parent
that "points at" wrappers, e.g. `atelier --module cos` — is the first step
toward fan-out and is declined. A separate noun also avoids overloading the
existing vocabulary: modules belong to a wrapper, wrappers belong to a
directory, and `--module` already means "a subdirectory within the source
repo".

## Alternatives considered

- **Make `atelier module list` search children by default.** Rejected: it
  silently changes an existing, scripting-oriented command's semantics,
  produces ambiguous output when several children match, and conflates two
  nouns ("modules of a wrapper" vs "wrappers in a directory").
- **A parent/workspace manifest or `--module <child>` addressing.** Rejected:
  it needs new durable state or a new configuration language (against
  ADR-0001), and it is the orchestration wedge ADR-0016 exists to stop.
- **Recursive discovery by default.** Rejected: "one level" is the scratch-dir
  pattern, and any deeper default makes the boundary arbitrary and noisy in
  large trees. Can be added later behind an explicit flag if a concrete need
  appears.
- **Do nothing.** Rejected: the misleading message and the discovery gap are
  both real.

## Consequences

- A small, read-only surface that stays within ADR-0016.
- `atelier module list` becomes honest when the current directory is not a
  wrapper.
- Future work that wants cross-wrapper *operations* must write a superseding
  ADR and argue the value, rather than growing this view into a controller.
