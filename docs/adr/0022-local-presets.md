# ADR-0022: Local presets: `atelier.local.yaml`

## Status

Accepted (supersedes [ADR-0010](0010-manifest-format.md))

## Context

ADR-0010 introduced `atelier.yaml`, a maintainer-owned manifest committed to
the **upstream module repository**. It served two purposes: overriding
heuristic candidate discovery (names/descriptions/paths) and declaring
presets (named bundles of variable overrides applied with `F`).

In practice this coupling was wrong for our workflow:

- **It pollutes upstream repos.** Requiring Atelier files (`atelier.yaml`) to
  be committed to a product repo like `observability-stack` is intrusive.
  Atelier is not yet stable enough to justify that footprint, and the feature
  was rarely used.
- **The pain is user-side, not maintainer-side.** The real friction is a user
  repeatedly configuring a module with *many* variables across several wrapper
  directories (e.g. `tf-testing/*`). They want to snapshot a configuration
  once and reuse it — a personal, local concern, not a maintainer-curation
  one.
- **Discovery override was dead weight.** Heuristic candidate discovery
  ([ADR-0003](0003-gitops-loading.md)) is good enough; the manifest's
  name/description override added complexity for little benefit.

## Decision

**Remove the upstream manifest entirely. Atelier never reads any file from the
upstream module repository.** Candidate discovery is purely heuristic.

**Presets become user-owned and wrapper-local**, in a file named
**`atelier.local.yaml`**, discovered by **walking up** from the wrapper
directory:

- Atelier collects every `atelier.local.yaml` from the wrapper directory up to
  the filesystem root (or `$HOME`, whichever comes first).
- A single file at a parent directory (e.g. `tf-testing/atelier.local.yaml`)
  is therefore shared by every wrapper beneath it — the primary motivation.
- **Precedence:** files nearer the wrapper win. When two files declare a preset
  with the same `name`, the nearer file's definition is used; otherwise presets
  are unioned. Because everything is local, there is no upstream/local merge to
  reason about.

The schema is unchanged from ADR-0010's `modules[].presets` shape, so the
parser and preset-application logic (`ResolvePresets`, `anyToCty`) are reused
as-is. Two ergonomic adjustments for local files:

- A module entry with **`path: "."`** matches the wrapper's *primary module*
  regardless of its upstream sub-path. Since local files live outside the repo,
  users should not have to track a path like `terraform/cos`. An exact
  sub-path match still wins over `"."` when both are present.
- `name`/`description` on a module entry are accepted but ignored (candidate
  naming is heuristic now).

### Explicitly out of scope

- **No `preset save` command.** Snapshotting current wrapper values into a
  preset was considered and rejected for now; users hand-write the YAML.
- **No local override of candidate names/descriptions.** Presets only.
- **No global (`~/.config`) presets store.** Walk-up local files cover the
  stated need; a global store may be revisited later.

## Consequences

- Upstream product repos stay clean; no Atelier files required or read.
- Users curate reusable presets in version-controllable files alongside their
  test wrappers, shared across sibling wrappers via a single parent file.
- The `internal/manifest` package is repurposed: `LoadFromRepo`/`FindModule`
  are removed; `LoadLocalPresets(wrapperDir, primaryModulePath)` performs the
  walk-up and returns the applicable presets. `candidate.Discover` no longer
  takes a manifest argument.
- ADR-0010 is superseded and retained for historical context.

## Alternatives considered

- **Per-wrapper file only** (no walk-up): rejected — forces duplicating a large
  preset into every `tf-testing/*` directory, the exact friction we're solving.
- **User-global config keyed by source URL**: rejected as the primary — it's
  invisible, not co-located with the test configs, and fiddly to key. May be
  added later as a supplement.
- **`--presets <path>` flag as the mechanism**: rejected as the primary —
  poor ergonomics for an interactive tool; could be added as an override.
- **Keeping upstream `atelier.yaml` and layering local on top**: rejected —
  keeps the upstream-pollution problem and introduces merge semantics we don't
  need.
