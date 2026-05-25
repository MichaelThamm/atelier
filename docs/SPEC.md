# Atelier v1 Specification

Status: **implemented** — reflects the current state of the v1 codebase.

This document specifies what Atelier v1 will do and what shape it takes. It is
*not* an implementation plan — it describes the surface, contracts, and
behaviours the implementation must satisfy. Architectural decisions referenced
inline as `ADR-NNNN` are captured separately under [`adr/`](adr/).

---

## 1. Overview

Atelier is a provider-agnostic terminal UI for configuring Terraform root
modules. It works with any Terraform provider (AWS, GCP, Azure, Juju, etc.)
and has no provider-specific code paths. The user points it at a Terraform
module (typically a public git repository), and Atelier:

1. Clones the repository into a managed cache (`.atelier/clone/`).
2. Detects configurable module candidates (root modules within the repo) and
   lets the user pick one.
3. Fetches the provider's configuration schema via `terraform providers
   schema -json`.
4. Presents the module's variables and the provider's configuration as an
   editable two-pane TUI surface.
5. Writes a wrapper Terraform project in the current working directory: a
   `main.tf` calling the module via its git source, a `versions.tf`,
   `providers.tf`, supporting files, and a `.gitignore`.
6. On user request, runs `terraform plan` against the wrapper and renders the
   result inline.
7. On user request (after a successful plan), runs `terraform apply` using
   the cached plan file.

## 2. Goals and non-goals

### v1 goals

- Provider-agnostic: work identically for any Terraform provider without
  special-casing provider names or resource types.
- Work for any Terraform root module that declares variables.
- Produce a wrapper directory that is runnable without Atelier installed.
- Round-trip cleanly: a user can hand-edit `main.tf` between sessions and
  Atelier respects the edits (modulo Atelier's own write rules; see §10).
- Surface module-maintainer curation when a `atelier.yaml` manifest is
  present, but require no maintainer effort to function.
- Distribute as a single static Go binary; package as a snap.

### v1 non-goals
- Authenticated git access. Public repositories only in v1; private
  repositories deferred.
- Sensitive secret handling beyond variable-indirection with a gitignored
  tfvars file. v1 assumes a development trust model; see [ADR-0009](adr/0009-secrets-handling-v1.md).
- Terraform Registry sources (`namespace/name/provider` form). Deferred to v2.
- `any` and `tuple([...])` variable types as first-class widgets. Rendered as
  read-only HCL with an "edit in `$EDITOR`" affordance in v1.
- Multiple instances of the same provider via `alias`.

## 3. Glossary

- **Module** — a Terraform module: a directory of `.tf` files declaring
  `variable`, `resource`, `output`, and other blocks.
- **Module candidate** — a directory within a cloned repository that looks
  like a configurable root module. Identified heuristically (any directory
  with `.tf` files declaring `variable` blocks, excluding `tests/`,
  `examples/`, and modules referenced by another module as `source = "./..."`)
  or by maintainer declaration in `atelier.yaml`.
- **Wrapper** — the Terraform project Atelier writes to the user's current
  working directory. Contains a `module {}` block referencing the chosen
  module via its git source, the user's variable overrides, and supporting
  files.
- **Manifest** — `atelier.yaml` at the root of a module repository. Optional;
  declares friendly names, descriptions, and presets for module candidates.
- **Session** — one invocation of `atelier` against a wrapper directory.
- **`.atelier/`** — a hidden subdirectory inside the wrapper holding
  Atelier-managed internal state (module clone cache, session metadata).
  Regenerable; safe to delete; gitignored.

## 4. Wrapper directory layout

The wrapper is rooted at the current working directory. Files Atelier writes
or owns are listed below; the user may add their own (`.git/`, additional
`.tf` files, etc.) freely.

```
<cwd>/
├── main.tf              # module {} block calling the chosen module via git
├── versions.tf          # terraform { required_providers {...} } block
├── providers.tf         # provider "X" {...} blocks
├── outputs.tf           # re-exports all module outputs (auto-generated)
├── variables.tf         # only if the wrapper declares its own variables
│                        #   (e.g., for sensitive value indirection)
├── secrets.auto.tfvars  # values for sensitive variables (gitignored)
├── README.md            # one-time auto-generated; user may edit freely
├── .gitignore           # one-time auto-generated; user may add to it
└── .atelier/            # internal state; gitignored
    ├── clone/           # shallow clone of the module repo for introspection
    │   └── <module>/    # subdir matching the module candidate path
    ├── cache/
    │   └── providers/   # cached `terraform providers schema -json` outputs
    └── session.json     # last opened, resolved SHA, etc.
