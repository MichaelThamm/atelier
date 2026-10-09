# ADR-0056: Terraform action logs name the action and are bounded

## Status

Accepted — refines the durable-log format that
[ADR-0029](0029-live-logs-view.md) introduced and
[ADR-0052](0052-hand-the-terminal-to-terraform.md) kept after the in-TUI view
was removed.

## Context

`.atelier/logs/tf-stdout.log` and `tf-stderr.log` are the only durable record of
what terraform did, and a run usually contains several actions (`init`,
`init -upgrade`, `plan`, `apply`). They were hard to read as soon as more than
one action appeared:

- The header was anonymous: `=== action started at 2026-10-08 14:47:02 ===` did
  not say which action produced the block after it.
- An action that wrote nothing still got a header, so two headers back to back
  with nothing between them read as a mistake.
- The stdout and stderr halves of one action were delimited independently, with
  no shared text to pair them.
- The CLI `apply` path mirrored terraform's stderr, ANSI color escapes
  included, so the file was noisy to grep. The failed-plan status line points
  readers at `tf-stderr.log`, so it is read far more often than before.

## Decision

### 1. The header names the command, the wrapper, and the time

Each block is headed by

```
=== 2026-10-08T14:47:02-04:00 terraform apply (/home/me/proj) ===
```

The resolved binary (`terraform` or `tofu`), the subcommand and its relevant
flags, the wrapper directory, and an RFC3339 timestamp are all in the line, so
`init -upgrade`, `plan`, and `apply` are distinguishable at a glance. The
timestamp is generated once per action and used in both files, so their blocks
share the same header and pair up.

### 2. The header is lazy; an end marker bounds the block

The header is written the first time the action actually writes to a file, and
a `... finished ===` line closes a block that has one. A file that received no
output gets neither, so an empty action cannot leave an orphan header. The
split between stdout and stderr is kept: it is what tells a reader that errors
are on stderr.

### 3. The logs are plain text

ANSI escapes are stripped as the output is appended, while a terminal that the
CLI `apply` mirrors to still receives the original bytes. Stripping on write is
robust to terraform deciding to colorize when it has a terminal, without
threading `-no-color` through every command.

### 4. Append-only and per-file are unchanged

Both files are still opened `O_APPEND`, never truncated, so a record survives
across sessions. `tf-trace.log` and the `ATELIER_DEBUG` behavior are untouched.

## Alternatives considered

- **Emit an explicit `(no output)` line.** Rejected: a lazy header conveys the
  same information with no extra line, and an action that writes nothing stays
  a zero-byte non-block rather than an empty one.
- **Structured JSON lines.** More precise and machine-readable, but much less
  pleasant to read by eye and a larger change; worth revisiting only if
  something ever consumes the logs programmatically.
- **One combined log instead of splitting stdout/stderr.** Rejected: the split
  is what tells a reader that errors are on stderr; a combined file loses it.
- **Keep the anonymous header and rely on ordering and timestamps.** Rejected:
  ordering does not identify the action, and independently written timestamps
  do not pair the two files.

## Consequences

- `internal/tfexec` gains `ActionLog` (`BeginAction`, `Stdout`, `Stderr`,
  `Close`) and drops `WriteTimestampHeader`, `StderrFile`, `StdoutFile`,
  `MirrorStdout`, and `MirrorStderr`; the planner, the CLI ref switch, and the
  CLI apply path begin an action instead of writing a header by hand.
- SPEC §7.7 and the README describe the block format.
- A reader grepping `tf-stderr.log` sees a named, bounded, ANSI-free block per
  action; a failed plan's diagnostics are attributable to the action that
  produced them.
