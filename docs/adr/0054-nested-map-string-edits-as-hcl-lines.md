# ADR-0054: A nested `map(string)` is edited as HCL lines, and its keys are named on the parent row

## Status

Accepted — amends [ADR-0023](0023-map-row-editing-lifecycle.md) for the
nested case. ADR-0023's two-column row grid stands for a `map(string)` that
owns the right pane.

## Context

Two defects, both visible on cos-lite's `alertmanager`:

```hcl
variable "alertmanager" {
  type = object({
    app_name           = optional(string, "alertmanager")
    config             = optional(map(string), {})
    constraints        = optional(string, "arch=amd64")
    resources          = optional(map(string), {})
    storage_directives = optional(map(string), {})
    units              = optional(number, 1)
  })
}
```

**A collection field did not say what it held.** `config` rendered as
`(map: 2 entries)`. A count names no keys, so `config`, `resources` and
`storage_directives` were indistinguishable at a glance, and reading any of
them cost a drill-in and an Esc back out. For the variables a COS user
edits most, every inspection was a round trip.

**The two-column grid was the wrong shape one level down.** It is right for a
`map(string)` that owns the right pane: every row and the add affordance are
visible at once, and the key/value separation helps when keys and values are
long. As one field of a seven-field object it costs more than it gives:

- The key is a separate cell to reach. Changing a value means Enter to
  advance `key → value`, because there is no other way across.
- Enter then means three different things depending on the column: key→value,
  value→next row, and add-row→append.
- Rows are sorted on load (`newMapEditor`). Go randomises map iteration, so
  sorting is needed for a stable widget — but the user's `main.tf` order is
  what they wrote, and the grid returns it alphabetised.

The line form fixes the shape: the widget renders the HCL it edits, so what
the user sees is what `RenderMain` emits, and a value is edited where it sits.
The caret starts at the end of the line, so changing a value is
Backspace-and-type with no navigation at all.

Across six gallery products (114 top-level variables, 298 object fields) three
quarters of the nested collections are `map(string)` — `config` (28),
`resources` (23) and `storage_directives` (22) alone account for 73 of 116. So
the line form is not a rare shape; it is the common one.

That sample is not the whole gallery. haproxy's
`protected_hostnames_configuration`, a **required** `list(object(...))`, was
missed by it, and the widget that mishandled it is the subject of §3 and §4.
A schema sample bounds what was *looked at*, not what exists.

## Decision

### 1. A collection field names its keys on the parent row

`(map: 2 entries)` becomes `(retention_time,replicas)`, and an empty
collection is `(empty)`. At most three keys are listed and the remainder is a
`+N` count; long keys are clamped. A nested `object` field lists its field
names the same way, so `cloud` reads `(name,region)` rather than
`(object: 2 fields)`.

The listing contains **no spaces**. `renderRightPane` word-wraps on spaces and
then hard-truncates what is left, so a preview that can be split gains a
physical row, overflows the pane's fixed height, and shoves its bottom border
down. An unbreakable token is truncated cleanly instead.

A preview still over-runs a narrow terminal's value column, which bumps it to
its own line. A long string value already behaves that way, so this is not a
new layout fault.

### 2. A nested `map(string)` uses the line form

`lineEditor` renders and edits one `key = value` per line. Enter appends a
line. Alt+Delete removes one, confirming when the line is populated.

A line with no `=`, or with an empty key, is **not an entry and not an error** —
it is a key the user has not finished typing. It is dropped from the value
rather than written, because a partial entry in `main.tf` is worse than none.

The dispatch lives in `newFieldEditor`: a field whose type is `map` with scalar
elements gets the line form. A **top-level** `map(string)` variable keeps the
grid — it owns the right pane, where the grid's affordances are all visible at
once.

### 3. A scalar `list` or `set` uses the same line form

`lineEditor` covers any collection of scalars: a map line is `key = value`, a
list or set line is the bare value. A list keeps its written order — unlike the
grid, which sorts — and a set folds duplicates the way `cty` does.

This replaces a widget that had no caret at all: `a` appended an empty string
and `d` removed the last entry regardless of which one you were looking at, so
there was no way to put a value into an entry. That widget also corrupted
`list(object(...))` by stringifying elements with `cty`'s `GoString`; see
[#69](https://github.com/MichaelThamm/atelier/pull/69).

### 4. A composite `list` or `set` is read-only

A `list(object(...))` or `set(object(...))` renders an entry count and a
pointer to `main.tf` / `--var`. Atelier does not report a value for it, which
is what stops the model pushing a lossy one into state. Hand-editing is the
honest instruction only once the round-trip preserves what was written; that
is the `UnknownAttrs` work below, and until it lands the instruction means
"Atelier will reformat it, not lose it".

### 5. Two widgets for one type is a known cost

A `map(string)` now has two presentations. That is duplication, and it is
accepted deliberately for the scoped change: the nested case is the one that is
mis-shaped, and it is the one users hit. Collapsing both onto the line form
would remove `mapEditor` outright and is the cheaper end state, but it changes
the top-level experience too, which is not justified by the evidence here.

## Alternatives considered

**Grey out nested maps and send them to `main.tf`.** Rejected: `config`,
`resources` and `storage_directives` are the fields a COS user edits most, and
they are flat `map(string)`. The composites that are genuinely hard —
`map(object(...))`, and trino's `endpoint_bindings` (`set(object)`) — are nine
fields across all six products, and they keep their editors.

**Keep the grid but make it two-dimensional-friendly.** Rejected: it keeps the
column-to-column Enter and the forced alphabetical order, which are the two
costs the line form removes.

**Render the preview as a count *and* the keys.** Rejected: it is longer and
the count adds nothing the key list does not already convey.

**Preview keys only for maps, leaving nested objects as a count.** Rejected:
`cloud` was `(object: 2 fields)` in the same screenshot, and the two branches
now share one helper. Leaving one as a count would be inconsistent inside a
single widget.

**Launch `$EDITOR` on a greyed field.** Rejected: `SaveIfDirty` writes on every
keystroke, so resuming with in-memory state the editor just invalidated would
have the next keystroke silently overwrite the user's edit. It needs a
re-read-and-reconcile pass, in an adapter. `atelier apply --var` already
covers the case with a type-checked deep merge.

## Consequences

- Reading a collection no longer costs a round trip.
- A nested map's keys stay in the order the wrapper had them, so a hand-edited
  `main.tf` reads back the way it was written.
- Editing a nested map value needs no navigation: the caret starts at the end of
  the line.
- The `?` modal gains a separate section per map form. It is the source of
  truth for keybindings, so the two had to be distinguished.
- `internal/tui` gains one editor covering every collection of scalars. The
  grid is untouched, so its tests and
  [ADR-0023](0023-map-row-editing-lifecycle.md) still hold.
- The old `listEditor` is gone, along with the `a`/`d` bindings over a buffer
  with no caret.
- **Not addressed here:** `ReadMain` evaluates every attribute to a `cty.Value`
  and `RenderMain` re-renders it, so comments and alignment *inside* a complex
  value are lost on the first save whether or not it was edited in the TUI.
  Routing non-editable types to `UnknownAttrs` would preserve them, and is
  separate work — it is a precondition for ever telling a user to hand-edit a
  nested collection, not a consequence of this decision.
