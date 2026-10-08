# ADR-0055: A gallery-sourced import names its own revision

## Status

Accepted — amends [ADR-0047](0047-import-gallery-name.md), which made a gallery
entry's pinned revision the default a `--source` import silently used.

## Context

ADR-0047 gave `atelier import` the gallery: `--source cos-lite` expanded to the
entry's module, subdirectory and **pinned SHA**, and imported against that SHA.
Its rationale was that `gallery-check` validates the module at that revision,
which is a better default than whatever branch a hand-written URL tracks.

That is a claim about the module, not about the deployment. Import matches live
resources to the addresses the module declares **at a given revision**, so the
revision is part of the answer, and the user is the only one who knows it.
Deployments are commonly pinned to a branch or track (`--ref feat/service-mesh`,
`--ref track/3.0`), not the gallery's SHA. Importing such a deployment against
the pin plans against a revision it was never deployed from: addresses can
differ, and the report — resources "created", resources "unmatched" — becomes
noise, or a plan that reads like drift. The failure is quiet, which is the worst
property a safety signal can have.

Separately, `atelier import cos-lite` reaches for `apply`'s grammar, where the
positional is the source. Import's positional is the provider, so the name fell
through to the provider resolver and failed as `cos-lite/cos-lite`, which does
not say what the user meant.

## Decision

### 1. A gallery-sourced import requires an explicit `--ref`

When the source is a gallery entry and `--ref` is not given, the run is refused
before anything is cloned or planned. The message names the entry and its pin,
and shows the command to re-run, so a user who deployed the pin can pass it in
one copy. The entry still supplies the module and subdirectory; it never
supplies the revision.

`--list` and `--list-var-files` are exempt: no matching happens, so there is no
revision to get right.

### 2. The positional stays the provider, with a guard

`--source` remains the only way to name the module. The positional is the
`PROVIDER`, which is what import's second mode needs: importing into an existing
Terraform root has no clone, and still has to say which provider to query.

A positional that names a module source — a gallery entry, a URL, or a local
path — is refused with a pointer to `--source` (and, for a gallery entry, to
`--ref`), rather than failing later as an unrecognised provider.

## Alternatives considered

- **Make the positional the source, as `apply` does.** Rejected: import has a
  no-source mode, so its positional is the provider; overloading it against a
  gallery lookup introduces a heuristic, cannot name a provider and a source
  together, and leaves two ways to spell the source. The guard gives the
  discoverability without the second grammar.
- **Remove `--source` and take the source only positionally.** Rejected: it is a
  breaking change to an accepted ADR, the integration test, SPEC and the docs,
  and the no-source mode still needs the positional for the provider — so the
  overload it was meant to avoid remains.
- **Keep the pin as the default and warn.** Rejected: a warning is exactly the
  quiet signal this ADR is about, and the values are not approximately right —
  matching against the wrong revision produces a wrong plan.
- **Refuse a gallery-sourced import whose entry composes presets.** Still
  rejected, as in ADR-0047: the presets are not applied, so the entry is usable.

## Consequences

- ADR-0047's "the entry's pinned SHA replaces the branch" no longer holds for
  import. The pin is offered in the refusal message, never assumed.
- `atelier import juju --source cos-lite --ref track/3.0` is the gallery form.
  A run that omits `--ref` is refused; a run that deploys the entry's own pin
  must state it. That is the deliberate cost: the gallery cannot know what was
  deployed, and guessing is the failure this removes.
- `atelier import cos-lite` no longer fails as an unknown provider; it says to
  use `--source` and to name the revision.
- The provider argument is unchanged: required to scaffold a bare root, derived
  from the wrapper otherwise.
