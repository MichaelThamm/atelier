# ADR-0020: Readline-style text editing in variable editors

## Status

Proposed — relates to [ADR-0006](0006-two-pane-ui-layout.md) (two-pane TUI
layout) and [ADR-0009](0009-secrets-handling-v1.md) (sensitive rendering).
Touches every variable widget defined in `internal/tui/editor.go`.

## Context

Atelier's right-pane editors currently treat each text cell as an
**append-only byte buffer** with no caret. The canonical example is
`stringEditor`:

```go
case tea.KeyBackspace:
    if len(e.value) > 0 {
        e.value = e.value[:len(e.value)-1]
    }
case tea.KeyRunes, tea.KeySpace:
    e.value += string(k.Runes)
case tea.KeyCtrlU:
    e.value = ""
```

The same pattern is duplicated in `numberEditor`, in `mapEditor`'s key/value
cells, and in `mapObjectEditor`'s key cell. This shape produces five
concrete problems:

1. **No caret.** A typo three characters into a 40-character value can only
   be fixed by backspacing the entire tail and retyping it. There is no way
   to position the cursor inside an existing value, so editing scales
   linearly with how far back the mistake is — terminal behaviour from the
   1970s.

2. **Byte-truncation is not rune-safe.** `e.value[:len(e.value)-1]` lops a
   single byte off the end. Any UTF-8 string ending in a multi-byte rune
   (e.g. `café`, `naïve`, `μ-service`, `日本語`) gets corrupted on the first
   Backspace: the buffer holds a partial code point that will surface as a
   replacement character or a `cty` validation error downstream.

3. **The keymap is below the floor of every other shell on the system.**
   Default Ubuntu, bash (readline), zsh (zle), VS Code's terminal, GNOME
   Terminal's prompt, and every `bubbles/textinput`-based TUI all share a
   common readline keymap: `Home`/`End`, `Ctrl+←/→` for word jumps,
   `Ctrl+W`/`Alt+Backspace` for delete-word-back, `Alt+D`/`Ctrl+Delete` for
   delete-word-forward, `Delete` for char-forward, `Ctrl+A`/`Ctrl+E` as
   emacs equivalents of Home/End. Atelier honours **none** of these inside
   a value cell. A user moving from typing into bash to typing into Atelier
   loses every reflex they have.

4. **Inconsistent shortcuts across widgets.** The same logical operation has
   different bindings (or none) in different editors:

   | Operation                | string  | number | map cell | mapObj key | object field |
   |--------------------------|---------|--------|----------|------------|--------------|
   | Delete char back         | `BkSp`  | `BkSp` | `BkSp`   | `BkSp`     | (n/a)        |
   | Clear cell               | `Ctrl+U`| `Ctrl+U`| `Ctrl+U`| `Ctrl+U`   | —            |
   | Delete row               | —       | —      | `Ctrl+D` | `Ctrl+D`   | —            |
   | Move caret in cell       | —       | —      | —        | —          | —            |
   | Cell ↔ cell (k/v)        | —       | —      | `←`/`→`  | —          | —            |
   | Field-list `Home`/`End`  | —       | —      | —        | —          | jumps field  |

   `←`/`→` *inside* a `mapEditor` switch between the key column and the
   value column rather than moving a caret. `Home`/`End` inside an
   `objectEditor` jump the field-cursor, not the caret of the focused
   scalar field. There is no shared rule about which scope a key applies
   to.

5. **`Ctrl+D` is overloaded.** In `mapEditor`/`mapObjectEditor` it deletes
   the current row. In every other shell on the system, `Ctrl+D` either
   sends EOF or deletes the character under the caret. Once we add
   caret-aware text editing, the row-delete binding stops being defensible.

The cumulative effect is that the editor surface — the central UX of
Atelier — feels several decades older than the rest of the tool.

## Decision

Adopt a **readline-compatible text-editing model inside every scalar cell**
and rationalise the keymap so each key has a single, scope-appropriate
meaning. The change is constrained to scalar text/number cells and the
key/value text cells of map editors; structural keys (row navigation,
drill-in/out, modal triggers) are unaffected except where they currently
collide.

**North star: terminal/readline convention, not GTK text-field
convention.** Atelier is launched from and lives in a shell, so a user's
dominant reflexes are bash/zsh/`bubbles/textinput`, not Firefox or GNOME
Text Editor. Where the two conventions genuinely conflict we pick readline.
The one visible casualty is `Ctrl+A`: it is caret-to-start here, not GTK's
select-all (Atelier has no in-cell selection model — see Out of scope).
This is a deliberate trade-off, called out again in Consequences. Where
readline leaves a key *unbound*, we are free to add GTK aliases (see
`Ctrl+Backspace` in §2) — additive aliases cost nothing and rescue desktop
muscle memory.

