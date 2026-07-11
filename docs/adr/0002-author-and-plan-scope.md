# ADR-0002: Author + plan + apply scope

## Status

Accepted (updated: in-TUI apply added after the initial design)

## Context

Once Atelier produces a wrapper directory, the next decision is *how far down
the Terraform lifecycle Atelier goes*. The lifecycle is:

```
init → validate → plan → apply → (state ops, destroy)
```

Three plausible scopes:

- **(A) Author only.** Atelier writes the wrapper and exits. User invokes
  `terraform plan`, `apply`, etc. in their own shell.
- **(B) Author + plan.** Atelier writes the wrapper *and* invokes `terraform
  plan` from inside the TUI, rendering results inline. User exits the TUI and
  runs `apply` themselves.
- **(C) Full lifecycle.** Atelier additionally invokes `apply` (and possibly
  `destroy`, state operations) from inside the TUI.

A secondary axis exists within (B): is `plan` triggered manually (user
presses a key) or automatically (debounced on edit)?

## Decision

Atelier implements **scope B+C with manual plan and apply invocation**.
The user presses `P` to run `terraform plan`; once a plan is ready, pressing
`A` runs `terraform apply` using the cached plan file. Atelier does not
auto-replan on edit.

## Alternatives considered

### Author only (A)

The minimum useful tool. Rejected because it loses the highest-value moment
in the user's loop: seeing the plan diff *while the variables are still warm
in your hands*. Without an in-TUI plan, the user has to leave the TUI, run
plan, mentally correlate the diff to the variables they just edited, then
return. That round-trip kills the "configure visually then iterate" pitch.

### Full lifecycle (C)

`terraform apply` is long-running, has interesting failure modes (provider
auth, partial apply, state lock contention), needs log streaming, and
crucially overlaps with workflows users already have (CI, OpenTofu Cloud,
Atlantis, manual approval gates). To do `apply` excellently is a significant
project on its own; to do it badly is worse than not doing it.

If Atelier did `apply`, it would also have to handle: state file location and
backends, lock acquisition and contention, partial-apply recovery, log
streaming and cancellation, dry-run vs. real-run UX. All of this is real
implementation work that does not contribute to the "configure visually"
value proposition.

May be revisited later; the wrapper format does not need to change to
accommodate it.

### Automatic plan-on-edit

Considered seriously. Rejected for v1 because:

- Plan invokes provider RPCs (data source reads, refresh). For modules like
  COS Lite with 7 `data.juju_charm.*_info` data sources, every plan hits
  Charmhub. Autoplan-on-keystroke means hitting Charmhub on every keystroke.
- Plan latency is highly module-dependent. A 200ms plan is great; a 5s plan
  with autoplan feels broken. Detecting "is autoplan OK on this module" is
  doable (measure the first plan; enable autoplan if under threshold) but
  adds complexity v1 does not need.
- Manual plan is honest about cost. "Press P to plan" sets a clear mental
  model and gives the user control over when expensive operations happen.

Adaptive autoplan is not yet implemented; see [ROADMAP](../ROADMAP.md).

## Consequences

- The TUI has a `P` key binding for plan and an `A` key binding for apply
  (available only from the plan view after a successful plan).
- Plan results are rendered inline in the TUI (see [ADR-0011](0011-plan-output-tree.md)).
- Apply uses the cached plan file (`.atelier/cache/plan.tfplan`), ensuring
  the user applies exactly what they reviewed. After a successful apply the
  plan is invalidated.
- Atelier must invoke `terraform` as a subprocess. It uses the
  [`terraform-exec`](https://github.com/hashicorp/terraform-exec) library;
  see [ADR-0005](0005-implementation-language-go.md).
- Pre-launch failures (missing `terraform` binary, version too old, network
  failure during git clone) error out at the CLI before the TUI launches.
  In-session failures (plan, apply, or validate errors) are surfaced in the
  status pane and do not block the user.
