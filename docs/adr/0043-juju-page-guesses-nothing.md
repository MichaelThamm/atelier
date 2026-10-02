# ADR-0043: The Juju page reads the environment, but guesses nothing

## Status

Accepted — supersedes [ADR-0041](0041-juju-opinionated-gallery-page.md)'s
credential and channel defaults. The page and
[ADR-0042](0042-juju-page-pins-and-own-model.md)'s pins stand.

## Context

ADR-0041 added `/juju/`, a page whose per-card variants are ready to run, and
gave them "conventional defaults": `$AWS_*` for the S3 trio and a charm channel
(`latest/stable` in the ADR, `dev/edge` in the code — the two already disagreed).

Both defaults are wrong, for different reasons.

The credential names imply a vendor Atelier does not assume. These modules talk
to any S3-compatible store — Ceph, MinIO, OpenStack — and the Juju provider is
orthogonal to the object store entirely. `AWS_ACCESS_KEY_ID` is the name the AWS
SDK happens to use; adopting it tells a reader that AWS is involved somewhere it
may not be.

The channel default is worse than vendor-specific: it is module know-how
presented as a platform fact. Loki, Mimir and Tempo each validate that the charm
track is `dev/`, so a page that emits `dev/edge` is correct for three entries
today and silently wrong for the next product added. Now that the gallery is
growing, every entry a product team onboards would need a channel exception in
the site generator — exactly the maintenance burden the gallery exists to avoid.
ADR-0042 made this concrete: the same assumption, one pin per entry, survived nine
entries and broke when the gallery grew to twenty-two.

The page also opened by claiming "every module in the gallery deploys through the
Juju provider". That is true today and false the moment a non-Juju entry lands.

## Decision

- Credentials read **`S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_ENDPOINT`** — names
  that describe the role rather than a vendor.
- **No charm-channel default.** A track is valid per module, not per Juju, so the
  input keeps its placeholder and the card names it as one the reader supplies.
  Where a channel genuinely belongs, the manifest carries it (`charmarr-plus`
  ships `2/stable`) and that value still wins.
- The **model pin** stays, in every shape ADR-0042 added. That is genuinely a Juju
  idiom and the reason the page exists.
- The page describes **gallery entries that deploy with Juju**, not every entry,
  so adding a non-Juju module does not falsify it. Entries that own their model
  keep their existing "no variant" note.
- The page states the conventions once and stays short: what the variant resolves,
  and which entries the override is a behaviour change for. That last split is
  given as **counts, not entry names** — at twenty-two entries the two groups
  hold fifteen and six names, and each card already states its own case in the
  variant's label, so naming them in the banner was redundant prose that stops
  being readable as the gallery grows.

## Alternatives considered

- **Keep the channel default per entry.** Rejected: it puts module track policy
  in the site generator, where a product team must be consulted for every entry.
- **Keep `AWS_*` as well-known aliases.** Rejected: two names for one value is
  ambiguity in a command a reader pastes. Pick the neutral one.
- **Document the per-module channel rules in the banner instead.** Rejected: the
  banner becomes a table that grows with the gallery, and the reader still has to
  know which rule applies to their entry.

## Consequences

- The page is shorter, and asserts less it cannot know.
- A reader supplies the charm channel for the LGTM backends, as they already
  supply S3 credentials for a store Atelier has never seen.
- New Juju-backed entries need no change here beyond a model pin — the failure
  mode ADR-0042 describes cannot recur through this path.
