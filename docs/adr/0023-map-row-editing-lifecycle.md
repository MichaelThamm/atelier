# ADR-0023: Map / map(object) row editing lifecycle

## Status

Proposed — refines [ADR-0020](0020-readline-style-text-editing.md)
(readline-style editing) and relates to [ADR-0006](0006-two-pane-ui-layout.md)
(two-pane layout). Touches `mapEditor`, `mapObjectEditor`, and `objectEditor`
in `internal/tui/editor.go`, plus the key-routing in `internal/tui/model.go`.

## Context

ADR-0020 gave every scalar cell a readline caret, but it left the *row
lifecycle* of the map editors — add, name, fill, navigate, delete — as
implementation detail. In practice that lifecycle was inconsistent and, in
places, unsafe:

1. **Inconsistent "Add row".** In `mapEditor` (`map(string)`), pressing Enter
   on `+ Add row` appended an empty row and focused its **key** cell. In
   `mapObjectEditor` (`map(object)`), the same gesture appended a row and
   **immediately drilled into the nested object editor**, leaving the key
   blank. A user had to remember to `Esc` back out to name the key. The two
   editors taught contradictory mental models for the same action.

2. **Silent data loss.** A row whose key was never typed was silently dropped
   at serialization time (`CurrentValue()` skips empty keys). In
   `map(object)` this could discard a fully-filled nested object with no
   warning.

3. **Dead / stale key bindings.** `mapEditor` handled `Tab`/`Shift+Tab` for
   key↔value cell cycling, but the top-level model intercepts `Tab`
   unconditionally for pane switching — so the editor never received it. The
   inline hint advertised `Tab` cell cycling that could not fire, and (in an
   earlier revision) a `[Ctrl+D] delete row` hint that no longer matched the
   `Alt+Delete` binding.

4. **`Esc` could not step back through nesting.** The model intercepts `Esc`
   and jumps straight to the left pane regardless of how deep the user has
   drilled, so backing out of a nested object one level at a time was
   impossible.

5. **Irreversible one-keystroke delete.** `Alt+Delete` removed a row
   unconfirmed, so a fat-finger could wipe a populated nested object.

## Decision

Adopt a single, consistent row lifecycle across both map editors, anchored on
one verb for **Enter** and one verb for **Esc**.

### 1. Key-first, always

`+ Add row` (Enter, or the last-value Enter rhythm below) appends a row and
focuses its **key** cell in *both* editors. A row reads as `key = value`, so
the user always names the key first — matching how HCL reads.

### 2. Enter = "advance forward; create a row only at the very end"

`Enter` is the single forward-advance verb:

| Editor       | Cursor / state              | Enter                              |
|--------------|-----------------------------|------------------------------------|
| both         | on `+ Add row`              | add row, focus **key**             |
| `map(string)`| key cell, key empty         | **blocked** ("key required")       |
| `map(string)`| key cell, key non-empty     | advance to the **value** cell      |
| `map(string)`| value cell, non-last row    | advance to the **next row's key**  |
| `map(string)`| value cell, last row        | commit + new row, focus **key**    |
| `map(object)`| key cell, key empty         | **blocked** ("key required")       |
| `map(object)`| key cell, key non-empty     | **drill into** the object editor   |
| `objectEditor`| scalar field               | advance to next field (existing)   |
| `objectEditor`| collection field           | drill deeper (existing)            |

`Tab` no longer cycles cells — Enter already advances key→value, so cell
cycling is redundant. `Tab` keeps its single app-wide meaning: **switch
panes**.

#### Caret-boundary arrow aliases (`map(string)` only)

Because a `map(string)` value is a flat sibling cell, the horizontal arrows
also promote across the key/value boundary, mirroring `Enter`'s key→value move
at the caret level:

- `→` at the **end** of a non-empty key cell moves to the **value** cell;
  anywhere else it is an ordinary caret move.
- `←` at the **start** of the value cell returns to the **key** cell; the
  natural inverse, so the promotion is never a one-way trap.

