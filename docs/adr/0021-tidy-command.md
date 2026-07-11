# ADR-0021: `atelier tidy` — on-demand prune to sparse form

## Status

Proposed

## Context

[ADR-0007](0007-sparse-wrapper-write-rule.md) established the
sparse-plus-required rule: when Atelier writes `main.tf`, it emits required
variables always and optional variables only when they differ from their
declared default. That rule runs on the *save path* — every time the TUI
writes the wrapper.

The rule therefore only applies to wrappers authored *through* Atelier's
writer. Several common situations bypass it and leave `main.tf` cluttered
with arguments that are explicitly set to their default value:

- `atelier init` adopt ([convert](../../internal/convert)) deliberately takes
  the user's existing `module {}` block verbatim and does not rewrite it.
- A hand-authored or copy-pasted `main.tf`.
- A `main.tf` seeded from an upstream module's full example.

For a module like observability-stack's `cos-lite`, this means blocks such as
`grafana = { units = 1 }` or `catalogue = { app_name = "catalogue" }` —
values identical to the module's declared defaults — sit in the wrapper as
noise, undermining the "speak in APIs; the wrapper states intent, defaults
handle the rest" framing.

We want a way to collapse such a wrapper back to sparse form *without*
requiring a full TUI session, and to do it safely on files Atelier did not
necessarily author.

## Decision

Add **`atelier tidy [PATH] [--write]`**, a headless command that prunes a
single-module wrapper to its sparse-plus-required form.

**It does not introduce new pruning logic.** It rehydrates a `wrapper.State`
from `main.tf` plus the upstream module schema — exactly as opening the TUI
does ([`bootstrap.LoadExisting`](../../internal/bootstrap)) — then renders
`main.tf` through the same writer the save path uses
([`State.RenderMain`](../../internal/wrapper/write.go)). Preview and apply go
through one code path, so the diff `tidy` shows is precisely what it writes,
and `tidy` can never diverge from save-time pruning.

Guardrails, because `tidy` rewrites a file the user may have hand-authored:

- **Dry-run by default.** `tidy` prints a line diff and writes nothing.
  `--write` (`-w`) applies the prune.
- **Backup before write.** The pre-tidy `main.tf` is copied to
  `.atelier/backups/main.tf.<timestamp>.bak`. The prune is information-lossy
  (a value explicitly set to its default becomes indistinguishable from
  unset), so recovery must be possible.
- **Refuse, don't guess, when the schema is unavailable.** Without the
  module's `variables.tf` Atelier cannot know a variable's declared default,
  so it cannot tell a redundant value from a deliberate override. A failure
  to fetch/parse the schema is fatal.
- **Single module block only.** A `main.tf` with more than one `module {}`
  block is ambiguous (which is the wrapper's?) and outside v1's
  one-module-per-wrapper model, so `tidy` refuses rather than tidy one
  arbitrarily.
- **Warn on an unpinned ref.** Defaults are only stable if the module source
  is pinned to a commit. Against a branch (or no ref) the "default" is
  whatever upstream says today, so `tidy` emits a non-fatal advisory.

**Explicit, never automatic.** `tidy` is a separate command, not a default
behaviour. In particular, `atelier init` adopt is left non-destructive (its
documented contract): it does not auto-tidy the adopted file. Save-time
pruning remains opt-in by virtue of the user choosing to author in the TUI;
silently rewriting a file the user only asked Atelier to *adopt* would
violate that.

## Alternatives considered

### Always prune (auto-tidy on adopt / on every open)

Rejected. It contradicts [ADR-0004](0004-wrapper-layout.md)'s and convert's
non-destructive promise for adopt, and rewrites files the user did not author
through Atelier without asking. The destructive blast radius (lost
inline comments inside removed blocks, erased deliberate default-pins) is
exactly why this must be opt-in and dry-run-first.

### A new standalone pruning engine

Rejected. Two code paths that "both prune" would drift. Reusing
`State.RenderMain` guarantees `tidy` and the TUI save path are identical by
construction.

### Name it `prune`

Rejected. `atelier purge` already exists and is destructive; `prune`/`purge`
differ by one letter and would be confused. `tidy` reads clearly and does not
alias an existing verb.

## Consequences

- `tidy` is apply-equivalent: because it only removes arguments equal to the
  declared default, `terraform plan` before and after a tidy produces the
  same diff (modulo upstream default drift on an unpinned ref). This is the
  correctness invariant; a tidy that changes the plan is a bug.
- It inherits ADR-0007's accepted trade-off: "explicitly set to default" and
  "unset" collapse to the same output. A user who pinned a value *to* its
  current default to guard against upstream drift loses that pin. The backup
  and the unpinned-ref warning mitigate; the dry-run default makes it visible
  first.
- Expression-valued arguments (`var.x`, `module.y.z`, `local.z`) are stored
  as `UnknownAttrs` and re-emitted verbatim — `tidy` never prunes what it
  cannot evaluate.
- Comments on *surviving* arguments are preserved by the AST-aware writer
  ([ADR-0005](0005-implementation-language-go.md)); comments *inside a removed
  block* are removed with it. This is documented and is the reason for the
  backup.
- Running `tidy` on a wrapper with no `.atelier/` triggers the same
  auto-rehydrate (re-clone for schema, write `session.json`) that opening it
  in the TUI would.
- Extends ADR-0007's rule from "Atelier-authored, at save time" to
  "on-demand, against existing files." ADR-0007 is unchanged and not
  superseded; this ADR builds on it.
