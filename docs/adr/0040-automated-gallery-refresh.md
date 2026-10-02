# ADR-0040: Automated gallery refresh on a schedule

## Status

Accepted — extends [ADR-0039](0039-composed-gallery-presets.md).

## Context

Every gallery entry pins its module to a full commit SHA. A pin rots: the
product modules move on Canonical's own cadence (Ubuntu release cycles), and
until something notices, `atelier apply <name>` deploys a stale module.

Until now the only path back was reactive. The `gallery-bump` skill assumes
someone saw a failure — usually a `gallery-check` failure after a pin was moved
by hand, or a bug report that a quick start had drifted. Nothing watched for a
pin falling behind, so the staleness was found by users rather than by us.

Two facts shape the fix. First, `gallery-check` only catches drift when a pin is
already moved, so it cannot prompt a bump. Second, a moved pin changes what a
documented one-liner deploys, so it must be reviewed rather than pushed blind.

## Decision

Add `tools/gallerybump` and a scheduled workflow that turns a pin move into a
reviewable pull request.

**The tool** resolves each entry's module with `git ls-remote` and rewrites
**only** `ref`. It patches the manifest source instead of re-encoding it, so a
run's diff is exactly the SHA lines and the maintainers' hand-written JSON is
never reformatted. A no-op run is byte-identical and writes nothing. A module
that cannot be reached fails the run and keeps its pin, so a flaky remote never
produces a half-bumped manifest.

**The workflow** runs weekly (and on `workflow_dispatch`). It bumps, validates
with `gallery-lint` and `gallery-check`, and only then opens or refreshes a
single PR on a bot-owned branch. If nothing moved, it exits quietly. If
validation fails, the job fails and no PR is opened — `main` keeps the last
known-good pins.

The bot never touches `presets`, `requires`, `block`, `subdir`, or the module
URL. Those are human judgements: a module that renamed a variable or added a
required input is a PR for a person, which is what the `gallery-bump` skill
then walks through.

## Alternatives considered

- **Commit bumps straight to `main`.** Rejected: an unreviewed pin move changes
  what a documented one-liner deploys, and a bad pin reaches every user of that
  gallery entry at once.
- **Trigger only on upstream tag releases.** Rejected: several modules publish
  no tags, and the relevant unit is usually a track branch rather than a
  release. `workflow_dispatch` covers a specific upstream event on demand.
- **Cron keyed to the Ubuntu release date.** Rejected: a cron cannot key off a
  release date, and pinning it to a guessed day is worse than a weekly cadence
  that reports drift within a week.
- **Let the bump rewrite presets too.** Rejected: preset contents are a
  judgement about the product, not a mechanical derivation from a SHA.

## Consequences

- Pins stay current within a week of an upstream move, and the PR is the review
  surface where a drifted entry is corrected.
- A bump that breaks an entry is caught before it reaches `main`, rather than by
  a user's apply.
- The bot force-pushes a dedicated branch, so only the bot writes it; the PR is
  reused across runs instead of opening a new one each week.
- GitHub disables scheduled workflows after 60 days without repository activity,
  so `workflow_dispatch` remains the manual path.
- `just gallery-bump` runs the same tool locally, which is how a maintainer
  reproduces or pre-empts what the bot will propose.