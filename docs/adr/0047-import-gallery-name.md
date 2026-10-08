# ADR-0047: An import takes a gallery entry's module but none of its presets

## Status

Accepted — amends [ADR-0039](0039-composed-gallery-presets.md) and
[ADR-0035](0035-bundled-module-gallery.md) (its per-verb list of the commands
that accept an entry name). Its pinned-revision default is amended by
[ADR-0055](0055-gallery-import-names-its-revision.md).

## Context

ADR-0035 enumerated the commands that accept a gallery entry **name** in place
of a URL: `atelier add` and `atelier apply`. `atelier import` was not among
them, and the consequence was not a gap in convenience but a confusing failure —
`--source cos-lite` was passed to `git ls-remote` as if it were a repository,
because import never ran the name through the gallery.

A name is the wrong thing to reach for when the deployment is already running,
which is the case import exists for. Import matches live resources to the
addresses the module declares at a given revision, so the module, its
subdirectory and its ref are exactly what the user must get right. A gallery
entry supplies all three, pinned to a SHA that `gallery-check` has validated
against the module — a better default than whatever branch a hand-written URL
tracks.

The one thing an entry carries that import must not use is its composed
presets. ADR-0039 gave an entry `presets` as **its curated default scenario**,
and [ADR-0045](0045-gallery-available-presets.md) sharpened that: a composed
preset is what makes a fresh deployment deploy, not a preference the operator
picked. `cos` composes `cos-grafana-single-unit` because `terraform/cos`
defaults every component to three units and validates that Grafana above one
unit requires a `postgresql_offer_url` a quick start has no value for.

Importing a real three-unit Grafana deployment through the entry name would
therefore hand the matcher a configuration with one. That does not fail
loudly — it produces a plan that wants to change and create resources the
deployment already has, which is precisely the signal the import report tells
the user to read as "your variables do not describe the live deployment".

The asymmetry is the point. `add` and `apply` create something that does not
exist, so the gallery's opinion about what good looks like is the right default.
Import adopts something that already exists, so every value in the
configuration is a claim about reality that only the user can make.

## Decision

`atelier import --source` accepts a gallery entry name and resolves it exactly
as `add` and `apply` do — module, subdirectory, `--as` block name, and pinned
ref, with an explicit `--module` or `--ref` still winning. **It composes none
of the entry's presets.** The skipped presets are named on stderr, next to the
pointer to `--var`/`--var-file`, so nothing is silently dropped.

The same entry's `available_presets` remain reachable by name through
`--var-file`, which is the user's explicit choice rather than the manifest's
default.

Two related target rules come with it, both of which `--source` already
depended on:

- **`--dir` creates the directory it names**, following
  [ADR-0044](0044-dir-names-the-wrapper.md): an existing `main.tf` is adopted, a
  missing directory is created, and one already holding other files is refused.
  Without `--source` nothing is scaffolded, so the directory must already be an
  initialised Terraform root. `--dir` also selects which walk-up
  `atelier.presets/` bundles resolve, which is a second reason it is not merely
  a convenience.
- **A `--source`/`--module`/`--ref` that contradicts the wrapper is refused.**
  Importing with `--source` into a directory that already holds a wrapper
  re-hydrates that wrapper and reads its module from `main.tf`; the flags
  described an address it already had. A mismatch used to be dropped in silence,
  so a `--ref` naming one revision would import against the pinned one.
  Components the command omits are not a contradiction — they leave the
  wrapper's own subdirectory and ref in place.

## Alternatives considered

- **Leave `import` alone.** Rejected: the raw `git ls-remote` failure for a
  name that `atelier add` accepts is a defect on its own, independent of
  whether the name is convenient here.
- **Accept the name and compose the presets too, for symmetry with `add`.**
  Rejected: it feeds the matcher a scenario the user did not deploy. The
  resulting plan is not obviously wrong, which is the worst property a safety
  signal can have.
- **Accept the name but warn about skipped presets.** Rejected: the values are
  not approximately right, and there is no plan in which applying them is
  correct. Silence plus a note in the docs would leave the default in place.
- **Have `import` take the entry's `requires` as `--query-var` placeholders.**
  Rejected: those are deployment-specific inputs
  (ADR-0035, *Deployment-specific inputs are metadata*), and import already
  requires the user to name the one that matters — the model UUID — because no
  manifest can know it.
- **Refuse a gallery name whose entry composes presets.** Rejected: it makes the
  shorthand unavailable for exactly the entries with the most deployment
  history, in exchange for a rule that is enforced simply by not applying them.
- **Add `--as` to `import` so an entry's block name can be honoured.** Deferred:
  `bootstrap.InitOptions` has no block-name field, and the block label is
  cosmetic for a single-module wrapper. Worth doing only if a gallery entry
  needs a block name that import cannot derive from the candidate directory.

## Consequences

- `atelier import juju --source cos-lite --query-var model_uuid=…` is a
  complete command; the entry's pinned SHA replaces the branch a hand-written
  URL would track.
- A user importing a deployment that diverges from the entry's scenario
  supplies `--var` values, which is the correct shape for a claim about
  reality.
- `import --list-var-files` with a gallery name lists that entry's bundled
  presets alongside the module repo's, so the names a user can pass are visible.
- The `--dir` rule and the contradiction check make `import` consistent with
  `add`/`apply` on both target handling and source resolution, so the three
  verbs no longer differ in ways the user has to know.