```

The wrapper is independently runnable: `cd <wrapper> && terraform init &&
terraform plan && terraform apply` works on any machine with `terraform`
installed, with or without Atelier. The `.atelier/` directory is purely
Atelier's cache; deleting it forces a re-introspection on the next `atelier`
invocation but does not affect Terraform behaviour.

See [ADR-0001](adr/0001-wrapper-as-durable-artifact.md) and
[ADR-0004](adr/0004-wrapper-layout.md).

## 5. Loading: from URL to ready-to-edit

### 5.1 Module sources

v1 supports two source forms:

- **Git URL** — any HTTPS or SSH git remote. Public repos only. Example:
  `https://github.com/canonical/observability-stack.git`.
- **Local path** — for development. Example: `./terraform/cos-lite`. Passed
  via `--source` flag (see §6).

Terraform Registry sources are out of scope for v1.

### 5.2 Clone and candidate discovery

`atelier init <git-url>` performs the following sequence:

1. Resolve the ref (defaults to the remote's HEAD; overridable via `--ref`).
2. `git clone --depth 1 --branch <ref>` (or `--depth 1` + `git checkout <sha>`
   for SHA refs) into `.atelier/clone/`.
3. Scan the clone for module candidates:
   - If `atelier.yaml` exists at the clone root, use its `modules:` list
     verbatim.
   - Otherwise, walk the tree, treating every directory containing at least
     one `.tf` file with a `variable` block as a candidate, **excluding**:
     directories named `tests/`, `test/`, `examples/`, `example/`; directories
     referenced as `source = "./<path>"` by another module (those are child
     modules, not root candidates); directories under `.atelier/`.
4. Present the candidates as a flat list with paths and descriptions
   (manifest → README first paragraph → path; see §11). If exactly one
   candidate is found, skip the list and proceed.
5. Resolve `terraform`'s presence and version (must be >= 1.5; tofu is
   acceptable). Run `terraform init` in the chosen candidate directory inside
   the clone (purely to populate provider schemas; Atelier does not invoke
   plan from inside the clone).
6. Run `terraform providers schema -json` to obtain provider configuration
   schemas. Cache per `<provider-source>@<version>` in
   `.atelier/cache/providers/`.
7. Open the TUI on the wrapper. If `main.tf` does not yet exist (a fresh
   init), bootstrap a minimal wrapper with the module reference and a stub
   provider block; the TUI then renders defaults and lets the user configure.

See [ADR-0003](adr/0003-gitops-loading.md) and
[ADR-0008](adr/0008-provider-schema-discovery.md).

### 5.3 Ref handling

When the user types a ref (e.g. `main`, `v1.2.0`, `abc123`), Atelier:

- Stores the user's literal input in the wrapper's `module { source = "...?ref=..." }` clause.
- Resolves the ref to a commit SHA via `git ls-remote` and displays it in the
  TUI alongside the literal.

Following a moving ref (e.g. `main`) is a deliberate user choice; pinning to a
SHA can be done by typing the SHA into the ref prompt. See
[ADR-0007](adr/0007-sparse-wrapper-write-rule.md) for the related write rule.

### 5.3.1 In-TUI ref switching

The user can switch the module ref from within the TUI by pressing `R` from
the left pane. This opens a modal prompt showing the module name and source
URL for context, pre-filled with the current ref.
On confirmation, Atelier:

1. Re-clones the module at the new ref.
2. Carries over existing user values for variables that still exist in the
   new ref into the wrapper before running init (required variables must be
   present in the HCL for init to succeed).
3. Runs `terraform init -upgrade` in the wrapper to fetch the new module
   revision and update providers.
4. Re-parses variables from the new ref.
5. Preserves all existing user overrides. Variables that no longer exist in
   the new ref are kept in state as orphaned overrides (recoverable if the
   user switches back).
6. Displays a status message summarising the switch and listing any orphaned
   variable names.

This enables cross-ref upgrade comparison: the user configures at ref `v1.0`,
runs a plan, switches to `v2.0`, and plans again to see the infrastructure
delta. See [ADR-0003](adr/0003-gitops-loading.md).

### 5.4 Default-change surfacing on ref bump