### 1. Caret-aware scalar cell

Every place that currently holds a `string` text buffer with no caret is
replaced by a `bubbles/textinput.Model`, the official text-input widget
from the `charmbracelet/bubbles` component library that is the sibling
repo of bubbletea itself. This component is rune-safe, supports the full
readline keymap below, and exposes a stable `Value()`/`SetValue()` API.
The same migration is applied to `refInput` in the ref-switch modal
(`view.go` / `model.go`) so that the project ends up with a single
canonical text-input cell rather than two parallel byte-buffer
implementations.

Affected editors:

- `stringEditor`  — wraps a single textinput; `Sensitive` toggles
  `EchoMode = EchoPassword`.
- `numberEditor`  — wraps a textinput with a `Validate` function that
  rejects characters outside `0-9 . - + e E`; the rune-filter currently
  done by string concatenation moves into the validator.
- `mapEditor`     — each row owns two textinputs (key, value); the focused
  one receives keystrokes.
- `mapObjectEditor` — the key cell becomes a textinput; the value cell
  remains an `Editor` reached via drill-in.

`listEditor` keeps its current scalar-add/scalar-delete shape for v1
(string elements only; ADR-0006 already classes richer list editing as
deferred). When list elements gain in-line editing in a later ADR, they
adopt this same cell.

### 2. Canonical keymap inside a focused scalar cell

The keymap below is **the** keymap. Every scalar cell honours every row;
no cell adds bindings outside this list, no cell omits one.

| Key                             | Action                                       |
|---------------------------------|----------------------------------------------|
| Printable runes / `Space`       | Insert at caret                              |
| `Backspace`                     | Delete char left of caret                    |
| `Delete`                        | Delete char at caret                         |
| `←` / `→`                       | Move caret one char                          |
| `Ctrl+←` / `Ctrl+→`             | Move caret one word                          |
| `Alt+B` / `Alt+F`               | Same as `Ctrl+←` / `Ctrl+→` (emacs alias)    |
| `Home` / `Ctrl+A`               | Caret to start of cell                       |
| `End`  / `Ctrl+E`               | Caret to end of cell                         |
| `Ctrl+W` / `Alt+Backspace`      | Delete word left                             |
| `Alt+D` / `Ctrl+Delete`         | Delete word right                            |
| `Ctrl+U`                        | Delete from caret to start of cell           |
| `Ctrl+K`                        | Delete from caret to end of cell             |
| `Ctrl+T`                        | Transpose char before caret with char at it  |

This is exactly the readline default set that ships with bash, zsh (in
emacs mode, which is the default), `bubbles/textinput`, and VS Code's
terminal. **`Ctrl+U` changes meaning** from "clear whole cell" to
"kill-to-start-of-line." Clearing a cell to empty is now the two-chord
sequence `Ctrl+A` `Ctrl+K` (or `End` `Ctrl+U`). Note that `Ctrl+R`
(reset-to-default, see ADR-0006) is **not** a replacement: it restores the
variable's *default*, which is a different operation from clearing to empty
and is empty only when the default is. Standardising on the readline kill
commands costs us the one-keystroke clear; this is accepted for v1. If
feedback shows clear-to-empty is high-frequency, that is the trigger for a
dedicated binding — we do not pre-solve it here.

**`Ctrl+Backspace` is bound as a third alias for delete-word-left**, on top
of `Ctrl+W` / `Alt+Backspace`, because it is the GTK/GNOME desktop reflex
and readline leaves it free. This is **gated on the terminal actually
emitting a distinct key event**: many terminals send `Ctrl+Backspace` as
`Ctrl+H` (`0x08`), indistinguishable from plain `Backspace`. Bind it only
where `bubbles/textinput` surfaces it as its own key; where it collapses to
`Backspace`, drop the alias rather than fake it.

**`Ctrl+Y` (yank) is intentionally unbound** — see Out of scope. The kill
commands above (`Ctrl+W`, `Ctrl+K`, `Ctrl+U`) delete in place; there is no
kill-ring in v1, so the readline reflex of `Ctrl+K` then `Ctrl+Y` to
re-paste does nothing by design.

