# ADR-0051: `atelier apply` exits 2 when Terraform failed

## Status

Accepted — extends [ADR-0048](0048-machine-readable-output.md).

## Context

`atelier apply` is the one command whose job is to run Terraform and report how
it went. That gives it two ways to fail that mean opposite things to whoever is
running it:

- **Atelier declined.** A clone failed, the module is already declared and cannot
  be told apart from another copy, `--as` names a block belonging to another
  module, a required input has no value. Nothing was deployed.
- **Terraform failed.** The wrapper was written, then `terraform init` or
  `terraform apply` exited non-zero. The wrapper on disk is current and
  infrastructure may be partly applied.

Both exited `1`. A CI job could only tell them apart by scraping stderr for a
phrase, which is not a contract.

[ADR-0050](0050-tolerant-apply-converges.md) widened the gap: `apply` now
*refuses* in cases where it previously appended a duplicate module block, so a
deploy job acquired a new way to fail that has nothing to do with
infrastructure.

The obvious fix — a `--json` payload on `apply` — is the one ADR-0048 ruled out
for this command, and the reasoning still holds: `apply` reports Terraform's
output, so a payload would have to invent a result Atelier does not own. The
refusal, though, *is* Atelier's. So the distinction belongs in the exit code,
which is the one channel `apply` has.

## Decision

`atelier apply` exits:

| code | meaning |
| --- | --- |
| `0` | The wrapper was written and Terraform applied it. |
| `1` | Atelier's failure. Nothing was deployed: bad usage, a clone that failed, a refusal, the required-input gate, a preflight refusal. |
| `2` | Terraform ran and reported failure. The wrapper on disk is current; infrastructure may be partly applied. |

The code says **which side failed**, never why — the reason stays in the message
on stderr, where a human reads it.

An error is Terraform's when `terraform init` or `terraform apply` was actually
run and exited non-zero. Everything else is Atelier's, *including* a failure to
locate Terraform or open its log directory: nothing ran, so it is the
environment's problem rather than a deployment outcome. The classification is
carried by an `exitError` wrapper (`cmd/atelier/exit.go`) so a deep call site can
classify itself once, and unclassified errors default to `1` — if Atelier cannot
tell, the failure is its own.

**This is scoped to `apply`, and the scoping is deliberate.** Every other command
still exits `0` or `1`. `atelier import` also runs Terraform, but it ships a
`--json` payload, and ADR-0048's pattern is that the code carries pass/fail while
the payload carries the reason — a taxonomy there would duplicate a channel it
already has. Extending these codes to `import` is a separate decision, and should
be taken when something needs it rather than pre-emptively.

### One behaviour change

A failed `terraform init` or `apply` now exits `2` where it exited `1`. A script
testing `!= 0` is unaffected; one testing `== 1` for a Terraform failure will see
a difference. That is the point of the change, and Atelier is pre-1.0 with a small
installed base, so the trade is taken knowingly rather than avoided.

## Alternatives considered

- **`--json` on `apply`, carrying the outcome.** Rejected by ADR-0048 and still
  wrong: the interesting result is Terraform's, and a payload that reported only
  Atelier's half would still leave a job unable to distinguish the two failures
  without also reading the exit code.
- **A stable machine-readable prefix on stderr** (`atelier: refused:` versus
  `atelier: terraform:`). Rejected: it works, but it is string matching wearing a
  contract's clothes, and it puts a promise in prose that nothing enforces. An
  exit code cannot drift from the code that sets it.
- **Keep `1` for everything and add a fourth state for refusals** (`2` = Atelier
  refused, Terraform failures stay `1`). Rejected: it makes `1` mean two opposite
  things, and the common case — a deployment that failed — is the one a reader
  least wants to have to look up.
- **Propagate Terraform's own exit status.** Rejected: Terraform exits `1` for
  nearly everything, so it carries no more information than the number it would
  replace, and it would make the same command mean different codes for different
  failures.

## Consequences

- A deploy job can branch without parsing output: `2` means investigate the
  infrastructure, `1` means fix the wrapper or the command line.
- `2` is deliberately *not* a safe blind re-run: a partly applied deployment is
  the case where reading the plan first matters, which is the reason the two are
  worth separating.
- Only `atelier apply` is affected. `add`, `ls`, `wrappers`, `tidy`, `rm`,
  `purge`, `import` and the TUI keep exiting `0` or `1`.
- Nothing about the wrapper format changes, and no new flag is introduced.
