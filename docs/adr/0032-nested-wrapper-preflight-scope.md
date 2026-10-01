# ADR-0032: Nested-wrapper preflight distinguishes deliberate nesting from stray `cd`

## Status

Accepted — amends [ADR-0030](0030-target-directory-preflight.md), which listed
"sits inside another wrapper" among the warnings that prompt.

## Context

ADR-0030 §1 has `inspectTarget` walk up to eight levels and, on finding an
Atelier wrapper, emit a **warning** ("this directory is inside an existing
wrapper") whose hint says to add the module to the outer wrapper instead.

That conflates two very different situations:

1. **A stray `cd`.** The user is inside a wrapper's own machinery — `.atelier/`
   or `.terraform/` — or somewhere they did not mean to be. Scaffolding a root
   there nests a Terraform root inside Atelier's state or Terraform's module
   cache, which is never intended.

2. **Deliberate nesting.** The user creates a new, independent wrapper in an
   ordinary directory below another wrapper. This is a *supported and
   encouraged* layout: [ADR-0031](0031-presets-as-tfvars-bundles.md) discovers
   personal `.tfvars` bundles by walking up from the wrapper, so a parent
   directory holding several related wrappers is exactly how presets are shared
   between them.

The old finding fired on case 2, and its hint actively misled: it told the user
to add the module to the outer wrapper, when creating a separate root was the
intent. It also forced an interactive prompt (or `--yes`) on a layout Atelier
otherwise promotes.

The concern's original justification — "nested wrappers share nothing and
confuse Terraform state" — does not hold for case 2. Terraform state is
per-directory; independent sibling or child roots do not interfere. The hazard
is real only in case 1, where the new root would sit *inside* files that the
outer wrapper already owns.

## Decision

`nestedWrapperConcern` distinguishes the two cases by where the target sits
relative to the wrapper it found:

- **Inside the wrapper's internal machinery** — any path segment equal to
  `.atelier` or `.terraform` — remains a **`levelAlarm`**: it prints and
  prompts, and without a terminal the command fails closed (unchanged from
  ADR-0030 §4).
- **Any other directory below a wrapper** becomes a **`levelNote`**: it is
  printed for context ("this directory is inside another wrapper (…); it will be
  an independent root") but never prompts and never requires `--yes`.

The repository-boundary stop and the eight-level depth bound are unchanged.

## Alternatives considered

- **Remove the nested-wrapper finding entirely.** Rejected: it would lose the
  guard against scaffolding inside `.atelier/` or `.terraform/`, which is the
  one case that genuinely nests a root inside another's files.
- **Keep it prompting and only reword the hint.** Rejected: the prompt is the
  friction, not the wording. A supported layout should not require a
  confirmation flag.
- **Ask the user to pick "outer" or "new root" at the prompt.** Rejected as
  over-built: the directory the user is standing in already answers the
  question, and the choice is reversible by moving files.

## Consequences

- `mkdir child && cd child` inside a parent wrapper now scaffolds without a
  prompt, so a collection of related wrappers under one parent works
  non-interactively — matching the preset-sharing workflow in ADR-0031.
- The guard against scaffolding inside a wrapper's internal directories is
  preserved.
- The hint shown for case 2 no longer tells the user to do the wrong thing.
- ADR-0030 §1's list of prompting findings is narrowed for this one entry; all
  other findings (foreign project markers, hand-authored Terraform, home/config
  roots, clutter) are unchanged.
