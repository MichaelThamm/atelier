# ADR-0006: Two-pane TUI layout

## Status

Accepted

## Context

The TUI's top-level layout has to scale to modules with many variables, some
of which are deeply nested objects. COS Lite alone has 14 top-level
variables, several of which are objects with up to 6 fields each — about 50
visual rows if everything is unfolded.

Candidates considered:

- **Single-pane vertical scroll.** All variables stacked top-to-bottom; user
  arrow-keys to navigate.
- **Two-pane: list + editor.** Left pane lists variables; right
  pane is the editor for the selected variable.
- **Tabs / sections.** Top bar with named sections; one section visible at a
  time.
- **Tree.** Variables form a tree (variable → fields → nested fields), with
  a single navigator.

## Decision

**Two-pane layout** (left: variable list in declaration order; right: editor
for selected variable). Status pane at the bottom for validation and plan
errors. Plan view replaces the right pane when triggered.

Without a manifest grouping mechanism, variables appear in declaration order
in a flat list.

## Alternatives considered

### Single-pane scroll

Rejected because it does not scale. With 50+ visual rows in a moderately
sized module, the user loses their place and has no overview. A two-pane
layout gives both a navigable map (left) and a focused editing context
(right).

### Tabs

Rejected as the top-level layout because we don't know the section count
upfront. Tabs work poorly at both extremes. A flat declaration-order list
gives users a predictable, scannable list without an additional layout
dimension.

### Tree as primary navigation

Considered. Bubble Tea has reasonable tree components. Rejected because
left+right pane handles nested objects via drill-in just as well, with less
visual complexity: the right pane becomes a sub-form when the selected
variable is an object, and further drill-in opens sub-sub-forms. The tree's
benefit (showing arbitrary nesting depth) is achieved in two panes without
forcing the user to mentally model the entire tree at once.

### File-based grouping

Considered. Useless for COS Lite (all variables in one file). Conflicts with
maintainer freedom — they might split files for reasons unrelated to UI
grouping. Rejected in favour of declaration-order listing.

### Comment-marker parsing

Considered. COS Lite's `variables.tf` does have informal section comments
(`# -------------- # TLS configurations --------------`). However, the
convention is ad-hoc. Rejected: declaration order provides a predictable,
maintainer-free ordering.

## Consequences

- Bubble Tea models compose naturally: a top-level model owns the left/right
  pane state, the status pane, and the active sub-model (editor or plan
  view).
- Variables appear in declaration order in a flat list.
  No manifest authoring needed for a sensible left-pane order.
- Modified-vs-default markers appear in the left pane: `[ ]` at default,
  `[✓]` modified, `[✓N]` for object variables with N modified fields.
  Required-but-unset variables get `[!]`.
- Provider configuration appears as a top-level pseudo-group `Provider:
  <name>` in the left pane, containing the provider's configurable
  attributes.
- Plan view (triggered by `P`) replaces or expands across the panes. See
  [ADR-0011](0011-plan-output-tree.md).
- Specific colour, dimming, and focus styling decisions are deferred to a
  later aesthetics pass; this ADR pins the structural decision only.
