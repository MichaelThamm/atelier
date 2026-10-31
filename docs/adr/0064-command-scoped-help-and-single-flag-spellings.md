# ADR-0064: Command-scoped help and one spelling per flag

## Status

Accepted.

## Context

Atelier's CLI diverged from the Canonical CLI standard in five ways that cost
discovery and predictability. The CLI is the first surface a new user or a CI
job meets, and the standard exists so Canonical CLIs behave alike.

- `help` was unreachable (`atelier help` and `atelier help <command>` were
  rejected as unknown commands), and `--help` on a command printed the whole
  top-level usage rather than that command's usage, options, and examples. The
  top-level help itself had no `Global options` section and did not point at
  command-level help.
- `--var-file` accepted several values both by repetition **and** by
  comma-splitting under a singular name, which the standard forbids.
- Short and long flags were offered for the same action (`-y`/`--yes`,
  `-f`/`--force`, `-v`/`--version`), and `rm` and `purge` collapsed `--yes` and
  `--force` into one behavior with two names.
- Flag names were inconsistent across commands: `import --list` (list the
  provider's importable resource types) sat beside `import --list-var-files`
  (list `.tfvars` bundles) — near-identical names with unrelated meanings.
- `ls` and `wrappers` always printed a header row with no way to hide it;
  empty-state messages went to stdout; and some error copy used "failed to" and
  "couldn't" instead of "cannot".

## Decision

Adopt the standard's conventions across the CLI:

- `atelier help [command]` is the documented help entrypoint. `-h` and
  `--help` on any command print **that command's** help — its usage, its own
  options, and one example — not the top-level usage. The top-level help gains a
  `Global options` section and a pointer to command-level help.
- A flag that takes several values supports repetition **or** a comma-separated
  list, never both. `--var-file` keeps its singular name and repetition;
  comma-splitting is removed.
- One spelling per action. `--yes`, `--force`, and `--version` are the only
  spellings; `-y`, `-f`, and `-v` are removed. `-h` is retained because the
  standard requires the short help form.
- `import --list` is renamed `--list-types`. `--types` was rejected because it
  would collide with the existing `--type <T>` query filter.
- Table output is brace-hideable with `--no-headers` (`ls`, `wrappers`).
  Empty-state messages go to stderr while the command still exits 0, so a
  script can tell "none found" from a failure. Error copy uses "cannot".

## Alternatives considered

- **Adopt a shared CLI framework (e.g. `cobra`).** Rejected: a large dependency
  and rewrite for behavior the standard does not require; the existing
  hand-rolled parser is small enough to conform incrementally.
- **Leave the CLI as-is and treat the standard as advisory.** Rejected: the
  mismatches are exactly what the standard exists to prevent.
- **Keep the short flags and document them in help.** Rejected: dual spellings
  for one action are what the standard forbids, and `-v` is ambiguous with
  `import --verbose`.
- **Rename `import --list` to `--types`.** Rejected: it collides with
  `--type <T>`. `--list-types` reads against the existing `--list-var-files`
  pattern and does not.

## Consequences

- A **breaking change** for scripts that use `-y`, `-f`, `-v`,
  `import --list`, or a comma-separated `--var-file`; the fix is mechanical.
- Help is discoverable per command, so `atelier <command> --help` is now the
  single place to look up a command's flags.
- SPEC §6, the README, the usage text, and the help tests move to the
  per-command text and the single flag spellings.
- [ADR-0038](0038-flat-cli-surface.md) is unaffected: the flat command surface
  and its aliases (`rm`/`remove`, `ls`/`list`) stay.
