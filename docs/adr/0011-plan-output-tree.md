# ADR-0011: Plan output as module-path tree

## Status

Accepted

## Context

[ADR-0002](0002-author-and-plan-scope.md) decided Atelier invokes `terraform
plan` from inside the TUI and renders the result inline. This ADR pins down
*how* the result is rendered.

`terraform plan -json` emits structured events: for each resource, an action
(create / update / delete / replace / no-op), a resource address (e.g.,
`module.cos_lite.module.alertmanager.juju_application.alertmanager`), and
before/after attribute values.

Three rendering levels considered:

- **(a) Raw text.** Pipe `terraform plan` (non-JSON) output into a scrollable
  pane. Familiar from the shell; ugly; not interactive.
- **(b) Parsed flat summary.** Header with counts (`+12 ~0 -0`); flat list of
  resource addresses with their actions; tap a resource to see its full
  attribute diff in a side pane.
- **(c) Module-path tree.** Resources grouped hierarchically by module path,
  then by resource type, with individual resources as leaves; tap a leaf to
  see attribute diffs in a side pane.

## Decision

**Option (c): module-path tree with side-pane attribute diffs.**

Scope explicitly bounded:

- Tree grouping is `module.X.module.Y.<resource-type>.<name>`, collapsible
  at each level.
- Leaf rows show resource address, action, and a compact change indicator
  (`+`, `~`, `-`, `↻` for replace).
- Selecting a leaf row populates a side pane with full attribute-level
  diffs.
- **Per-attribute diffs are *not* rendered inline within tree rows.** That
  is genuinely future work — collapsible per-attribute rows, syntax
  highlighting, before/after value rendering with appropriate type-aware
  formatting. v1 keeps the tree clean and puts diffs in a clearly delimited
  side pane.

## Alternatives considered

### Raw text (a)

Rejected. Raw `terraform plan` output is what users already get from the
shell. The whole point of an in-TUI plan is to do better than that. A
scrollable raw-text pane gains essentially nothing over `terraform plan |
less`.

### Flat parsed summary (b)

This was the initially recommended v1 option, on the grounds that "tree is
nice but optional, defer." Reversed on reflection because:

- Terraform plan output is **inherently hierarchical**. Every resource
  address is a module path. A wrapper that wraps COS Lite has every resource
  prefixed `module.cos_lite.…`; COS Lite itself may use child modules
  internally. A flat list with these addresses is a flat list of *strings
  that desperately want to be a tree*.
- The implementation gap between (b) and (c), scoped as decided above, is
  small. (b) and (c) both parse `terraform plan -json` events and both
  render in Bubble Tea; (c) adds a tree component (which exists in the
  Bubble Tea ecosystem) and the grouping pass over events. A few extra
  days of focused work, not a quarter-long milestone.
- Atelier's pitch is "nicer than CLI plan output." Shipping a flat-list
  plan view undermines the pitch.

What was deferred is *inline per-attribute diffs inside tree nodes*,
which is the genuinely fiddly part (diff rendering, syntax highlighting,
animated reveal). v1 puts diffs in a side pane on selection.

## Consequences

- Plan view (triggered by `P`) replaces or expands across the editor panes.
  `Esc` returns to the editor.
- The plan tree is collapsible at the module and resource-type levels.
  Default expansion: top-level modules expanded, resource types collapsed.
- Side pane shows attribute diffs as `before → after` rows with
  `+`/`-`/`~` indicators. Sensitive attributes are masked (`<sensitive>`).
- The plan rendering uses `terraform-json` for structured parsing of
  `terraform plan -json` output. No bespoke parsing of human-readable
  Terraform output.
- Plan results are cached in memory for the session. The user re-runs plan
  explicitly by pressing `P`. Plan failures surface in the status pane
  (see SPEC §13.4); the previous successful plan remains accessible.
- Autoplan-on-edit is not part of v1 (see [ADR-0002](0002-author-and-plan-scope.md)).
  When/if added later, the plan rendering does not need to change.