### 3. Resolving cross-scope collisions

Three keys today carry different meanings depending on which widget is
focused. We pick a single rule and stick to it:

- **`←` / `→` in `mapEditor`.** Today they switch between the key column
  and the value column. After this ADR they move the caret inside the
  focused cell. Cell-to-cell movement uses `Tab` (key → value → next-row
  key) and `Shift+Tab` (reverse), which is the universal form-navigation
  convention and is unbound today. `Tab` is governed by a single
  **scope-based** rule, not a per-widget one: when a cell is focused, `Tab`
  moves to the next cell; when no cell is focused (row-cursor scope), `Tab`
  is the global pane-toggle. There is no `Esc Tab` chord — to toggle panes
  from inside a form you `Esc` to row scope (which you do anyway to leave the
  cell) and then a single `Tab` toggles. One key, one rule per scope, mirror-
  ing the `Ctrl+D` resolution below; the old "depends which widget I'm in"
  overload is not reintroduced.

- **`Home` / `End` in `objectEditor`.** Today they jump the field-cursor
  to the first/last field. After this ADR, when the focused field is a
  scalar with a textinput, `Home`/`End` belong to the textinput
  (caret-to-edge). Field-list jumps move to `Ctrl+Home` / `Ctrl+End`. We do
  **not** use `g` / `G` here: the editor surface is emacs/readline-flavoured
  throughout (this ADR's own Alternatives section rejects vim bindings in a
  cell for exactly this reason), and `Ctrl+Home`/`Ctrl+End` is the natural
  `Ctrl`-escalation of `Home`/`End` — the same char-vs-whole pattern as
  `←` vs `Ctrl+←`. `g`/`G` stay confined to the output view, where no text
  cell is focused and they are unambiguous. `PgUp`/`PgDn` continue to do
  half-page field navigation as today.

- **`Ctrl+D` and row-delete in `mapEditor` / `mapObjectEditor`.** Row-delete
  is a **structural** operation and lives only at the row-cursor scope (no
  cell focused), bound to `Alt+Delete` (mnemonic: "delete this whole row").
  Inside a focused cell **no** structural delete is bound — to remove a row
  you `Esc` to row scope first. `Ctrl+D` is therefore freed unconditionally
  for the readline char-forward delete inside a cell, and is unbound at row
  scope. This keeps `Alt+D` (delete-word-forward, in-cell) and `Alt+Delete`
  (row-delete, row scope) in **separate scopes** so the one-keystroke-apart
  pair never coexists, and avoids a destructive whole-row delete firing while
  the user is mid-edit. (Row-delete is destructive and unconfirmed today;
  whether it earns a confirm or undo is left to a future ADR.)

### 4. Visual treatment

- Caret rendered as a reverse-video block on the character it sits on, or
  on a single-cell space placeholder at end-of-cell. Reuses the
  `styleCursorActive` style already defined in `theme.go`.
- Sensitive cells render `•` per rune (existing behaviour) and never echo
  the character at the caret position even momentarily.
- Cell width is unchanged: textinput is configured `Width: budget` from
  the same right-pane budget calculation as today, with horizontal scroll
  inside the cell when the value exceeds the budget (textinput handles
  this natively via `Cursor` tracking).

### 5. Help surface

The help modal (`?`) gains an "**Editing a value**" section listing every
row of the table in §2 verbatim. The README's existing "Keyboard shortcuts"
table gains the same section. The per-editor inline hint
(`typeSpecificHint` in `editor.go`) shrinks to a single line —
`type to edit · Home/End · Ctrl+←/→ word · Ctrl+W del word` — pointing at
the help modal for the rest. The hint must not duplicate the full table;
the help modal is the source of truth.

Because the keymap roughly triples in size while the inline hint stays one
line, discovery is made **active** rather than relying on users
spontaneously opening `?`: the first time a cell is focused in a session,
and whenever an unrecognised chord is pressed inside a cell, a brief
transient nudge (`press ? for editing keys`) is shown. This teaches the
expanded keymap without duplicating the table inline.

## Alternatives considered

### Hand-roll a caret on top of the existing `string`

Rejected. We would re-implement, badly, what `bubbles/textinput` already
does correctly: rune-aware indexing, word-boundary rules
(`WordCharacters`), kill-ring semantics, horizontal scroll for narrow
cells, and IME composition. The textinput is a charmbracelet-supported
component on the same release cadence as bubbletea itself; reimplementing
it is pure liability.

