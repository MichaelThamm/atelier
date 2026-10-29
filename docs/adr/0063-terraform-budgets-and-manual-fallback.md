# ADR-0063: Per-phase Terraform budgets and naming the manual fallback

## Status

Accepted. Complements [ADR-0062](0062-apply-upgrades-only-when-changed.md),
which reduces how often `init` re-fetches; this decides how long Atelier waits
for it and what it says when the run does not complete.

## Context

On a bandwidth-constrained host, `terraform init` is the slowest thing Atelier
does: a cold cache downloads every module and provider. For COS Lite the `juju`
provider zip alone is 22.2 MB, and a plain cold init exceeded five minutes in a
slow environment. Two defects turned that into a cliff:

- **The TUI bounded a cold init by validate's 30-second budget.** The debounced
  `terraform validate` ran `init` first (`TfexecPlanner.Validate` →
  `EnsureInit`) under a 30-second context. A cold init could not finish in that
  window, so it was killed mid-fetch — reproduced: `Validate` returned at
  30.047s with `terraform init: exit status 1`, before one provider finished.
- **A failed validate was silently swallowed.** `update.go`'s
  `validateErrorMsg` branch cleared the output and wrote no status, so the user
  saw a spinner clear with no explanation.

A third gap made any failure a dead end. The wrapper is an ordinary Terraform
project needing no Atelier ([ADR-0001](0001-wrapper-as-durable-artifact.md)),
and the README says so, but nothing at the failure surface did — so the way
forward (`cd` into the wrapper and run Terraform) was discovered by accident.

## Decision

### 1. Operation budgets are per phase

`init`, `plan`, and `validate` each get a budget scaled to what they do, defined
in one place:

| phase | budget | why |
| --- | --- | --- |
| `init` | 15 minutes | network-scale: fetches every module and provider |
| `plan` | 5 minutes | local work once init is done, but can refresh large state |
| `validate` | 30 seconds | local and quick by design |

`init` is run **before** plan and validate, on its own budget, so validate's
short one never bounds a cold fetch. A ref switch re-clones and runs
`init -upgrade`, so it shares init's budget.

### 2. A validate that cannot run is surfaced

`validateErrorMsg` sets the status bar (`validate failed: <first line>`) instead
of clearing the output and saying nothing. Validate stays non-blocking and its
result is still superseded by a newer edit.

### 3. A failure names the manual fallback

A failed plan (TUI) and a failed `init`/`apply` (CLI) name the command that runs
the wrapper by hand:

```
cd <wrapper> && terraform init && terraform plan   # or … apply
```

One helper, `tfexec.ManualRunHint`, produces it for both adapters, so the CLI
and the TUI cannot drift.

## Alternatives considered

- **Raise validate's budget to cover init.** Rejected: validate should stay
  quick feedback, and its result is discarded when an edit supersedes it, so a
  long init under it is wasted either way. Splitting keeps each phase's budget
  honest about what it bounds.
- **Make the budgets unbounded.** Rejected: a genuinely hung fetch must stay
  killable, and the interrupt contract depends on the context ending it.
- **Make the budgets configurable now.** Deferred: fixed, generous defaults fix
  the reproduced failure with less surface. A flag or environment override is a
  separate decision.
- **Duplicate the fallback line in each adapter.** Rejected: it is the same
  string in two packages, which the reuse rule forbids; one helper has one home.

## Consequences

- A cold init on a constrained host completes instead of dying at 30 seconds,
  and the plan no longer has its own budget eaten by an un-finishable init.
- A failed validate is visible, so "the spinner cleared and nothing happened"
  cannot recur.
- A failure names the wrapper and the command to continue, making the fallback
  deliberate rather than accidental.
- The budgets are fixed. An environment slower than 15 minutes for one init
  still fails; configuration is future work, as is retrying a transient fetch
  failure rather than surfacing it.