When the user re-opens an existing wrapper and the resolved SHA has changed
since the last session (recorded in `.atelier/session.json`), Atelier:

1. Diffs the variable defaults between the previous resolved SHA and the
   current one (both are still in the clone cache, or are re-fetched).
2. Displays a one-shot summary modal listing changed defaults, e.g.:
   ```
   Module ref resolved to a new commit since last session.
     v1.2.0 (abc123) → main (def456)
   Defaults that changed:
     • alertmanager.constraints: "arch=amd64" → "arch=arm64"
     • ingress.alertmanager: true → false
   These are now your effective values for fields you have not overridden.
   ```
3. User dismisses to continue; the summary remains accessible via a hotkey.

This protects users from silent infrastructure drift when following moving
refs. See [ADR-0007](adr/0007-sparse-wrapper-write-rule.md).

## 6. CLI surface

```
atelier                                    # open TUI on existing wrapper in CWD
atelier init <git-url>                     # bootstrap a new wrapper in CWD
atelier init --source <path>               # bootstrap from a local module
atelier init <git-url> --module <subdir>   # skip the candidate picker
atelier init <git-url> --ref <ref>         # set initial ref (default: HEAD)
atelier init <git-url> --module <subdir> --ref <ref>   # combined
atelier init                               # adopt an existing Terraform project
atelier init --module-dir <name>           # adopt with custom subdir (relocate path)
```

That is the complete v1 CLI surface. Notably absent:

- No `atelier plan` / `atelier apply` (use `terraform` directly in the
  wrapper).
- No daemon mode or persistent sessions.

Outputs are viewable from within the TUI (see §7.6); a standalone
`atelier output` subcommand is not provided.

### 6.1 Behaviour matrix

| CWD state                          | Command            | Behaviour                                                                                  |
|------------------------------------|--------------------|--------------------------------------------------------------------------------------------|
| Empty                              | `atelier`          | Error: `Not a wrapper directory. Run 'atelier init <source>' to bootstrap.`                |
| Has wrapper files **and** `.atelier/` | `atelier`          | Open TUI normally.                                                                         |
| Has wrapper files, missing `.atelier/` | `atelier`          | Auto-rehydrate: parse `main.tf`, re-clone module, repopulate `.atelier/`, open TUI.        |
| Empty                              | `atelier init <url>` | Bootstrap wrapper.                                                                       |
| Non-empty, no `main.tf`            | `atelier init <url>` | Bootstrap; preserve existing files (`.gitignore`, `README.md`, etc.).                     |
| Has existing `main.tf` + `.atelier/` | `atelier init`   | Error: `Already initialized. Use 'atelier' to open.`                                       |
| Has existing `main.tf` + `.atelier/` | `atelier init <url>` | Error: `Wrapper exists. Use 'atelier' to open, or remove main.tf to re-init.`            |
| Has `.tf` with git `module {}`, no `.atelier/` | `atelier init` | **Adopt**: create `.atelier/`, clone upstream, open TUI. No files moved.            |
| Has `.tf` files, no git module block, no `.atelier/` | `atelier init` | **Relocate**: move files to `./module/`, generate wrapper, migrate state.    |

See [ADR-0002](adr/0002-author-and-plan-scope.md).

## 7. TUI layout

The TUI is a two-pane layout enclosed in rounded-border panels, with a
bordered header bar at the top and a bordered footer bar at the bottom.

```
╭────────────────────────────────────────────────────────────────────╮
│ Module: cos_lite ref track/2 (827b891)                             │
╰────────────────────────────────────────────────────────────────────╯
╭────────────────────╮ ╭─────────────────────────────────────────────╮
│ [ ] risk           │ │   app_name           "alertmanager"         │
│ [ ] base           │ │   config             {} (default)           │
│ [ ] ingress        │ │   constraints        "arch=amd64" (default) │
│ [ ] alertmanager   │ │   revision           null (default)         │
│ [ ] catalogue      │ │   storage_directives {} (default)           │
│ [ ] grafana        │ │ ▸ units              ▸ 3                    │
│ [ ] ...            │ │                                             │
╰────────────────────╯ ╰─────────────────────────────────────────────╯
╭────────────────────────────────────────────────────────────────────╮
│ [Tab] pane  [↑↓] navigate  [P] plan  [Q] quit  [?] help            │
╰────────────────────────────────────────────────────────────────────╯
```

### 7.1 Left pane — variable list

