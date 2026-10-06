# ADR-0029: Live logs view (`L`)

## Status

Superseded by [ADR-0052](0052-hand-the-terminal-to-terraform.md) — the in-TUI
logs view is removed and `A` hands the terminal to Terraform for apply. The
persistent log files decided here are kept and remain the record for a failed
plan.

This ADR was originally Accepted (amended): it added a unified logs view mode
to the TUI with tabbed stderr/stdout, wall-clock timestamps, and persistent log
files, touching `internal/tui` (model, view, progress, theme) and
`internal/tfexec`, and amending
[ADR-0014](0014-unified-layout-budget.md) (height derivation and scroll
support tables). It is kept unedited below.

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

Additionally, the old `[E]` error detail modal duplicated information already
available in the logs. Merging errors into the logs view under a dedicated
Errors tab provides a single, discoverable location for all terraform output.

## Decision

### 1. Log buffer in `ProgressTracker`

Add a `LogLine` struct with `Content`, `Timestamp`, and `IsStderr` fields.
The `lines []LogLine` field in `ProgressTracker` accumulates every non-empty
terraform stdout and stderr line. `ProgressWriter` calls `AppendLine()` for
stdout; `ErrorLogWriter` calls `AppendStderrLine()` for stderr.
Lines are thread-safe (`sync.Mutex`) and persist after the operation
completes — `m.progress` is no longer set to nil on plan/apply/ref-switch
completion. The next operation creates a fresh `ProgressTracker` via
`NewProgressTracker()`, replacing the old one.

### 2. `viewMode` enum and `viewLogs` state

Add a `viewMode` enum (`viewEditor`, `viewLogs`) to the Model with an
`activeView` field. A new `logScroll` field tracks the scroll offset in the
logs view.

### 3. `logsTabMode` enum

Add a `logsTabMode` enum (`logsTabErrors`, `logsTabLogs`) to the Model with
a `logsTab` field. The Errors tab (stderr) is the default since errors are
the primary use case.

### 4. `L` key binding

- **Editor mode:** `L` enters logs view when `m.progress != nil` and at
  least one stderr or stdout line exists.
- **Plan view:** `L` enters logs view (capital `L` only; lowercase `l` is tree collapse).
- **Plan loading:** `L` enters logs view (both cases).
- **Logs view:** `L`/`Esc` returns to the previous view (plan view if
  `planState == planReady`, editor otherwise). `Esc` during plan loading
  cancels the plan and returns to the editor.

### 5. Tab switching (`Tab`)

In the logs view, `Tab` toggles between `logsTabErrors` and `logsTabLogs`.
Scroll resets to the bottom on switch.

### 6. `renderLogsView()`

Renders a bordered panel with a tab bar at the top showing counts and the
action start time:
```
Errors (3)  Logs (15)  (started 14:32:05)
```
The active tab uses `styleTabActive` (bold, primary colour). Content is
clamped to `panelHeight()` lines. Empty tabs show descriptive messages.

### 7. View() cascade priority

The `activeView == viewLogs` check sits between `warnDetail` and
`planState == planReady` in the View() cascade, so the logs view takes
priority over the plan tree when active.

### 8. Simplified footer during loading

`progressSuffix()` now returns only the elapsed time `(12s)` without the
truncated phase string. The full output is available via `L` instead of
being squeezed into the footer.

### 9. Responsive footer

When `m.height < 15`, the footer collapses to `[?] help` only. The
`contentHeight()` budget (`height − 7`) is unchanged; the footer still
occupies 3 lines but with minimal content.

### 10. `handleLogsKey()`

A dedicated key handler for the logs view manages scroll navigation
(`↑↓/j/k`, `PgUp/PgDn`, `g/G`), tab switching (`Tab`), and view dismissal.
A nil guard on `m.progress` returns to the editor if the tracker is cleared
while in logs view.

### 11. `ErrorLogWriter`

A new `io.Writer` that captures stderr lines into the ProgressTracker with
`IsStderr=true`. Accepts an optional `FileWriter` to tee stderr to both the
TUI tracker and the persistent log file (`.atelier/logs/tf-stderr.log`).

### 12. Persistent log files

Terraform stderr is written to `.atelier/logs/tf-stderr.log`. Each action
(plan, apply) appends a timestamp header (`=== action started at HH:MM:SS ===`)
followed by the raw stderr output. The `Terraform.StderrFile()` method
exposes the file handle so the planner can pass it to `ErrorLogWriter`.

### 13. `[E]` error detail modal removed

The old `[E]` keybinding, `errorDetail` bool field, and `renderErrorDetail()`
function are removed. Errors are now shown in the Errors tab of the unified
logs view, which is more discoverable and consistent.

## Amendment: stdout log file and log path banner

The decision above persisted only stderr (§12) and never told the user *where*
any of it went. A user who suspected an intermittent failure had no in-TUI
pointer to `.atelier/logs/`; they had to read the README or already know the
wrapper layout. And the Logs tab showed stdout that had no on-disk counterpart.

Two changes close that gap:

1. **stdout is now persisted too.** `ProgressWriter` gains the same optional
   `FileWriter` as `ErrorLogWriter`, writing terraform's stdout to
   `tf-stdout.log` (always on, appended, timestamped per action) alongside the
   in-memory tracker. §12's "stderr only" is extended, not reversed: stderr and
   stdout now have symmetric durable files.

2. **The logs view names where the files are.** A one-line banner under the tab
   bar shows the wrapper's diagnostics directory with its **absolute** path and
   the files that actually exist:

   ```
   Files: /home/me/proj/.atelier/logs/  (tf-stderr.log · tf-stdout.log)
   ```

   `tfexec.LogDirPath` resolves the directory against the process CWD, so the
   path is openable outside the TUI. Only files present on disk are named, so
   `tf-trace.log` appears only when `ATELIER_DEBUG` was set. On a narrow
   terminal the path middle-truncates (root and tail survive) before the
   filename list is dropped, so the filenames survive the longest. On a panel
   too short to fit the tab bar, the banner, and one content line, the banner is
   omitted.

3. **init output is delimited like plan and apply.** §12 said "(plan, apply)",
   but `init` also writes to both files; it previously did so without a header,
   leaving the first block of a session's log undelimited. `EnsureInit` and the
   ref-switch `init -upgrade` path now write the same timestamp header first,
   via the shared `tfexec.WriteTimestampHeader`.

This refines §6 and §12 rather than reversing them: §12 gains stdout, and the
banner points at the files those sections write.

## Consequences

- Users can review full terraform output post-mortem without re-running.
- Errors are shown by default in the logs view (Errors tab), making them
  more discoverable than the old `[E]` modal.
- The footer stays clean during loading (no truncated phase noise).
- The `[L] logs` hint appears in the footer whenever logs exist, including
  after plan/apply completes.
- The help modal (`?`) documents `L` in both the plan-view and editor-mode
  sections, plus a "Logs view" scroll-key reference.
- Persistent log files provide a durable record of errors across sessions.
- SPEC §7.3, §7.5, §7.7; README keyboard shortcuts table; ADR-0014
  height/scroll tables; and ROADMAP are updated.