This alias does *not* apply to `map(object)`, whose value is a nested object
reached by drilling in (`Enter`), not a sibling cell.

### 3. Esc = "back one level", symmetric with Enter's descent

`Esc` pops exactly one drill level. Enter goes deeper one rung at a time; Esc
comes back one rung at a time. Only when the editor is at its **top level**
(drill depth 0) does `Esc` leave the editor and return focus to the variable
list. The model consults the editor's depth (a new `depthProvider` interface)
and delegates `Esc` to the editor whenever depth > 0, pane-switching only at
depth 0.

### 4. Empty keys: block forward, abandon empty

- **Forward motion off an empty key** (Enter to advance / drill) is
  **blocked** with an inline "key required" nudge, because forward motion
  implies committing the row.
- A **freshly-added, untouched row** (empty key *and* no value content) is
  treated as abandoned: moving away with `↑`/`↓`/`Esc` silently removes it,
  exactly like backing out of `+ Add row` without adding. Because a
  `map(object)` row cannot be drilled into before its key is named, the
  "filled object with no key" state is structurally impossible for fresh
  rows.
- Rows loaded from `main.tf` are never auto-abandoned; an empty key on a
  pre-existing row is surfaced as an invalid-row marker rather than silently
  dropped.

### 5. Delete: type-aware confirmation, keybinding unchanged

`Alt+Delete` remains the row-delete chord (chosen by ADR-0020 to avoid the
readline collisions). Deletion is proportional to what is at stake:

- an **empty / abandonable** row deletes instantly;
- a **populated** row requires **pressing `Alt+Delete` again** to confirm
  (an inline status-line nudge, no modal). No undo stack — undo is a separate
  concern deferred to its own ADR.

### 6. Visual language

Reusing the app's existing marker vocabulary:

- **Row status glyph** in the left margin: valid & complete, invalid
  (empty key / fails validation), or blank for a fresh empty row.
- **Collapsed `map(object)` row summary** — `keyname (object: N set)` via the
  existing `compactObjectCount`, so a filled row is self-describing without
  drilling.
- **Blocked-key feedback** — an empty key cell shows a dim `(key)`
  placeholder; a blocked forward attempt flashes "key required" in the hint
  line. No error styling while the user is mid-type.
- **Delete-confirm feedback** — the doomed row is highlighted and the hint
  line shows "press Alt+Del again to remove …".

## Consequences

- The two map editors now teach one mental model: *name the key, then Enter
  to advance into the value (a cell for `map(string)`, a drill-in for
  `map(object)`); Esc backs out one level; Alt+Delete removes a row (twice if
  populated).*
- The model gains a tiny `depthProvider` interface and consults it before
  handling `Tab`/`Esc` in the right pane. Pane focus stays the model's job;
  within-editor navigation is the editor's job; the handoff point is the
  editor's root.
- `Tab`/`Shift+Tab` cell cycling is removed from `mapEditor` (it was dead
  code). Hints, the help modal, and the README are corrected to match.
- No unnamed row can silently survive: it is either abandoned (fresh) or
  flagged as invalid (pre-existing). This removes the ADR-0020-era silent
  drop.

## Alternatives considered

- **Value-first / drill-first add** (today's `map(object)` behaviour, unified
  onto `map(string)`): rejected — it does not match how maps are read and it
  is the very inconsistency that motivated this ADR.
- **Modal key prompt** before materializing a row: rejected — heavier than an
  inline cell and breaks the "it's just a grid" feel; inline validation gives
  the same non-empty guarantee.
- **`Esc` exits the whole editor at any depth**: rejected — hostile when deep
  in a nested object; breaks the Enter/Esc symmetry.
- **Undo stack for deletes**: deferred — a genuinely separate feature; adding
  it only for row-delete would be an inconsistent island.
- **Reassigning the pane-switch key off `Tab`**: rejected — out of scope and
  high blast radius for an app-wide interaction.
