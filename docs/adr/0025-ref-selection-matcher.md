# ADR-0025: Interactive ref selection in the ref-switch modal

## Status

Proposed — completes the ref-modal migration promised by
[ADR-0020](0020-readline-style-text-editing.md) §1 and builds on
[ADR-0018](0018-additive-module-command.md) (context-aware ref switch) and
[ADR-0003](0003-gitops-loading.md) (`git ls-remote` ref discovery). Touches
`internal/tui/model.go`, `internal/tui/view.go`, and `internal/tui/editor.go`.

## Context

Switching a module ref (the `R` key, and the auto-opened modal when a wrapper
is opened on a ref the remote no longer has) puts the user in front of a
single free-text field plus a one-line hint of what refs exist:

```
New ref: ▏
Available: 2.0a1, bup, charm-tracing-workshop, chore/update-references-to-track-3-main,
cos-lite-microk8s-doc-fixes-for-main, cos-lite-model-topology (+46 more)
```

For a real repository (`canonical/observability-stack` has 50+ branches and
tags) this surface fails on two independent axes:

### 1. The hint is a dead end

`summarizeRefs` renders the first six names and `(+N more)`. The list is
**static** (typing does nothing to it), **truncated** (the six shown are
whatever order the remote returned, not what the user wants), and **wrapped
past the modal width** (long branch names like
`chore/update-references-to-track-3-main` blow the budget and get clipped).
The user's only path to the other 46 refs is to already know their exact
spelling and type it blind. There is no way to *discover* a ref from inside
the tool — the exact task the modal exists to serve.

### 2. The input is a hand-rolled byte buffer, below the floor of every shell

The field is not the canonical readline cell that [ADR-0020](0020-readline-style-text-editing.md)
standardised for value editing. It is the pre-ADR-0020 append/trim buffer:

```go
case tea.KeyBackspace:
    if len(m.refInput) > 0 {
        m.refInput = m.refInput[:len(m.refInput)-1]
    }
case tea.KeyCtrlU:
    m.refInput = ""
case tea.KeyRunes, tea.KeySpace:
    m.refInput += string(msg.Runes)
```