- Variables are sorted into three groups, alphabetically within each:
  1. Required variables (no default)
  2. Non-object-map optionals
  3. Object-map optionals (`map(object(…))`)
- Each variable has a modified-vs-default marker:
  - `[ ]` — at default
  - `[✓]` — modified
  - `[✓N]` — for object variables: N fields modified out of total optional
    fields
- Required variables (no `default`) show with a distinct marker
  (e.g. `[!]` when unset, indicating user must provide a value).
- Provider configuration appears as a top-level pseudo-group named
  `Provider: <name>` containing the provider's configuration attributes.

### 7.2 Right pane — editor

- Renders the selected variable as a widget appropriate to its type (see §8).
- For object variables, the right pane becomes a sub-form with one row per
  field. Nested objects open as further sub-forms (drill-in navigation).
- When editor content exceeds the panel height, the pane scrolls
  automatically to keep the cursor visible. A scroll percentage indicator
  appears at the bottom of the pane. See [ADR-0014](adr/0014-unified-layout-budget.md).
- Edits propagate to disk immediately (auto-save; see §13).

### 7.3 Header and footer bars

The TUI uses a bordered header and footer matching the panel theme (rounded
borders). The header shows module context and validation status; the footer
shows contextual key hints and transient status messages (spinner during
plan/apply, error summaries).

All screens share a unified layout budget (see [ADR-0014](adr/0014-unified-layout-budget.md)):
the bordered header consumes 3 lines (border + content + border), the bordered
footer consumes 3 lines, and 1 safety line is reserved for terminals that
report height inclusive of the cursor row. The remaining `height − 7` lines
are the **content height** available to each screen's body. Per-screen
elements (panel borders, summary lines) subtract from this budget.

**Header** (always visible):
- Module name + git ref (with resolved SHA short form).
- Validation indicator: `✓ valid` or `✗ N error(s), M warning(s)`.

**Footer** (contextual hints change by mode):
- Editor mode: `[Tab] pane  [↑↓] navigate  [P] plan  [F] preset  [R] ref  [Q] quit  [?] help`
- Plan mode: `[↑↓/g/G] navigate  [Enter] toggle  [[ ]] diff scroll  [P] re-plan  [A] apply  [O] outputs  [Esc] back  [?] help`
- Hints for `[F]`, `[R]`, `[O]`, `[A]`, `[E]` appear only when the
  corresponding feature is available.

- When validation or plan emits errors, the first line of the error is shown
  in the footer. Pressing `E` opens a full-screen error detail modal
  with the complete multi-line output; `Esc` dismisses it.
- On the first plan of each session, Atelier runs `terraform init` to
  ensure the module cache matches the wrapper's current source. After a ref
  switch, it uses `terraform init -upgrade` instead.

### 7.4 Ref switch view (modal)

Triggered by `R` from the left pane. Shows the module name, git source URL,
current ref (with resolved SHA), and an input field for the new ref. On
`Enter`, the module is re-cloned and reinitialised; a spinner shows progress.
On completion, the user returns to the editor with the new ref active. See
§5.3.1.

### 7.5 Plan view (modal-ish)

Triggered by `P`. Replaces the right pane (and optionally expands across both)
with the plan output:

```
Plan: 12 to add, 0 to change, 0 to destroy.

▾ module.cos_lite
  ▾ juju_application.alertmanager
    + name      = "alertmanager"
    + model     = var.model_uuid
    + …
  ▾ juju_application.catalogue
    + …
  ▾ juju_integration.ingress (alertmanager)
    + …
```

- Resources grouped by module path (collapsible) then resource type.
- Selecting a leaf opens an attribute diff in a side pane.
- Both the plan tree and the diff pane are independently scrollable when
  content exceeds the available height. The tree scrolls with `↑↓/PgUp/PgDn/g/G`;
  the diff pane scrolls with `[` and `]`. A scroll indicator shows position
  percentage when content overflows.
- Pressing `A` from the plan view runs `terraform apply` using the cached
  plan file. A spinner shows progress; success invalidates the plan (since
  the infrastructure now matches). Errors are surfaced in the status bar
  and viewable via `E`.
- Pressing `O` shows the output view (see §7.6).
- `Esc` returns to the editor.
- Inline per-attribute diffs *inside* tree nodes are out of scope for v1; see
  [ADR-0011](adr/0011-plan-output-tree.md).

