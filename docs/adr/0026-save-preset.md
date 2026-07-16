# ADR-0026: Generate a preset from the current configuration (`S`)

## Status

Accepted — amends [ADR-0022](0022-local-presets.md) (which explicitly deferred
a `preset save`). Builds on [ADR-0007](0007-sparse-wrapper-write-rule.md) (the
sparse-plus-required write rule) and reuses the ADR-0022 preset schema and
`ResolvePresets`/`anyToCty` load path. Touches `internal/manifest`,
`internal/tui`.

## Context

ADR-0022 made presets user-owned and wrapper-local (`atelier.local.yaml`) and
solved *reuse* — apply a named bundle of variable overrides with `F`. But it
left *authoring* entirely manual and listed a `preset save` command as
explicitly out of scope: "users hand-write the YAML."

Hand-writing is the remaining friction. To author a preset a user must learn
the DSL (`modules[].path`, `presets[].sets`), know each variable's name and
declared type, and hand-transcribe values — including the object-merge
semantics for nested types like `loki_worker`. The irony is that the user has
already expressed exactly the configuration they want: it is sitting in the TUI
(and in `main.tf`) right now. The DSL is a second, redundant place to say the
same thing, gated behind learning its grammar.

The insight that flips ADR-0022's deferral: the value set a preset should
capture is *identical* to the set Atelier already computes to write `main.tf`.
[ADR-0007](0007-sparse-wrapper-write-rule.md)'s `wrapper.ShouldEmit` /
`SparseValue` already yield "the non-default arguments, sparsely." Generating a
preset is therefore not new modelling — it is serialising that existing result
in the other direction (cty → YAML) instead of cty → HCL. The cost that made
`preset save` feel heavy in ADR-0022 largely evaporates.

## Decision

Add a **Save preset** action (the `S` key, left pane) that snapshots the
current configuration into a **new** `atelier.local.yaml` in the wrapper
directory.

### 1. What is captured: exactly what `main.tf` gets

The snapshot walks the primary module's declared variables and, for each, runs
the same `ShouldEmit` + `SparseValue` pair that `writeMain` uses. The result is
a minimal `sets:` map containing only what differs from the module's
defaults — byte-for-byte the same value set the user sees in `main.tf`. A new
`ctyToAny` (the inverse of `anyToCty`) serialises each `cty.Value` to
YAML-native Go, walking the value itself so partial objects from `SparseValue`
round-trip faithfully. Numbers serialise as integers when integral (`units: 1`,
not `1.0`); an explicit `null` is preserved (it is a meaningful preset value).

Two things are deliberately **not** captured, because they cannot round-trip
through the DSL or must never land in a shared file:

- **Sensitive variables (secrets).** Never serialised, so a committed or shared
  `atelier.local.yaml` cannot leak credentials. This governs *generation only*:
  secrets remain hand-authorable in the file and still load via `F` — the load
  path is unchanged. The asymmetry is intentional: writing a secret into a
  preset must be a deliberate human act, never an automatic side effect of `S`.
- **Wired reference expressions** (`var.`, `module.`, `data.`, `local.`,
  preserved verbatim in `UnknownAttrs` per ADR-0007 §10.2). The `sets:` DSL
  holds concrete values only; there is no faithful serialisation of an
  expression, so these are skipped rather than mangled into a string literal.

### 2. Create-only: Atelier never merges or overwrites a preset file

`S` writes a brand-new `atelier.local.yaml` in the wrapper directory, and only
if none exists there:

