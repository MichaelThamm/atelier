# ADR-0014: Unified layout budget and scroll support

## Status

Accepted — supersedes height-budget aspects of [ADR-0006](0006-two-pane-ui-layout.md) and [ADR-0011](0011-plan-output-tree.md).

## Context

The TUI has four primary screens a user can reach: the variable editor
(two-pane), the plan/state view, the output modal, and the error detail
modal. Each screen computed its own available height independently using
ad-hoc constants (`bodyHeight()`, `planBodyHeight()`, modal frame math).

This caused two classes of defects:

1. **Border/banner truncation.** The plan screen rendered
   `header (3) + summary (1) + panels (height−7+2) + footer (3)` =
   `height + 2`, overflowing the terminal by 2 lines. The bottom border of
   the footer was clipped on every plan invocation.

2. **No scroll support in bounded panes.** The plan tree, plan diff, and
   right-pane editor rendered all content at once; content exceeding the
   panel height was silently truncated by lipgloss's `Height()` constraint
   with no way for the user to reach off-screen items.

## Decision

### 1. Single source of truth: `contentHeight()`

Introduce one canonical method that every screen uses to compute the vertical
space available between the bordered header and bordered footer:

```go
func (m *Model) contentHeight() int {
    // header (3) + footer (3) + 1 safety line = 7
    return m.height - 7
}
```

All per-screen height helpers derive from it:

| Screen | Panels inner height | Derivation |
|--------|---------------------|------------|
| Editor (two-pane) | `contentHeight() − 2` | Subtract panel borders (top+bottom). |
| Plan | `contentHeight() − 3` | Subtract summary line (1) + panel borders (2). |
| Logs view | `contentHeight() − 2` | Subtract panel borders (top+bottom). |
| Output modal | `contentHeight()` (no additional border math needed; handled by `renderModalFrame`). |
| Error modal | Same as output. |

### 2. ANSI-aware word-wrapping

Content rendered inside bounded panels must not exceed the panel's inner
width. A line that is visually wider than the panel causes the terminal to
wrap it into an extra visual line — an extra line that the height budget
never accounted for, causing the bottom chrome (or top, depending on the
terminal's scroll behaviour) to be clipped.

All content-bearing panes word-wrap their text to `innerWidth` using
`charmbracelet/x/ansi.Wordwrap`, which preserves ANSI escape sequences and
handles wide characters. This is applied:

- **Right pane (editor):** `rightWidth − 2` (panel padding).
- **Plan diff pane:** `rightWidth − 2`.
- **Modal frame body:** `innerW` (terminal width − border − padding).

This ensures that regardless of how long a variable description, resource
address, or error message is, the panel's line count remains predictable
and the height budget holds.

### 3. Scroll support for all bounded panes


Every pane that can overflow its allocated height now supports viewport
scrolling:

| Pane | Scroll field | Navigation keys | Follow-cursor |
|------|-------------|-----------------|---------------|
| Left variable list | `leftScroll` (existing) | ↑↓, PgUp/PgDn, g/G | Yes |
| Right editor | `editorScroll` (new) | Automatic via `EditorWithCursor` | Yes |
| Plan tree | `planScroll` (new) | ↑↓, PgUp/PgDn, g/G | Yes |
| Plan diff | `planDiffScroll` (new) | `[` / `]` keys | Manual |
| Logs view | `logScroll` (new) | ↑↓/j/k, PgUp/PgDn, g/G | Manual |
| Output view | `outputScroll` (existing) | j/k, PgUp/PgDn, g/G | Manual |

The right-pane editor uses a new optional interface `EditorWithCursor`:
```go
type EditorWithCursor interface {
    Editor
    CursorLine() int
}
```
Editors that implement it (objectEditor, mapEditor, mapObjectEditor) have
their cursor tracked automatically — the scroll window adjusts to keep the
cursor line visible without manual intervention.

### 4. Scroll indicators

When content overflows a pane, a percentage indicator is rendered so the user
knows they are viewing a subset and roughly where in the content they are.

## Alternatives considered

### Per-screen `max(height−N, 1)` constants (status quo)

Rejected. Fragile: every new chrome element (summary line, breadcrumb, etc.)
requires auditing all height formulas independently. The plan screen overflow
bug was a direct consequence of this approach.

### Bubble Tea viewport component

The Bubble Tea ecosystem has a `viewport` component. Considered for the plan
tree and diff panes. Rejected because:
- Viewport wraps content in its own model/update cycle, adding message-passing
  overhead for panes where the parent model already owns the cursor.
- The simple slice-based scroll (`lines[scroll:scroll+visible]`) achieves the
  same result with less abstraction for our use case.
- We only need vertical scroll; viewport's horizontal scroll, mouse support,
  and header/footer slots are unnecessary baggage.

### Splitting panes into independent Bubble Tea sub-programs

Rejected. Composability via sub-programs adds routing complexity for what is
fundamentally a layout constraint problem. The existing flat model with
focus-routing is adequate.

## Consequences

- The plan screen no longer overflows the terminal regardless of height.
- All four screens (editor, plan, output, error) display consistent borders:
  header at the top, footer at the bottom, content between them — always
  exactly filling `m.height − 1` lines (the 1-line safety margin for
  terminals that report height inclusive of the cursor row).
- Large plan trees (100+ resources), large object editors (20+ fields), and
  long attribute diffs are all navigable via keyboard scrolling.
- New chrome elements only need to subtract their line count from
  `contentHeight()` in their screen's panel-height helper — no other file
  needs adjustment.
- ADR-0006 and ADR-0011 remain valid for their layout-topology decisions
  (two-pane, tree-on-left, diff-on-right); this ADR supersedes only their
  implicit height-budget assumptions.