See [ADR-0006](adr/0006-two-pane-ui-layout.md), [ADR-0011](adr/0011-plan-output-tree.md),
and [ADR-0014](adr/0014-unified-layout-budget.md).

### 7.6 Output view

Triggered by `O` from the plan view. Shows module outputs in a scrollable
modal with syntax-highlighted JSON values.

- **Before apply:** displays planned output values extracted from the plan
  file (`plan.OutputChanges`).
- **After apply:** fetches live values from state via `terraform output -json`.
- Sensitive outputs are masked (`<sensitive>`).
- Navigation: `j`/`k` scroll line-by-line, `Ctrl+D`/`Ctrl+U` or `PgDn`/`PgUp`
  for half-page jumps, `g`/`G` for top/bottom, `Esc`/`q` to dismiss.

#### `outputs.tf` generation

Atelier generates an `outputs.tf` in the wrapper that re-exports all of the
module's declared outputs:

```hcl
output "offers" {
  value = module.cos_lite.offers
}
```

This file is generated at bootstrap (`atelier init`) and kept in sync when
re-opening an existing wrapper (`EnsureOutputs`). It enables `terraform output`
to work outside Atelier and makes plan-time output values available.

## 8. Type-to-widget mapping

| Terraform type                            | Widget                                                                                  |
|-------------------------------------------|-----------------------------------------------------------------------------------------|
| `string`                                  | single-line text input                                                                  |
| `string` with `validation { contains([…], var.x) }` parsed as enum | dropdown (best-effort enum parsing; fallback to text)                  |
| `bool`                                    | checkbox                                                                                |
| `number`                                  | free-text input; accepts digits, `.`, `-`, `+`, `e`, `E` (scientific notation); invalid input highlighted |
| nullable scalar                           | above widget; empty input means `null` when the declared default is `null`              |
| `object({...})`                           | expandable sub-form, one row per field; nested objects drill in                         |
| `map(string)`                             | rows of `[key] = [value] [-]`, with `[+ Add row]` below                                  |
| `map(object(...))`                        | rows of `[key] [edit ▸] [-]`, drill into a sub-form for the object value                |
| `list(string)` / `list(any-simple)`       | rows of `[i] [value] [-]`, with `[+ Add row]` below                                     |
| `list(object(...))`                       | stack of expandable cards, each card a sub-form; `[+ Add card]` below                   |
| `set(string)`                             | same widget as `list(string)`; emits with implied `toset()`; header tagged `Set`        |
| `any`, `tuple([...])`                     | read-only HCL rendering with `[E]` to open `$EDITOR` on the wrapper                     |

### 8.1 Reordering

Lists support reordering via `Shift+↑` / `Shift+↓` on the focused item. Sets
ignore reorder hotkeys (they have no order); the widget header tag indicates
the type.

### 8.2 Set semantics

The widget visible distinction between list and set is minimal: header tag
(`Set` vs `List`), reorder hotkey ineffective on sets, duplicate-add shows a
brief toast (`already in set`). No prominent disclaimer.

### 8.3 Empty vs null collections

Atelier hides the `[]` vs `null` distinction: an empty collection in the UI
maps to whichever the variable's declared default is. If the user needs to
write the other case (e.g., explicitly `null` when the default is `[]`), they
hand-edit the wrapper. This is documented in the TUI's help.

## 9. Validation surfacing

`validation {}` blocks in the module's `variables.tf` are evaluated via
debounced `terraform validate`:

- After the user finishes editing (no edits for 500ms), Atelier runs
  `terraform validate` in the wrapper directory.
- Errors are surfaced in the status pane with the `error_message` from the
  validation block.
- A persistent `✓ Valid` / `N errors` indicator shows in the status pane.
- Validation does not block editing; the user can save invalid states.
  `terraform plan` will surface the same errors.

See [ADR-0012](adr/0012-validation-via-terraform-validate.md).

## 10. Wrapper-write rules