### Adopt a full editor (e.g. `bubbles/textarea` everywhere)

Rejected for v1. Multi-line editing in a scalar variable cell is not
useful — Terraform string variables that legitimately span lines are rare,
and the existing `readOnlyEditor` fallback ("use $EDITOR on main.tf for
now") covers them. textarea also doubles the keymap surface (`Up`/`Down`
move the caret, colliding with row navigation everywhere), and the v1
contract (ADR-0006) is a flat scalar cell. We can promote individual
cells to textarea later when a concrete need surfaces.

### Embed a real `$EDITOR` shell-out for every text cell

Rejected. Suspending the TUI to launch `vim`/`nano` for every string
variable destroys flow, breaks debounced validation (ADR-0012), and
duplicates work the user came to Atelier specifically to avoid. The
existing `$EDITOR on main.tf` escape hatch is reserved for types we don't
have widgets for.

### Keep the append-only model and just add `Backspace`-rune-safety

Rejected as a half-fix. The rune-safety bug is real and must be fixed
regardless, but doing only that leaves problems 1, 3, 4, and 5 from the
context untouched and locks in the inconsistent keymap as "intentional."

### Implement a vim-style modal editor inside cells

Rejected. The rest of the TUI is emacs/readline-flavoured (`Ctrl+R`,
`Ctrl+U` today, `Ctrl+C` to quit) and the surrounding shells are too
(bash, zsh, GNOME Terminal's prompt). A modal cell would be the only
place in Atelier where keystrokes don't mean what they mean elsewhere.
The output view's `j`/`k`/`g`/`G` set is non-modal — those keys are
unambiguous because no text cell is focused there.

## Consequences

- **All scalar text editing in `internal/tui/editor.go` flows through one
  widget.** Five copies of the byte-append/byte-trim pattern collapse into
  one textinput per cell. The bug class in problem 2 (UTF-8 corruption)
  disappears at the source.
- **The keymap matches every other shell on the system.** A user's bash /
  zsh / VS Code reflexes carry over directly, including word jumps, kill
  shortcuts, and Home/End. The "feels old" complaint is resolved without
  changing the visual layout.
- **Three scope collisions go away** (`←/→`, `Home/End`, `Ctrl+D`) and
  the rules for which scope owns a key are documented in §3 — no more
  "depends which widget I'm in" surprises.
- **`Ctrl+U` semantics shift** from "clear whole cell" to standard
  readline kill-to-start. Documented in §2 and the help modal. There is no
  longer a one-keystroke clear-to-empty: it becomes `Ctrl+A` `Ctrl+K`.
  `Ctrl+R` is reset-*to-default*, a different operation (empty only when the
  default is). This is an accepted v1 cost; a dedicated clear binding is a
  watch item gated on usage feedback, not pre-solved here.
- **`Ctrl+A` is caret-to-start, not GTK select-all.** Forced by the
  readline north star (see Decision). `Home` covers the same action; there
  is no in-cell selection model in v1 to fall back to.
- **Existing tests that simulate `Backspace` to clear a value will need
  updates** where they assert behaviour that depended on the old
  byte-trim contract. The textinput exposes `SetValue("")` for tests that
  want to construct a starting state directly; `Update(tea.KeyMsg{Type:
  tea.KeyBackspace})` continues to work for keystroke-driven tests.
- **One new direct dependency.** `github.com/charmbracelet/bubbles` is
  added to `go.mod`; it is the sibling component library to bubbletea
  itself and is already the canonical source of textinput in the
  charm ecosystem. No new transitive licences appear (MIT, same as the
  rest of the charm stack).
- **Help modal and README grow one section** (§5). The per-widget inline
  hint shrinks correspondingly; the help modal becomes the single source
  of truth for editing keys.

## Out of scope

- **Multi-line cells.** Deferred (see "Adopt a full editor" alternative).
- **Selection / kill-ring across cells.** Deferred. `Ctrl+K`, `Ctrl+U` and
  `Ctrl+W` delete in place, no yank buffer; `Ctrl+Y` is intentionally
  unbound (§2).
- **List(T) inline element editing.** ADR-0006 already defers richer list
  editing; this ADR specifies the cell shape that list will adopt when
  that work lands, but does not implement it.
- **Bracketed paste / undo history.** textinput supports paste natively
  (it arrives as one `tea.KeyRunes` event); per-cell undo is deferred to
  a future ADR if user feedback demands it.
- **Vim-mode opt-in.** Not pursued in v1 (see alternatives).
