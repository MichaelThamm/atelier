# ADR-0046: A gallery card names its required inputs once

## Status

Accepted — amends [ADR-0037](0037-gallery-pages-site.md)'s card contents. The
generated site stands.

## Context

ADR-0037 gave a card "the preset and required inputs when present". The
required inputs are already in the command the same card offers:
`ApplyCommand` renders every `requires` entry as a `--var`, so `tempo-operators`
printed `Needs channel=dev/edge, model_uuid, s3_access_key, s3_secret_key,
s3_endpoint.` directly above a command carrying all five.

The prose adds nothing. It is the same list, in the same order, with the
`--var` spelling dropped — so a reader is asked to reconcile two renderings of
one fact and learns nothing from the second. On the widest cards it is also the
longest line, and the entries with the most required inputs are the ones whose
card was tallest.

The Juju page ([ADR-0041](0041-juju-opinionated-gallery-page.md)) never printed
it, so the same entry said two different things about what it needs depending on
which page the reader was on.

## Decision

A card states a required input **once**, in the command it offers. The
generator does not render `requires` as prose.

`requires` keeps every other job: it is what
[ADR-0039](0039-composed-gallery-presets.md)'s coverage lint checks, what
`atelier gallery requires <name>` prints, and what the Juju page's variants
resolve values for.

## Alternatives considered

- **Keep the line, worded as a warning** ("You'll need …"). Rejected: the
  warning is the duplication. It changes the register of the card to make a
  reader brace for a command that already states the requirement.
- **Move the values into the prose and strip the placeholders.** Rejected: the
  one-liner is the artifact the page publishes, and a command that is not
  copy-pasteable is not worth printing.
- **Drop the line only when an entry has more than one required input.** Rejected
  as an arbitrary rule: it makes card shape depend on a count rather than on
  whether a fact is already on the page, and leaves single-input cards the only
  ones restating themselves.
- **Leave it for discoverability.** Rejected: a reader scanning for what an entry
  needs reads the command. Search over the page already indexes the `--var`
  names, since it indexes the code block.

## Consequences

- Cards are shorter, most visibly on the S3-backed observability entries.
- The two pages describe an entry identically, so nothing has to be kept in step
  between them.
- A required input the command does not carry would now be invisible on the page.
  That cannot happen: `requires` is what `ApplyCommand` reads, so the two are the
  same list by construction, and `gallery lint` fails an entry that names an
  input the module no longer declares
  ([ADR-0039](0039-composed-gallery-presets.md)).