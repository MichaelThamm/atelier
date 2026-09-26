# internal/tui

The Bubble Tea TUI. Read the root [AGENTS.md](../../AGENTS.md) first; this
file only adds what is local to the package.

## File map

| File | Responsibility |
| --- | --- |
| `model.go` | Top-level `Model`, state enums, row model, constructor, accessors, `Init`/`View`/`SaveIfDirty`. |
| `update.go` | `Update` and the top-level key router. |
| `messages.go`, `commands.go` | Bubble Tea message types and `tea.Cmd` producers (plan, apply, validate, ref). |
| `list_keys.go`, `plan_keys.go` | Key handling for the variable list/logs, and for the plan/diff views. |
| `preset_keys.go`, `ref_keys.go` | `.tfvars` bundle picker/save flows; ref-switch modal, matching, and apply. |
| `helpers.go` | Small model helpers (labels, counts, diagnostics formatting). |
| `editor.go` | `cellInput` readline cell, `Editor` interfaces, dispatcher, and the scalar editors (`string`/`number`/`bool`/read-only). |
| `editor_map.go`, `editor_mapobject.go` | The `map(string)` and `map(object(...))` editors. |
| `editor_object.go` | The `list`/`set` and `object` editors and their field helpers. |
| `view.go` | Core view helpers: wrapping, modal frame, layout primitives. |
| `view_panes.go`, `view_logs.go`, `view_modals.go` | Panes/header/footer; logs view and status line; help/ref/preset modals. |
| `theme.go` | All colors and role styles (Catppuccin Mocha/Latte). |
| `planner.go` | `Planner` / `Applier` / `Validator` interfaces and the tfexec-backed implementation. |
| `plan.go` | Pure plan/state tree, attribute diff, summary, and check-warning builders. |
| `plan_view.go` | Full-screen plan rendering. |
| `preset.go` | `ResolvedPreset` (a `.tfvars` bundle) and `snapshotValues` for saving the current configuration. |
| `refswitcher.go` | `RefSwitcher` interface and result types. |
| `progress.go` | Thread-safe progress and log capture for long-running terraform. |

## Invariants

- **Two-pane layout** (variable list ⇄ editor) with a status pane; see
  [ADR-0006](../../docs/adr/0006-two-pane-ui-layout.md) and SPEC §7.
- **All styling goes through `theme.go`.** No inline `lipgloss.Color` /
  `AdaptiveColor` anywhere else; a theme swap should be a one-file change.
- **Terraform is reached through interfaces, not directly.** Long-running
  work goes via `Planner`/`Applier`/`Validator`/`RefSwitcher` so tests can
  substitute stubs. Never shell out to terraform from the TUI outside those
  interfaces.
- **Plan tree construction in `plan.go` is pure.** Keep rendering out of it;
  it is what makes plan logic unit-testable without a terminal.
- **Presets are `.tfvars` bundles** ([ADR-0032](../../docs/adr/0032-upstream-tfvars-discovery.md)):
  the picker lists personal walk-up and repo example bundles (local does not
  hide a same-named repo bundle — the source is shown and the user picks), `S`
  writes `atelier.presets/<name>.tfvars`, and the list must be refreshed after
  a ref switch.
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