- If `./atelier.local.yaml` **already exists in the wrapper directory**, `S`
  refuses with a status hint ("… already exists here — edit it directly or move
  it to a parent") and writes nothing. The write uses `O_CREATE|O_EXCL`, so the
  refusal is atomic even against a file that appears between the check and the
  write.
- If the configuration is **entirely at its defaults** (nothing to capture),
  `S` refuses with "nothing to save."
- Otherwise a small modal collects a preset **name** (required) and optional
  **description**, then the file is written with a header comment documenting
  its provenance and Atelier's hands-off contract.

This create-only stance is the load-bearing design choice, and it is
deliberately narrower than a general "save." We rejected **merging** a new
preset into an existing file (see Alternatives): merging forces comment/layout
preservation, same-`name` conflict resolution, and choosing which `modules[]`
entry to edit — all risking silent damage to a file the user hand-authored and
cares about. Refusing instead keeps Atelier's relationship to any existing
`atelier.local.yaml` strictly read-only, matching how it already treats every
other user-authored file it discovers (ADR-0022). The generated file always
targets the wrapper directory (never a parent), so a shared parent file is
never touched; the union + nearest-wins discovery (ADR-0022 §11.1) means the
new wrapper-local file simply joins the set, and the user can move it up to
share it.

The accepted consequence: once a wrapper has an `atelier.local.yaml`, the TUI
cannot add a *second* preset — the user edits the file by hand. This is
acceptable because the goal is to lift the *initial* authoring burden: `S`
generates a correct, complete first preset that doubles as a worked template
for any further hand-editing.

### 3. Keybinding

`S` from the left pane (unused there; mnemonic for "save"). It is added to the
footer hints and the `?` help modal. `Ctrl+S` was rejected as easy to
fat-finger and less discoverable in a single-key TUI. The uppercase `S` in the
plan view already means "toggle state/diff"; the two scopes never overlap
(the plan screen intercepts keys before the editor layout), so there is no
collision.

## Alternatives considered

### Merge the new preset into an existing `atelier.local.yaml`

Rejected as the default (this ADR's central decision). Appending to a file
Atelier did not write means either losing the user's comments and layout on a
full re-marshal, or a `yaml.Node`-level surgical insert plus conflict rules for
duplicate `name`s and a policy for which `modules[]` entry to target. All of
that to edit a file the user owns — high risk of surprising or clobbering
hand-authored content, for a convenience (a second TUI-authored preset) that
hand-editing already covers. Refuse-and-inform is simpler and safe.

### Write to the nearest existing file up the tree

Rejected. Presets are commonly shared from a parent (`tf-testing/`). Editing
that shared file from a single wrapper's `S` is exactly the merge-into-someone-
else's-file hazard above, amplified across siblings. Always writing wrapper-
local, create-only, keeps the blast radius to the current wrapper.

### Include secrets, or offer a toggle

Rejected for generation. A preset file is meant to be committed/shared; an
automatic path that can serialise credentials into it is a footgun. Deliberate
hand-authoring of a secret in the file (which still loads) is the safe,
explicit alternative.

### `atelier preset save` CLI subcommand instead of a TUI key

Rejected as the primary. The configuration lives in the interactive session;
capturing it there (`S`) is where the user already is. A CLI snapshot would
have to re-derive state outside the TUI for little gain. Could be added later
as a non-interactive supplement.

## Consequences

- `internal/manifest` gains a write path — `SavePreset(dir, Preset)`
  (create-only, `O_EXCL`, provenance header) and `HasLocalFile(dir)` — having
  been read-only since ADR-0022. The parse/validate/schema surface is unchanged
  and reused to validate before writing.
- `internal/tui` gains `ctyToAny` (inverse of `anyToCty`), a `snapshotPreset`
  helper over `ShouldEmit`/`SparseValue`, and a small two-field modal following
  the ref-modal pattern (ADR-0025): `cellInput` fields, `renderModalFrame`.
- The generated file is a working, complete example of the DSL, which lowers
  the barrier to subsequent hand-editing — partially fulfilling the "learn the
  DSL by example" path.
- SPEC §11.3 no longer lists `preset save` as unsupported; §11 documents `S`.
- ADR-0022's "No `preset save` command" out-of-scope item is amended: the
  create-only, secrets-excluded, wrapper-local generator is now in scope; the
  rejection of *merging* and of a *global store* stands.

## Out of scope

- **Adding a second preset to an existing file from the TUI.** Refused by
  design; hand-edit the generated file.
- **Capturing secrets, references, or `main.tf` meta-arguments.** Concrete,
  non-sensitive values only.
- **Choosing the target directory / sharing to a parent from the UI.** Always
  wrapper-local; move the file by hand to share.
