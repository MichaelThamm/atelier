# ADR-0024: Surfacing Terraform `check` block warnings

## Status

Accepted — complements [ADR-0012](0012-validation-via-terraform-validate.md)
(live validation) and [ADR-0011](0011-plan-output-tree.md) (plan output).
Touches `FailedChecks`/`CheckWarning` in `internal/tui/plan.go`, the plan
screen in `internal/tui/plan_view.go`, and the header/hints/modal wiring in
`internal/tui/view.go` and `internal/tui/model.go`.

## Context

Terraform modules can declare `check` blocks with `assert` conditions and an
`error_message`. Unlike `validation` blocks (which reject bad variable values
outright) and resource pre/post-conditions (which fail the plan), a `check`
block assertion that fails produces an **advisory warning** — the plan and
apply still succeed. The observability-stack COS / COS Lite modules use this
pattern to nudge deployers about storage sizing, e.g.:

```hcl
check "grafana_storage_directives" {
  assert {
    condition     = length(var.grafana.storage_directives) > 0
    error_message = "grafana.storage_directives is unset, so it will use the default 1G volume. Set a size before deploying to production; ..."
  }
}
```

Before this ADR, Atelier never surfaced these warnings. Two facts constrain
the design:

1. **They are warnings, not errors.** Routing them through the existing error
   surface (red status bar, `[E]` error modal, `[!]` required markers) would
   over-alarm the user for a non-blocking condition.
2. **They only evaluate at plan time.** `terraform validate` does not evaluate
   `check` blocks — it never computes variable values against providers. So
   the live-validation path from ADR-0012 will *never* observe them; the
   signal has to come from the plan.

Three options for *where* to surface them were considered:

- **(a) Per-variable warning icon** in the left pane.
- **(b) Plan-level summary** (a banner + count + on-demand detail).
- **(c) Both — plan-level primary, per-variable opportunistic.**

## Decision

**Option (b): a plan-level warning surface, driven by the plan's machine-
readable check results.**

- Extract failed checks from `tfjson.Plan.Checks` (the `checks` section of
  `terraform show -json`) rather than scraping human-readable stdout. Only
  entries with `Kind == "check"` and `Status == "fail"` are surfaced; each
  carries the check's display address and the author's `error_message`
  (`CheckResultProblem.Message`). This is `FailedChecks(plan) []CheckWarning`,
  a pure, unit-tested helper mirroring `BuildPlanTree`.
- Surface the warnings on three coordinated, **peach-tinted** (not red)
  affordances:
  1. A `⚠ N check warning(s)` chip in the header, beside the validate summary.
  2. A one-line banner under the plan summary showing the first message and a
     `[W]` hint, shown only when warnings exist.
  3. A `[W]` detail modal listing every failed check with its full message
     (including any doc URL the author embedded), reusing the error-modal
     frame and dismiss pattern.
- Warnings are populated on `planResultMsg` and cleared on re-plan and on
  apply, so they never go stale relative to the current plan.

## Alternatives considered

### Per-variable warning icon (a)

Rejected as the *primary* surface:

- **No reliable check→variable mapping.** A `check` block's name and message
  are free-form and may reference multiple variables, locals, or resources.
  Associating `check "grafana_storage_directives"` with
  `var.grafana.storage_directives` requires a heuristic (name/message string
  matching) that is right for COS Lite and wrong in general. Atelier's design
  (ADR-0008, ADR-0012) is to lean on Terraform's own semantics and avoid
  guessing.
- **Timing mismatch.** The variable list is shown before a plan; checks are
  only known after one. An icon that appears post-plan and goes stale on the
  next edit is confusing.

A future, non-heuristic enhancement remains open: when a check `Message`
begins with a token that *exactly* matches a known variable name (the COS
convention is `"<varname>.storage_directives ..."`), the row *may* be
decorated as an opportunistic extra — but only on top of (b), gated on an
exact prefix match, and never as the sole surface.

### Scraping plan stdout

Rejected. The JSON `checks` section is authoritative and stable; parsing the
human-readable stream would be brittle and would drift across Terraform
versions.

## Consequences

- The header is a multi-purpose status surface: module info, validate result,
  and now check-warning count, with errors (red) and warnings (peach) visually
  distinct.
- The plan screen budgets one extra line for the banner when warnings exist;
  `planPanelHeight` subtracts it so the panes stay within the fixed height and
  never push the footer off-screen.
- Warnings are strictly advisory: they never block `[A] apply`, matching
  Terraform's own treatment of `check` blocks.
- Because extraction reads `Plan.Checks`, no new Terraform invocation is
  added — the data already rides along with the plan Atelier parses.
