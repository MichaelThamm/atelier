# ADR-0029: Live logs view (`L`)

## Status

Accepted — adds a new view mode to the TUI. Touches `internal/tui` (model,
view, progress, theme). Amends [ADR-0014](0014-unified-layout-budget.md)
(height derivation and scroll support tables).

## Context

Terraform stdout during plan, apply, and ref-switch is parsed by
`ProgressWriter` into a single "phase" string displayed inline in the footer
alongside a spinner. This gives a high-level breadcrumb ("Initializing
providers…") but loses the full output: warnings, deprecation notices,
provider version constraints, and resource-level progress are all discarded
after being parsed.

Users debugging unexpected behaviour (a plan that looks wrong, a provider
warning, a slow apply) have no way to see the raw terraform output without
re-running with `ATELIER_DEBUG=1` and tailing `.atelier/logs/tf-trace.log`.

The footer's phase string also truncates at 50 characters, producing noise
on wider terminals rather than useful information.

## Decision

### 1. Log buffer in `ProgressTracker`

Add a `lines []string` field to `ProgressTracker` that accumulates every
non-empty terraform stdout line. The existing `ProgressWriter.Write()` calls
`AppendLine()` for each line in addition to the existing `SetPhase()` call.
Lines are thread-safe (`sync.Mutex`) and persist after the operation
completes — `m.progress` is no longer set to nil on plan/apply/ref-switch
completion. The next operation creates a fresh `ProgressTracker` via
`NewProgressTracker()`, replacing the old one.

### 2. `viewMode` enum and `viewLogs` state

Add a `viewMode` enum (`viewEditor`, `viewLogs`) to the Model with an
`activeView` field. A new `logScroll` field tracks the scroll offset in the
logs view.

### 3. `L` key binding

- **Editor mode:** `L` enters logs view when `m.progress != nil && len(m.progress.Lines()) > 0`.
- **Plan view:** `L` enters logs view (capital `L` only; lowercase `l` is tree collapse).
- **Plan loading:** `L` enters logs view (both cases).
- **Logs view:** `L`/`Tab`/`Esc` returns to the previous view (plan view if
  `planState == planReady`, editor otherwise). `Esc` during plan loading
  cancels the plan and returns to the editor.

### 4. `renderLogsView()`

Renders a single bordered panel (`stylePanelFocused`) using the same
`panelHeight()` + `clampToLines()` pattern as the left/right editor panes
(see [ADR-0014](0014-unified-layout-budget.md)). Content is clamped to
`panelHeight()` lines. Empty output shows "Waiting for terraform output…".

### 5. View() cascade priority

The `activeView == viewLogs` check sits between `warnDetail` and
`planState == planReady` in the View() cascade, so the logs view takes
priority over the plan tree when active.

### 6. Simplified footer during loading

`progressSuffix()` now returns only the elapsed time `(12s)` without the
truncated phase string. The full output is available via `L` instead of
being squeezed into the footer.

### 7. Responsive footer

When `m.height < 15`, the footer collapses to `[?] help` only. The
`contentHeight()` budget (`height − 7`) is unchanged; the footer still
occupies 3 lines but with minimal content.

### 8. `handleLogsKey()`

A dedicated key handler for the logs view manages scroll navigation
(`↑↓/j/k`, `PgUp/PgDn`, `g/G`) and view dismissal. A nil guard on
`m.progress` returns to the editor if the tracker is cleared while in
logs view.

## Consequences

- Users can review full terraform output post-mortem without re-running.
- The footer stays clean during loading (no truncated phase noise).
- The `[L] logs` hint appears in the footer whenever logs exist, including
  after plan/apply completes.
- The help modal (`?`) documents `L` in both the plan-view and editor-mode
  sections, plus a "Logs view" scroll-key reference.
- SPEC §7.3, §7.5, §7.7; README keyboard shortcuts table; ADR-0014
  height/scroll tables; and ROADMAP are updated.
