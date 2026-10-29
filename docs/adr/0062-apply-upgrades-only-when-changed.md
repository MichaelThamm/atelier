# ADR-0062: `atelier apply` runs `init -upgrade` only when the wrapper changed

## Status

Accepted. Narrows the init step of [ADR-0034](0034-module-apply-one-liner.md) and
extends the converge contract of [ADR-0050](0050-tolerant-apply-converges.md).

## Context

`atelier apply` scaffolds or composes a wrapper, then runs `terraform init` and
`terraform apply` ([ADR-0034](0034-module-apply-one-liner.md)). It has always run
`init -upgrade`.

The flag is load-bearing in two cases:

- **A module is composed into an initialised root.** Appending a block widens the
  root's provider constraints, and a plain init then refuses the lock file's
  selection — *"must use terraform init -upgrade to allow selection of new
  versions"*.
- **An existing block is re-pointed.** `--ref track/3.0` → `track/3.1` is a
  cross-track upgrade; the new revision may require a newer provider than the
  lock file selected.

But the flag is unconditional, so a re-deploy at the same ref re-resolves module
and provider versions every time. On a bandwidth-constrained host that means
re-fetching module trees and provider archives that were already selected, when
the lock file exists precisely so a converged project need not.

Two behaviours of the git-sourced modules Atelier composes were measured before
deciding:

| change | plain `init` | `init -upgrade` |
| --- | --- | --- |
| the `?ref=` in a module's `source` changes | re-fetches the module | re-fetches |
| the ref is unchanged but the branch advanced | keeps the resolved commit | re-fetches to the new commit |

So `-upgrade` is needed neither to follow a changed ref nor by definition to
converge — only for widened provider constraints and for deliberately chasing a
moved branch.

## Decision

`atelier apply` runs a **plain `terraform init`** unless the compose changed the
wrapper's module declarations — a module was **appended**, or an existing
block's **source** (repository, sub-path, or ref) was **re-pointed** — in which
case it runs **`terraform init -upgrade`**.

- The signal comes from what Atelier itself wrote: `wrapperDeclarationsChanged`
  compares the wrapper's module blocks before and after the compose. It is not
  inferred from Terraform's error text.
- If a plain init is **refused** — the lock file no longer satisfies the
  configuration — Atelier **retries once with `-upgrade`** and says so. The
  inconsistency is reconciled without the caller having to predict it.
- A **fresh** wrapper (no lock file) uses a plain init: there is nothing to
  reconcile, and plain init already selects the newest allowed versions.
- The **TUI is unchanged**: its `ResetInit` already marks a ref switch to run
  `init -upgrade` next and uses a plain init otherwise.

## Alternatives considered

- **Keep unconditional `-upgrade`.** Rejected: it re-resolves on every run, which
  is the cost a constrained host cannot absorb, and makes `atelier apply` do
  more work than `terraform init` on a converged wrapper.
- **Parse `.terraform.lock.hcl` against the requirements up front** to decide.
  Rejected: it duplicates Terraform's selection logic and is easy to get subtly
  wrong. The retry lets Terraform be the oracle.
- **Match Terraform's `-upgrade` advisory in the error string** to choose the
  retry. Rejected as fragile. The retry does not need to name the cause: a
  refused plain init is retried once regardless, so no text matching is needed.
- **Thread the change signal out of `composeModuleBlock`.** Deferred: re-reading
  through the shared `wrapper.ReadModuleBlocks` is a couple of file reads and
  keeps the decision in one testable helper, where a signature change across the
  compose's many return sites was not worth it.

## Consequences

- A re-run at the same ref converges cheaper — no re-resolution, and no re-fetch
  on a constrained link — matching the converge contract of
  [ADR-0050](0050-tolerant-apply-converges.md).
- **Behaviour change:** a same-ref re-run no longer picks up new commits on a
  moved branch. A user who wants the newest commit changes the ref, or removes
  `.terraform/modules`; a future `--upgrade` flag could expose the choice
  explicitly.
- A plain init that Terraform refuses costs one extra init invocation (the
  retry).
- Nothing about the wrapper format changes, and no new flag is introduced.
