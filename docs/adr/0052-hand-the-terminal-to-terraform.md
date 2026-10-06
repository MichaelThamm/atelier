# ADR-0052: Hand the terminal to Terraform instead of re-rendering its output

## Status

Accepted — supersedes [ADR-0029](0029-live-logs-view.md). The TUI no longer
captures Terraform's output for an in-TUI logs view: `A` applies with the
terminal released to Terraform, and plan output is only persisted to
`.atelier/logs/`.

## Context

The TUI reached Terraform through `Planner`/`Applier`/`Validator`/`RefSwitcher`
and, for the operations it drove itself, through `ProgressWriter` and
`ErrorLogWriter`. Those two writers buffered every stdout and stderr line into
a `ProgressTracker`, which the `L` key rendered as a two-tab (Errors / Logs)
scrollable browser with counts, a start time, and a log-path banner
([ADR-0029](0029-live-logs-view.md)).

That arrangement cost roughly 1100 lines of TUI source and 440 lines of tests
to re-present output Terraform had already written, and it made the alt-screen
the only place that output could be seen. `atelier apply`
([ADR-0034](0034-module-apply-one-liner.md)) does not do this: it clears its
spinner, points Terraform's streams at the process's own stdout and stderr, and
lets Terraform render its own plan and approval prompt. That is better at
showing Terraform output than any re-rendering can be, and it is now the
primary way a module is deployed.

The CLI also closed the gaps the logs view was built for. `atelier apply
<module> --dir <wrapper> [--ref REF] [--var-file NAME]` composes a wrapper and
deploys it in one re-runnable command, so the configure-and-deploy loop no
longer needs the TUI to drive Terraform at all.

Two smaller costs compounded the maintenance one. The `A` key applied the
cached plan through `terraform-exec`, which passes `-auto-approve
-input=false`, so Terraform's progress was hidden behind a spinner with no way
to see it. And `planErr`, `applyErr`, and `statusDetail` on the model were
written and never rendered — the footer showed a truncated one-line status, so
`statusDetail`'s only real consumer was its own test.

## Decision

### 1. `A` releases the terminal via `tea.ExecProcess`

`Applier` changes from `Apply(ctx) error` to `ApplyCmd() tea.Cmd`.
`TfexecPlanner.ApplyCmd` runs

```
terraform apply -auto-approve -input=false <.atelier/cache/plan.tfplan>
```

with `cmd.Dir` set to the wrapper, inside `tea.ExecProcess`, which suspends the
alt-screen so Terraform writes to the real terminal. The callback maps the exit
status to `applyResultMsg` or `applyErrorMsg`, so the existing post-apply
behaviour is unchanged: the cached plan is invalidated, `check` warnings are
cleared, and state is reloaded.

The flags precede the plan file because Terraform takes at most one positional
argument and reads a flag placed after it as a second one. Putting the plan
file first fails with `Too many command line arguments`. This is also the order
`terraform-exec`'s `DirOrPlan` produced, so the handoff matches what the
replaced code passed.

The flags are deliberately the ones the plan-file path has always passed. `A`
is pressed *after* reading the plan tree, so it is the confirmation; prompting
again in Terraform's own summary would be a second gate on a reviewed plan. An
apply failure therefore needs no in-TUI rendering — it is already on screen —
and the footer says only `apply failed`.

### 2. No in-memory capture; the log files stay

`ProgressTracker`, `LogLine`, `ProgressWriter`, `ErrorLogWriter`, and the
stdout phase parser are deleted, along with `viewMode`, `logsTabMode`,
`activeView`, `logScroll`, `logAutoScroll`, `logsTab`, `renderLogsView`, and
`handleLogsKey`. The `L` key is gone.

The persistent files under `.atelier/logs/` are unaffected: the planner and the
ref-switch `init -upgrade` path now point Terraform's streams straight at
`tf.StdoutFile()` / `tf.StderrFile()`, which is what the `FileWriter` half of
those writers already did. `.atelier/logs/tf-stderr.log` remains the durable
record for a failed plan, and `ATELIER_DEBUG`'s `tf-trace.log` is unchanged.

### 3. Failures name their cause in the status bar

`planErrorMsg` sets `plan failed: <first line of the error>`, and
`validateResultMsg` sets `validate: <first diagnostic>` — the message
[SPEC §9](../SPEC.md) already promises. `planErr`, `applyErr`, and
`statusDetail` are deleted rather than left as write-only state. The location of
`.atelier/logs/` is documented in the help modal and SPEC §7.7 instead of
being rendered in a banner.

### 4. One spinner label for every in-flight operation

`opPhase` and `opStarted` on the model replace the tracker, set by `beginOp`
from `startPlan`, `startApply`, and `startRefSwitch`. The footer's three
near-identical spinner branches collapse into one that renders
`opPhase + progressSuffix()`. `progressSuffix()` and `formatDuration()` move to
`view_panes.go` next to the footer that uses them.

### 5. What stays

The plan tree is untouched. `P` still runs `terraform plan -out` +
`terraform show -json` in-process through `Planner`, because the tree is built
from the parsed JSON and there is no other source for it. Only the output
routing changed. `terraform validate`, the variable editors, ref switching, and
preset handling are unaffected.

## Alternatives considered

**Keep the logs view and add the terminal handoff alongside it.** Rejected: it
keeps both a re-rendered copy and the real thing, and the user has to learn
which one to read. The files already answer the post-mortem question.

**Delete the log files too, and rely on Terraform's own output.** Rejected: a
plan the TUI runs on the user's behalf writes to no visible terminal, so
without the files a failed plan leaves nothing to read. They cost nothing —
`tfexec.configureLogging` already opens the handles.

**Keep `Apply(ctx) error` and render its output into the status bar.** Rejected:
it is the ADR-0029 arrangement with a smaller font. `tea.ExecProcess` already
solves the alt-screen problem.

**Move `snapshotValues` out of the TUI now.** Deferred. If `atelier presets
save` is added it belongs in `internal/wrapper` alongside `ShouldEmit` and
`SparseValue`, and both callers should share it; that is its own change.

## Consequences

- The TUI stops being a Terraform driver for apply: `internal/tui` loses about
  1100 lines of source and 440 of tests, and `progress.go` and `view_logs.go`
  are gone.
- `A` shows Terraform's real apply output, including its per-resource progress
  and any provider prompts.
- A failed plan leaves its diagnostics in `.atelier/logs/tf-stderr.log`, named
  in the help modal and SPEC §7.7 rather than in a view.
- The footer shows one cause line instead of a pointer to a browser that no
  longer exists, which is what SPEC §9 always described.
- `Applier` implementations must return a `tea.Cmd`; tests stub it with a
  closure that yields the message, so a test never spawns Terraform.
- SPEC §7.5, §7.7, §9, and §13.3, the README keyboard table, the `?` help
  modal, and `internal/tui/AGENTS.md` are updated.
