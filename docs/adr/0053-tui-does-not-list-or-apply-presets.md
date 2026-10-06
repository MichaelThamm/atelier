# ADR-0053: The TUI does not list or apply preset bundles

## Status

Accepted — supersedes [ADR-0031](0031-presets-as-tfvars-bundles.md) and
[ADR-0026](0026-save-preset.md) in part. The decision that presets *are*
Terraform-native `.tfvars` bundles stands unchanged; what changes is that the
TUI no longer lists or applies them. `S`, which writes one, is kept.

## Context

[ADR-0031](0031-presets-as-tfvars-bundles.md) unified presets on `.tfvars`
bundles and gave the TUI two keys: `F` listed the discovered bundles and applied
the selected one, `S` wrote the current configuration as a new bundle.

Since then the CLI reached parity with both. `--var-file <name>` resolves a
bundle through `bootstrap.ResolveVarFile` across personal walk-up
`atelier.presets/` directories, the module repository, and the gallery, and
applies it; `--list-var-files` prints the same list. `atelier apply <module>
--dir <wrapper> --var-file <name>` composes and deploys in one re-runnable
command. Applying a bundle from inside an editor session is therefore a flag,
and the picker is a second way to reach a capability the command line already
has.

The picker was also showing the user bundles that could not apply.
`presetsFromBundles` resolved candidates through
`bootstrap.ListAllVarFiles`, which appends `GalleryVarFiles()` unconditionally —
every gallery bundle, for every product. Each was then read against the
*primary* module's schema, so a `loki-defaults.tfvars` in a `cos-lite` wrapper
was type-checked against unrelated variables and surfaced as `(N ignored)`. In
a multi-module wrapper only the primary module's schema was consulted at all,
so a bundle meant for the second module was checked against the wrong one.

The cost was 246 lines of source and 419 of tests, in a file whose every other
function — `openSavePreset`, `commitSavePreset`, `bundleFileName` — belongs to
the `S` flow that is staying.

## Decision

### 1. `F` and the picker are removed

`handlePresetKey`, `applyPreset`, `applyPresetCmd`, and `renderPresetPicker`
are deleted, along with `Model.presets`, `presetPicker`, `presetCursor`,
`Model.SetPresets`, and the `ResolvedPreset` type. `presetsFromBundles` in
`cmd/atelier` goes with them; so does `RefSwitchResult.Presets` and the refresh
it drove, since the only consumer was the picker.

`--var-file <name>` applies a bundle and `--list-var-files` lists them, both
documented in [SPEC §6.8](../SPEC.md) and the README.

### 2. `S` is kept

`S` still snapshots the current configuration and writes
`atelier.presets/<name>.tfvars`. It is the one preset operation with no CLI
equivalent, it is the write half of the bundle contract, and it composes with
the variable editors that remain. It no longer refreshes a picker list after
writing, since there is no list.

`preset.go` keeps `snapshotValues`, which selects the sparse non-default values
using `wrapper.ShouldEmit` and `wrapper.SparseValue` — the same rule the writer
uses (ADR-0007).

### 3. Filtering belongs to the listing command, not the TUI

The all-gallery noise was a symptom of an unfiltered candidate list, not of the
picker. `--list-var-files` already prints the source and description per row and
is where a module-aware filter belongs; this ADR removes the TUI's copy of that
list so there is one place to fix it.

## Alternatives considered

**Keep `F` as a convenience for people who do not know the flag name.**
Rejected: `--list-var-files` is discoverable from `--help`, the bundle name is
the argument `--var-file` takes, and the TUI's copy was the one place that
offered bundles that could not apply.

**Keep the picker but filter it to the current module.** Rejected as strictly
worse: it fixes the symptom in the surface we are removing and leaves
`--list-var-files` — the flag that survives — still showing every gallery
bundle for every product.

**Move `S` to the CLI as `atelier presets save`.** Not now. It is a working
feature with tests; a CLI form would need to decide whether it captures the
primary module or every module in a multi-module wrapper, which `snapshotValues`
does not currently answer. Worth revisiting alongside `atelier presets list`.

**Keep `RefSwitchResult.Presets` for a future consumer.** Rejected: deadcode
would fail the build, and a speculative field is the kind of stale prose
`AGENTS.md` warns against.

## Consequences

- The TUI loses a key. Applying a bundle is `--var-file`, listing is
  `--list-var-files`, and neither is available inside the editor — which is the
  point: the CLI is the interface for a wrapper that is already written.
- 246 lines of source and 419 of tests are gone, and `preset_keys.go` now holds
  only the `S` flow.
- No bundles that cannot apply are offered anywhere in the TUI. The
  `--list-var-files` equivalent is unfiltered for now; narrowing it is follow-up
  work that this ADR makes possible rather than blocking.
- `internal/tui/AGENTS.md`, SPEC §3, §6.8, §7.3, §11, and the README are
  updated. ADR-0031 is marked superseded and left unedited.
