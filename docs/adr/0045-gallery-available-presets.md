# ADR-0045: A gallery entry offers presets as well as composing them

## Status

Accepted — amends [ADR-0039](0039-composed-gallery-presets.md).

## Context

ADR-0039 made `presets` a list an entry composes as its curated default
scenario, and `validate` rejects an embedded preset no entry names, on the
grounds that an unreferenced preset would never be exercised by the gallery
check and could rot unnoticed. Both follow from the premise that every preset an
entry ships is one it applies.

That premise does not hold for a module whose defaults do not deploy.

`terraform/cos` defaults every component to three units, and validates that
Grafana above one unit requires `postgresql_offer_url` — an input a quick start
has no value for. So `cos` composed `cos-single-unit`, a preset that put
everything at one unit. It applied, and it was the wrong tool for the job: the
entry was relying on a Grafana line that was incidental to a preset about
scaling, so a future topology preset would silently re-break deployability.

The distinction matters because the two kinds of preset are not the same. Scaling
is a preference, and the cluster decides it. Valid deployability is not a
preference; there is one value that works without a database the entry has no way
to supply. Only the second belongs in the default.

The same split already existed in the data. `cos-no-ingress` and
`cos-single-unit` override values the module declares, while the four other
gallery presets (`netbox-defaults`, `trino-defaults`,
`github-runner-defaults`, `saml-integrator-defaults`) exist to let the module
take its own attribute defaults. Relying on upstream was the alternative, and it
is not safe: `canonical/observability-stack` may drop or rename its presets, at
which point a gallery entry that depended on them loses a capability quietly.

## Decision

An entry separates the two. **`presets` are composed**; **`available_presets`
are offered**. An offered preset is surfaced in `atelier --list-var-files`, the
TUI preset picker, and the gallery card, and the user applies it with
`--var-file <name>`.

The orphan rule becomes: an embedded preset no entry claims by either list is an
error. Declaring an offered preset is therefore how a preset stays in the
gallery's coverage — `gallery-check` still binds it against the entry's module,
so a variable the module renames or drops is caught rather than silently
unexercised.

Resolution is unchanged. A preset is a bundle; an opt-in resolves by name
through the same precedence as any other `--var-file`, and a local or repo
bundle of the same name still shadows the gallery's. Composition remains a
manifest concern: `presets` is what `resolveModuleSource` prepends.

`cos` composes `cos-grafana-single-unit` — the one variable that makes it
plan — and offers `cos-single-unit` and `cos-no-ingress`. `cos-lite` and
`charmed-spark`, which deploy on their own defaults, compose nothing and offer
one preset each.

When a composed preset and an opt-in set the same variable, the opt-in wins by
composition order, so applying `cos-single-unit` over
`cos-grafana-single-unit` is a no-op on `grafana` and lowers the rest.

`atelier gallery lint` measures coverage against the composed presets only. An
opt-in is by definition not needed for the entry to plan, so counting it would
let a preset mask a genuinely uncovered required input.

## Alternatives considered

### Compose nothing and let the user pass `grafana = { units = 1 }`

One fewer preset, and the entry stays honest about deploying the module's own
defaults. Rejected because the default would then not plan at all, and the
one-liner the gallery exists to publish would fail at apply. A known-bad default
is worse than a minimal good one.

### Compose `cos-single-unit` and drop the separate Grafana preset

Fewer files, and the wrapper it writes is a smaller deployment. Rejected for the
conflation this ADR exists to fix: `cos` would keep depending on an incidental
line inside a scaling preset, so the reason the entry deploys stays invisible to
whoever reads the manifest.

### Let `available_presets` satisfy required-input coverage

An offered preset that happens to cover a required variable would then be enough
to pass the lint. Rejected because it makes an opt-in load-bearing without
saying so: the entry would appear to deploy on its own while requiring a flag
the user has to know about.

### Keep one list, and mark each preset composed or not in place

Fewer manifest fields, and no second list to keep in sync. Rejected because the
composed set is what the lint and `resolveModuleSource` read, so the flag would
have to be interpreted at each use rather than stated once — and an entry's
default scenario would no longer be readable off the manifest.

### Rely on the module repo's own presets

`terraform/cos/presets/` already ships `no-ingress`, `single-unit`, and
`s3-seaweedfs`, and `ResolveVarFile` already searches it, so `--var-file
no-ingress` works with no gallery copy at all. Rejected as the gallery's default
dependency: a preset the entry's deployability or documented one-liner rests on
can be renamed or removed upstream without touching Atelier, and a user who
copied a published command gets a warning instead of a deployment.

## Consequences

- A gallery entry's default is the smallest configuration that deploys, and
  everything else is an explicit choice.
- A preset must be claimed by some entry to survive `validate`, so `presets/` has
  no unreferenced files; `available_presets` is the way to ship an opt-in.
- Adding a preset is one file plus one manifest entry, in either list, and the
  gallery check covers both.
- An opt-in is only as fresh as the entry that claims it: `gallery-bump` refreshes
  refs but does not re-read preset contents, so an opt-in still needs review when
  its module's schema changes ([ADR-0040](0040-automated-gallery-refresh.md)).
- The `presets` and `available_presets` fields are related by an invariant
  `validate` enforces: a preset appears in at most one of them per entry.