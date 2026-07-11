# ADR-0015: Multi-module grouping in the left pane

## Status

Accepted — extends [ADR-0006](0006-two-pane-ui-layout.md).

## Context

A wrapper's `main.tf` may contain multiple `module {}` blocks (e.g. a primary
module like `mimir` and supporting modules like `seaweedfs`). When Atelier
opens such a wrapper, it discovers all module blocks and clones each one's
source to introspect its variables.

Without visual grouping, the left pane presents a flat list of variables from
all modules interleaved by priority sort. Users cannot tell which variable
belongs to which module, making multi-module wrappers confusing.

## Decision

### Section headers

When the wrapper contains more than one module block, the left pane inserts
non-selectable section headers between groups of variables:

```
── mimir ──────────────────
[✓] channel
[ ] s3_endpoint
── seaweedfs ──────────────
[ ] model_name
```

Headers are styled distinctly (bold, secondary colour) using box-drawing
characters and padded to the pane width.

### Cursor behaviour

Section headers are not selectable. Arrow-key navigation skips over headers
automatically in both directions. Page-up/page-down and home/end also skip
headers when landing on one.

### Module ordering

The primary module (the one Atelier was initialised against) appears first.
Secondary modules are sorted alphabetically by their HCL block name.

### Single-module behaviour

When only one module block exists, no section headers are shown. The UI is
identical to the prior single-module experience — no visual regression.

### Data model

Each row in the left pane is a `rowEntry` with fields:
- `VarName` — the variable name (empty for headers)
- `ModuleIdx` — index into `Model.Modules`
- `IsHeader` — true for section dividers

`Model.Modules` is a slice of `ModuleEntry{State *wrapper.State, Name string}`.
The TUI method `recomputeRows()` rebuilds the row list whenever modules are
added or variables change.

### Secondary module loading

On open, `launchTUI` reads all `module {}` blocks from `main.tf` via
`ReadModuleBlocks`. For each block that is not the primary (identified by
matching the source attribute), it:

1. Clones the module source into a temporary directory.
2. Runs `PrepareState` to discover variables and defaults.
3. Overlays any existing values from `main.tf` into the state.
4. Calls `Model.AddModule(state, blockName)` to register it.

Loading is parallelised across secondary modules.

## Alternatives considered

### Tabs per module

Rejected. Tabs hide variables behind navigation, reducing the overview
benefit of the two-pane layout. Users would need to switch tabs to compare
values across modules.

### Colour-coded rows (no headers)

Rejected. Colour alone is insufficient for accessibility and doesn't provide
a clear label for which module a variable belongs to.

### Separate TUI invocations per module

Rejected. Forces users to run multiple sessions and mentally correlate state
across them. The wrapper is a single project and should present as one.

## Consequences

- The left pane now has a mix of selectable (variable) and non-selectable
  (header) rows. All cursor logic must account for this.
- `SelectedVariable()` and `ActiveModuleState()` resolve through the row
  entry's `ModuleIdx` rather than assuming a single state.
- Write operations (`SaveIfDirty`, plan, validate) iterate over all modules.