Atelier writes the wrapper using the [`hcl/v2`](https://github.com/hashicorp/hcl)
library to preserve formatting and any user-added comments. The rules for
*what* to write:

### 10.1 The sparse-plus-required rule

- **Required variables** (variables declared without a `default`): always
  emitted. The user must supply a value before Atelier saves a "valid"
  wrapper. The TUI marks unset required variables with `[!]` and the status
  pane flags them as missing.
- **Optional variables** (variables with a `default`): emitted only if the
  current value differs from the default.

This rule applies recursively for `object` types with `optional(T, default)`
fields: each field is emitted only if it differs from its `optional()` default,
unless it has no default (a `optional(T)` form without a second argument),
in which case it inherits Terraform's zero-value behaviour and Atelier treats
it as optional with the zero value as default.

### 10.2 Round-trip and hand-editing

- On open, Atelier parses `main.tf` and populates variable values from the
  existing `module {}` block's arguments. Any `module {}` argument Atelier
  doesn't recognise (e.g., `count`, `for_each`, `providers`) is preserved
  verbatim across saves.
- Comments and formatting outside Atelier-managed blocks are preserved
  through the `hcl/v2` AST.
- Hand-editing `main.tf` between sessions is supported. Atelier's next save
  reflects the hand-edits as the new baseline.

### 10.3 Generated files at init

When `atelier init` bootstraps a new wrapper, it writes:

- `main.tf` — `module "<name>" { source = "...?ref=..." }` plus required
  variable placeholders (or `# TODO` comments for required values the user
  hasn't supplied yet).
- `versions.tf` — `terraform { required_providers { ... } }` with the
  module's declared provider requirements.
- `providers.tf` — one `provider "<name>" {}` block per required provider,
  with stub attribute values the user will fill via the TUI.
- `outputs.tf` — one `output "<name>" { value = module.<m>.<name> }` block per
  module output, so that `terraform output` works outside Atelier and
  plan-time output values are available in-TUI (see §7.6). Re-generated on
  each session open (`EnsureOutputs`) to stay in sync with the module.
- `.gitignore` — Atelier-managed entries:
  ```
  .atelier/
  .terraform/
  terraform.tfstate
  terraform.tfstate.backup
  *.tfstate
  *.tfstate.backup
  secrets.auto.tfvars
  ```
- `README.md` — minimal scaffolding: what this directory is, how to apply
  (`terraform init && terraform apply`), and a note that `.atelier/` is
  internal.

See [ADR-0007](adr/0007-sparse-wrapper-write-rule.md).

## 11. Manifest schema (`atelier.yaml`)

Optional; lives at the module repository root. v1 schema is intentionally
minimal.

```yaml
modules:
  - path: terraform/cos-lite
    name: "COS Lite"
    description: |
      Production-ready Charmed Observability Stack: Alertmanager, Catalogue,
      Grafana, Loki, Prometheus, with TLS and ingress.
    presets:
      - name: production
        description: "Stable channel, TLS enabled, HA replicas."
        sets:
          risk: "stable"
          internal_tls: true
          alertmanager:
            units: 3

  - path: terraform/cos
    name: "COS"
    description: "Standalone COS deployment for development."
```

### 11.1 Field semantics

- `path` — required. Relative to the repository root.
- `name` — required. Display name in the candidate picker.
- `description` — optional. Falls back to README first paragraph, then path.
- `presets` — optional. Named bundles of variable overrides users can apply
  in bulk from the TUI. Each preset entry has:
  - `name` — required. Display name in the preset picker.
  - `description` — optional. Shown below the name in the picker.
  - `sets` — required. A map of variable names to values. Values follow YAML
    natural typing and are converted to the variable's declared Terraform type
    at load time. Variables referenced in `sets` that don't exist in the
    module are silently dropped.

### 11.2 What v1 does *not* support in the manifest

- Variable annotations (descriptions, friendly labels, value hints).
- Required-version constraints for Atelier itself.

These may appear in v2 or later; v1 keeps the manifest schema small to avoid
locking in a shape we'd regret.

See [ADR-0010](adr/0010-manifest-format.md).

## 12. Provider configuration

The wrapper must contain `provider "<name>" {}` blocks for any provider the
module requires. Atelier obtains the provider's configuration schema via
`terraform providers schema -json` and presents the configurable attributes
as a top-level pseudo-group in the left pane (`Provider: <name>`).

### 12.1 Sensitive provider attributes

Attributes flagged `sensitive: true` in the schema are handled via variable
indirection:

```hcl
# providers.tf
provider "juju" {
  controller_addresses = var.juju_controller_addresses
  username             = var.juju_username
  password             = var.juju_password
  ca_certificate       = var.juju_ca_certificate
}

# variables.tf (in the wrapper)
variable "juju_password" {
  type      = string
  sensitive = true
}

# secrets.auto.tfvars (gitignored)
juju_password = "..."
```

The TUI shows sensitive fields as masked (`***`) with a temporary reveal
toggle. Values round-trip via the gitignored `secrets.auto.tfvars` file.

See [ADR-0009](adr/0009-secrets-handling-v1.md) for the v1 security posture
and its explicit limitations.

## 13. Operational details

### 13.1 Auto-save

Every variable edit triggers a write to `main.tf` (and to `secrets.auto.tfvars`
if the field is sensitive). There is no draft / published distinction. The
file on disk always reflects what the user sees in the TUI.

### 13.2 Undo

The TUI maintains an in-memory undo stack of the last 20 user actions, popped
via `Ctrl+Z` and pushed forward via `Ctrl+Shift+Z` (subject to the chosen
keybinding scheme). Undo operates on logical edit actions, not character
keystrokes.

### 13.3 Plan invocation

`terraform plan` runs only on explicit user request (`P` key). It runs as a
background task; the TUI shows a spinner in the status pane while in-flight.
A new edit during an in-flight plan does not cancel it — the existing plan
finishes, the user can re-plan if needed. Plan results are cached in memory
for the session.

See [ADR-0002](adr/0002-author-and-plan-scope.md).

### 13.4 Error handling

| Error class                                      | Handling                                                                                        |
|--------------------------------------------------|-------------------------------------------------------------------------------------------------|
| `terraform` binary missing or version too old    | CLI-level error before TUI launch.                                                              |
| `git clone` fails (network / not found)          | CLI-level error before TUI launch.                                                              |
| `terraform init` fails at bootstrap              | CLI-level error before TUI launch.                                                              |
| `terraform validate` errors in session           | Surface in status pane; non-blocking.                                                           |
| `terraform plan` fails in session                | Surface in status pane with the error text; non-blocking; user re-plans after fixing.           |
| `git ls-remote` fails when resolving ref         | Show the literal ref but hide the resolved SHA; warn in status pane; user can retry.            |

## 14. Implementation notes

### 14.1 Language and key libraries

- **Language:** Go (>= 1.22). See [ADR-0005](adr/0005-implementation-language-go.md).
- **TUI:** [`github.com/charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea),
  [`bubbles`](https://github.com/charmbracelet/bubbles),
  [`lipgloss`](https://github.com/charmbracelet/lipgloss).
- **HCL:** [`github.com/hashicorp/hcl/v2`](https://github.com/hashicorp/hcl)
  (parser, writer, AST-preserving round-trip).
- **Terraform invocation:** [`github.com/hashicorp/terraform-exec`](https://github.com/hashicorp/terraform-exec).
- **Git operations:** shell out to `git`. v1 does not embed a git library.
- **Manifest parsing:** `gopkg.in/yaml.v3`.

### 14.2 Distribution

- Single static binary. Release tarballs for `linux/amd64` and `linux/arm64`
  at minimum.
- Snap package using the `home` plug for filesystem access.
- `go install github.com/MichaelThamm/atelier@latest` for development users.

### 14.3 Aesthetics

The TUI uses a **Catppuccin Mocha / Latte** adaptive colour palette:

- **Dark mode** (Mocha): deep base (`#1e1e2e`), mauve accent (`#cba6f7`),
  blue/green/peach/red for semantic roles (info, success, warning, danger).
- **Light mode** (Latte): cream base, matching semantic colours from the
  Latte palette.

All panels, modals, header, and footer use **rounded borders** (`lipgloss.RoundedBorder()`).
The focused panel's border is tinted with the primary accent colour (mauve);
unfocused panels use the muted faint colour. This gives the entire TUI a
consistent, boxed appearance.

JSON output values in the output view use syntax highlighting: keys, strings,
numbers, booleans, and null each have distinct colours drawn from the palette.

## 15. Open questions for v1

These are minor and can be settled during implementation; flagged here so
they don't get lost.

- **Provider lock file (`.terraform.lock.hcl`)**: generated by `terraform
  init`. Should Atelier surface it in the TUI? Probably no — it's a Terraform
  artifact, not an Atelier artifact. The user manages it like any other
  Terraform project would.
- **Module updates**: when `terraform init -upgrade` is needed (e.g. provider
  upgrades). v1 leaves this to the user; the README mentions it.
- **Wrapper naming**: the default `module "<name>"` block name uses the
  module candidate's directory basename (e.g., `cos-lite` → `module "cos_lite"`).
  Configurable in v2.
- **Multiple module instances**: not supported in v1. A user who wants two
  COS Lite deployments uses two wrapper directories.
