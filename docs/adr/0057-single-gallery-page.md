# ADR-0057: One generated gallery page, with provider variants on each card

## Status

Accepted — supersedes [ADR-0041](0041-juju-opinionated-gallery-page.md)'s split
between a provider-agnostic page and a provider-specific one. The per-card pins
of [ADR-0042](0042-juju-page-pins-and-own-model.md), the
environment-over-guessing rule of
[ADR-0043](0043-juju-page-guesses-nothing.md), and the model exports of
[ADR-0049](0049-juju-page-model-from-environment.md) stand, rendered on the one
page.

## Context

[ADR-0037](0037-gallery-pages-site.md) generates the site's provider-agnostic
Gallery page from the manifest.
[ADR-0041](0041-juju-opinionated-gallery-page.md) added a second page, `/juju/`,
beside it.

That second page renders **every** entry, and each card it writes is the Gallery
card — description, module link, pinned ref, presets, and the same
`ApplyCommand` — with the Juju variant appended in a collapsed block. So
`/juju/` is a strict superset of `/gallery/`, and
[ADR-0046](0046-gallery-card-states-inputs-once.md) fixes the two to describe an
entry identically. The gallery content is duplicated wholesale: the reader
browses two pages for one list, and every card change is written twice.

The duplication is structural, not incidental — it is one page per provider over
the same cards. A second provider would add a third page carrying the same cards
again, and force a provider taxonomy onto the manifest or the generator to decide
which page an entry belongs on.

## Decision

Publish **one** generated page, `/gallery/`.

- Each card is the provider-agnostic card the manifest derives. A Juju entry
  additionally renders its collapsed variant beneath the command, unchanged from
  ADR-0041/0042/0049.
- The conventions the Juju page stated once in a banner move into a collapsed
  "Deploying into a Juju model" block above the grid: the `juju switch` and the
  `CURRENT_MODEL`/`CURRENT_MODEL_NAME` exports, and the note on which entries
  demand a model and which override their module's own. The page's default
  statement stays provider-neutral; the opinion is opt-in, at both the page and
  the card.
- `tools/gallerysite` loses its `-page` flag and `renderJujuPage`;
  [`juju.go`](../../tools/gallerysite/juju.go) keeps the pins and the variant
  renderer, which the single card calls. The manifest, `internal/gallery`, and
  the CLI are untouched, so the binary stays provider-agnostic.
- `mkdocs.yml` drops the `Juju` navigation entry, and `/juju/` is no longer
  built.

## Alternatives considered

- **Keep the two pages.** Rejected: the Juju page is the Gallery page plus a
  variant, so one of the two is always the other's duplicate.
- **Make `/gallery/` a thin index linking to `/juju/` and `/aws/`.** Rejected:
  it multiplies the duplication, and routing an entry to a provider page needs a
  provider field in the manifest or the generator — the provider knowledge
  ADR-0041 deliberately keeps out of `internal/gallery`.
- **Inline the Juju command as the card's own command.** Rejected by ADR-0041:
  on several entries pinning the model overrides what the module would do, so it
  is a choice the reader makes, not the default they are handed.
- **Drop the Juju variant and ship only the neutral command.** Rejected: the
  ready-to-run commands are the reason the variant exists; the neutral command
  keeps placeholders a Juju user would otherwise fill by hand.

## Consequences

- One page to build, read, and keep true. ADR-0046's "the two pages describe an
  entry identically" is moot — there is one description.
- The Juju variant keeps its gates: `just gallery-check` still asserts every pin
  reaches the wrapper and every `create_model` arrives with its `model_uuid`.
- A future provider adds a collapsed variant to the card, and a block to the
  conventions note — not another page of duplicated cards.
