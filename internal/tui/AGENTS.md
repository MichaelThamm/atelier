# internal/tui

The Bubble Tea TUI. Read the root [AGENTS.md](../../AGENTS.md) first; this
file only adds what is local to the package.

## File map

| File | Responsibility |
| --- | --- |
| `model.go` | Top-level `Model`; owns `wrapper.State`; routes input to the active editor. |
| `editor.go` | `cellInput` readline cell and the `Editor` interfaces; variable type to widget. |
| `view.go` | Rendering helpers, ANSI-aware wrapping, layout. |
| `theme.go` | All colors and role styles (Catppuccin Mocha/Latte). |
| `planner.go` | `Planner` / `Applier` / `Validator` interfaces and the tfexec-backed implementation. |
| `plan.go` | Pure plan/state tree, attribute diff, summary, and check-warning builders. |
| `plan_view.go` | Full-screen plan rendering. |
| `preset.go` | `ResolvedPreset` and applying a preset to variables. |
| `refswitcher.go` | `RefSwitcher` interface and result types. |
| `progress.go` | Thread-safe progress and log capture for long-running terraform. |

## Invariants

- **Two-pane layout** (variable list ⇄ editor) with a status pane; see
  [ADR-0006](../../docs/adr/0006-two-pane-ui-layout.md) and SPEC §7.
- **All styling goes through `theme.go`.** No inline `lipgloss.Color` /
  `AdaptiveColor` anywhere else; a theme swap should be a one-file change.
- **Terraform is reached through interfaces, not directly.** Long-running
  work goes via `Planner`/`Applier`/`Validator`/`RefSwitcher` so tests can
  substitute stubs. Do not shell out to terraform from `model.go`.
- **Plan tree construction in `plan.go` is pure.** Keep rendering out of it;
  it is what makes plan logic unit-testable without a terminal.
- Editor behavior is spec'd: readline editing
  ([ADR-0020](../../docs/adr/0020-readline-style-text-editing.md)), map row
  lifecycle ([ADR-0023](../../docs/adr/0023-map-row-editing-lifecycle.md)),
  ref matching ([ADR-0025](../../docs/adr/0025-ref-selection-matcher.md)).
  Change those only with a new ADR.

## Testing

Tests drive the model and assert against rendered strings. Use `stripANSI`
from `test_helpers_test.go` unless the test is specifically about styling
(`theme_test.go` deliberately does not strip). Prefer table-driven tests with
scenario-named cases. Run `go test ./internal/tui/...` — this is the fastest
loop in the repo; use it while iterating.