This is exactly the shape ADR-0020 §1 called out for elimination — and ADR-0020
explicitly scoped `refInput` into that migration ("The same migration is applied
to `refInput` in the ref-switch modal … so that the project ends up with a
single canonical text-input cell rather than two parallel byte-buffer
implementations"). That migration was never carried out. The consequences are
the same ones ADR-0020 documents for value cells, and they bite hardest here
because ref names are long and full of word boundaries (`chore/update-references-to-track-3-main`):

- **No caret.** A typo early in a 40-character branch name can only be fixed
  by backspacing the whole tail.
- **No word motion or word delete.** `Ctrl+←/→`, `Ctrl+W`, `Alt+Backspace`,
  `Alt+D`, `Alt+Delete`, and `Ctrl+Delete` — every one of which works inside a
  value cell after ADR-0020 — do nothing in the ref field. `Ctrl+U` clears the
  whole field rather than killing to start, diverging from the value-cell
  contract.
- **Not rune-safe.** The byte-trim corrupts any ref ending in a multi-byte
  rune (rare in refs, but the bug class is real and free to fix by migrating).

The two problems share a root cause — the modal predates ADR-0020 and never
adopted its cell — so they are fixed together here.

## Decision

Turn the ref-switch modal into an **incremental filter over the fetched ref
list**, backed by the canonical readline cell. Two coupled changes.

### 1. Finish the ADR-0020 migration: `refInput` becomes a `cellInput`

`Model.refInput` changes from `string` to the existing `cellInput`
(`internal/tui/editor.go`), the shared `bubbles/textinput` wrapper that every
value editor already uses. The modal's `handleRefModalKey` stops hand-editing a
byte buffer and forwards editing keys to the cell, so the ref field inherits
the entire ADR-0020 §2 keymap for free: caret motion, word jumps, `Ctrl+W` /
`Alt+Backspace` (delete word left), `Alt+D` / `Alt+Delete` (delete word right),
`Ctrl+U` (kill to start), `Ctrl+K` (kill to end), rune-safety, and native
paste. This is the single canonical text cell ADR-0020 §1 asked for; there is
no longer a second byte-buffer input in the project.

**On `Ctrl+Delete` / `Ctrl+Backspace` specifically.** ADR-0020 §2 names these
as aliases for word-delete, and they are the desktop reflex, but ADR-0020 §2
also gates them on "the terminal actually emitting a distinct key event … Bind
it only where `bubbles/textinput` surfaces it as its own key; where it
collapses, drop the alias rather than fake it." That gate is **not met** in the
current stack: the pinned `bubbletea` (`v1.3.10`) has no sequence entry for
`Ctrl+Delete` (`CSI 3;5~`) — it parses `Delete` (`CSI 3~`) and `Alt+Delete`
(`CSI 3;3~`) but routes `Ctrl+Delete` to its unknown-CSI path, so no keymap
binding can ever match it — and `Ctrl+Backspace` is folded to `Ctrl+H` by most
terminals. Binding these strings would be dead code that fakes support. We
therefore **do not** add them; the delete-word chords that *do* fire —
`Alt+Delete` / `Alt+D` (forward) and `Ctrl+W` / `Alt+Backspace` (back) — are the
supported word-delete keys, and they work in the ref field the moment the
migration lands (they are `textinput` defaults). Re-binding `Ctrl+Delete` /
`Ctrl+Backspace` becomes a follow-up gated on a `bubbletea` that surfaces them,
per ADR-0020 §2's own rule.

### 2. Incremental substring filter with a selectable list

The static `Available: …` hint is replaced by a **live, scrollable, selectable
list** of the refs already fetched via `RefSwitcher.ListRefs`
(ADR-0003 / ADR-0018 — no new network call; the same list that feeds the hint
today).

- **Match rule: case-insensitive substring, prefix-first.** A ref is shown when
  the query is a substring of the ref name, matched case-insensitively. This is
  the `vim` `/foo` reflex — narrowing by any fragment, not just the start,
  which is what makes `model` find `cos-lite-model-topology`. Results are
  ordered *prefix matches first* (a query that starts the ref is the strongest
  signal), then the remaining substring matches; within each group the remote's
  original order (ADR-0019 ordering) is preserved. Substring is chosen over a
  fuzzy/subsequence scorer because it is predictable (a user can see *why* a ref
  matched) and needs no ranking heuristics or dependency; fuzzy matching is
  listed as a deferred enhancement.

- **Presentation: a windowed selectable list.** Below the input, up to a fixed
  number of matches (8) are shown at once with a highlight cursor and a
  `shown/total` count (`▸ cos-lite-model-topology … (8/52)`), scrolling when the
  match set is taller than the window. This replaces `summarizeRefs`, which is
  removed. For 50+ refs a scrollable list is the only presentation that lets a
  user *see and pick*; a single inline ghost-completion would surface only one
  candidate at a time and hide the rest — the exact failure of today's hint.

- **Keys inside the modal:**

  | Key                         | Action                                                        |
  |-----------------------------|---------------------------------------------------------------|
  | printable / editing keys    | Sent to the ref cell (full ADR-0020 §2 keymap)                |
  | `↑` / `↓` (`Ctrl+P`/`Ctrl+N`) | Move the highlight within the filtered list                 |
  | `Tab`                       | Autocomplete: copy the highlighted match into the field       |
  | `Enter`                     | Switch to the **typed text** (free-text preserved)            |
  | `Esc`                       | Cancel the modal                                              |

  **`Enter` always commits exactly what is in the field**, never the highlighted
  list row. This is non-negotiable: the field must remain free-text so an
  arbitrary SHA or an as-yet-unpushed ref can always be entered, and so a user
  who typed a full ref is never surprised by a "helpful" substitution. Picking
  from the list is the explicit, separate gesture `Tab` — mirroring shell
  completion (`Tab` completes the token, `Enter` runs the line) and consistent
  with the readline north star ADR-0020 set. `Tab` fills the field and parks the
  caret at the end, so the user can then edit or confirm; a highlighted match is
  therefore reached in `Tab` `Enter`, and a hand-typed ref in `Enter` alone.

- **Query seeding and the "browse" gesture.** The modal still seeds the field
  with the current ref (unchanged: this is what lets an unmodified `Enter` act
  as a no-op cancel, ADR-0018, and what pre-fills the broken ref for editing on
  auto-open). While the query is empty **or still equal to the seeded current
  ref**, the list shows **all** refs (highlight defaulting to the current ref if
  present); the first edit diverges the query and filtering begins. This makes
  `R` `↓↓` `Tab` `Enter` a pure browse-and-pick flow with no typing, while a
  user who knows the name just types and filters.

Offline / loading behaviour is unchanged: while `ListRefs` is in flight the
list area shows `loading…`; when the remote is unreachable the list is empty
and the field stays free-text (the switch itself will report a precise
`RefNotFoundError` if the typed ref is bad).

## Alternatives considered

### Keep the static hint, just widen/scroll it

Rejected. Word-wrapping or paging `summarizeRefs` addresses only the truncation
symptom. The list would still be non-interactive — the user still cannot narrow
50 refs to the 2 they mean, which is the actual task.

### Use `bubbles/textinput`'s built-in suggestion feature

Rejected as the matcher. `textinput` ships `ShowSuggestions` with `Tab` to
accept and `↑/↓` to cycle — attractive because it is free. But its matcher is
**prefix-only** (`strings.HasPrefix`), which directly contradicts the `/foo`
substring reflex that motivates this ADR (`model` would not find
`cos-lite-model-topology`), and it surfaces one ghost candidate at a time rather
than a browsable list. We reuse the widget for the *input cell* (decision §1)
but drive the *match list* ourselves so the match rule and presentation are
ours to choose.

### Fuzzy / subsequence matching (fzf-style)

Deferred, not rejected. Subsequence matching with ranking is strictly more
powerful (`clm` → `cos-lite-model`), but it needs a scorer (hand-rolled or a
new dependency) and makes "why did this match?" less obvious. Substring covers
the motivating cases with zero ranking surface; fuzzy can supersede the match
rule in a later ADR without changing the modal's shape or keymap.

### `Enter` accepts the highlighted match when one is selected

Rejected. It shaves one keystroke off the pick-from-list case but makes `Enter`
context-dependent and can silently discard a fully hand-typed free-text ref in
favour of a coincidentally-highlighted row. Free-text integrity (SHAs, unpushed
refs) outweighs the saved keystroke; `Tab` is the explicit accept.

## Consequences

- **The last pre-ADR-0020 byte-buffer input is gone.** `refInput` joins the
  value cells on the shared `cellInput`; ADR-0020 §1's stated end-state ("a
  single canonical text-input cell") is finally true. Rune-safety and the full
  readline keymap now apply in the ref field, including the word-delete chords
  (`Ctrl+W`, `Alt+Backspace`, `Alt+Delete`, `Alt+D`) that previously did
  nothing there.
- **`Ctrl+Delete` / `Ctrl+Backspace` remain unsupported for now** — not from
  choice but because the pinned `bubbletea` does not surface them as distinct
  keys (see Decision §1). `Alt+Delete` / `Alt+D` are the working
  delete-word-forward chords; the alias is a follow-up gated on the framework.
- **`Ctrl+U` in the ref field changes from "clear all" to readline
  kill-to-start**, matching value cells (ADR-0020 §2). From the seeded current
  ref the caret sits at end, so `Ctrl+U` still clears the whole field in
  practice — the common gesture is unchanged, and the semantics are now
  consistent project-wide.
- **`summarizeRefs` is removed** and its test with it; the available-refs data
  path (`availableRefs`, `refsLoading`, `startListRefs`) is unchanged and now
  feeds the filter instead of the hint.
- **`Tab` inside the modal is bound to autocomplete**, not the global
  pane-toggle. This is consistent with ADR-0020 §3's scope rule ("`Tab` means
  the next thing in the current scope"): the modal is a self-contained scope,
  and there is no pane to toggle while it is open.
- **README, SPEC §7.4, and the `?` help modal gain the modal's keymap.** The
  help modal is the source of truth (ADR-0020 §5); the SPEC's ref-switch mockup
  is updated to show the filtered list.
- **No new dependency and no new network call.** The matcher is pure string
  work over the list `ListRefs` already returns.

## Out of scope

- **Fuzzy / subsequence matching and match ranking.** Deferred (see
  Alternatives); substring is the v1 rule.
- **Highlighting the matched substring within each row.** A pure-cosmetic
  enhancement; the selection highlight and count are enough for v1.
- **Remembering recent refs / a ref history.** Out of scope; the list is the
  remote's current refs only (ADR-0003 freshness guarantee).
- **Multi-line or `$EDITOR` ref entry.** Not applicable — a ref is a single
  token; the scalar cell is the right and only shape.
