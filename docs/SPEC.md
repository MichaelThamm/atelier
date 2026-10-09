# Atelier Specification

This document describes what Atelier does and the shape it takes. It is *not*
an implementation plan — it describes the surface, contracts, and behaviours
the implementation satisfies. Architectural decisions referenced inline as
`ADR-NNNN` are captured separately under [`adr/`](adr/).

---

## 1. Overview

Atelier is a provider-agnostic terminal UI for configuring Terraform root
modules. It works with any Terraform provider (AWS, GCP, Azure, Juju, etc.).
The configuration and TUI surface is fully provider-agnostic; `atelier import`
is the sole exception, containing provider-specific import steps (currently
Juju only — see ADR-0028). The user points it at a Terraform
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

### Goals

- Provider-agnostic: the configuration and TUI surface work identically for
  any Terraform provider without special-casing provider names or resource
  types. (`atelier import` is the sole exception; see §1.)
- Work for any Terraform root module that declares variables.
- Produce a wrapper directory that is runnable without Atelier installed.
- Round-trip cleanly: a user can hand-edit `main.tf` between sessions and
  Atelier respects the edits (modulo Atelier's own write rules; see §10).
- Let users curate their own reusable presets as `.tfvars` bundles in a walk-up
  `atelier.presets/` directory, and let product repos commit presets under
  `<module>/presets/`. Atelier reads only Terraform-native `.tfvars` from
  upstream, and only when named.
- Distribute as a single static Go binary.

### Non-goals (not implemented)

- Authenticated git access. Public repositories only; private repositories are
  not yet supported.

- Terraform Registry sources (`namespace/name/provider` form) — not yet
  supported.
- `any` and `tuple([...])` variable types as first-class widgets. Rendered as
  read-only HCL with an "edit in `$EDITOR`" affordance.
- Multiple instances of the same provider via `alias`.

### Explicit scope boundaries

Atelier is an **interactive module discovery and configuration tool**. It is
not an orchestration tool, not a deployment platform, and not a multi-
environment manager. The following are permanently out of scope (see
[ADR-0016](adr/0016-scope-boundaries-no-orchestration.md)):

- **Multi-environment fan-out.** No dev/staging/prod directory hierarchies.
  One wrapper = one environment. Use Terragrunt or Terraform workspaces for
  fan-out.
- **Cross-root dependency orchestration.** No DAG execution across separate
  state files. Atelier operates on a single Terraform root.
- **DRY config inheritance.** No parent/child config merging. The wrapper is
  self-contained.
- **Remote state management.** No auto-configuring backends. Backend
  configuration is the user's responsibility.
- **Deployment rollout orchestration.** No approval gates or phased rollouts.
  `terraform apply` is the deployment mechanism.
- **Platform lock-in.** No features that require HCP Terraform, Spacelift,
  or any managed cloud account. Atelier is local-first.

## 3. Glossary

- **Module** — a Terraform module: a directory of `.tf` files declaring
  `variable`, `resource`, `output`, and other blocks.
- **Module candidate** — a directory within a cloned repository that looks
  like a configurable root module. Identified heuristically: any directory
  with `.tf` files declaring `variable` blocks, excluding `tests/`,
  `examples/`, and modules referenced by another module as `source = "./..."`.
- **Wrapper** — the Terraform project Atelier writes to the user's current
  working directory. Contains a `module {}` block referencing the chosen
  module via its git source, the user's variable overrides, and supporting
  files.
- **Preset bundle** — a named `.tfvars` file. Personal bundles live in an
  `atelier.presets/` directory discovered by walking up from the wrapper
  directory; product presets live in the module repo under `<module>/presets/`.
  Applied with `--var-file`; listed by `--list-var-files`. See §11.
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
├── README.md            # one-time auto-generated; user may edit freely
├── .gitignore           # one-time auto-generated; user may add to it
└── .atelier/            # internal state; gitignored
    ├── clone/           # shallow clone of the module repo for introspection
    │   └── <module>/    # subdir matching the module candidate path
    ├── cache/           # scratch: cached plan files
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

Atelier supports two source forms:

- **Git URL** — any HTTPS or SSH git remote. Public repos only. Example:
  `https://github.com/canonical/observability-stack.git`.
- **Local path** — for development. Example: `./terraform/cos-lite`. Passed
  via `--source` flag (see §6).

Terraform Registry sources are not yet supported.

### 5.2 Clone and candidate discovery

`atelier add <git-url>` performs the following sequence:

0. Preflight the target directory (§6.5) and confirm with the user if anything
   about it looks unintended. Nothing is written before this passes.
1. Resolve the ref (defaults to the remote's HEAD; overridable via `--ref`).
2. `git clone --depth 1 --branch <ref>` (or `--depth 1` + `git checkout <sha>`
   for SHA refs) into `.atelier/clone/`.
3. Scan the clone for module candidates heuristically: walk the tree, treating
   every directory containing at least one `.tf` file with a `variable` block
   as a candidate, **excluding**: directories named `tests/`, `test/`,
   `examples/`, `example/`; directories referenced as `source = "./<path>"` by
   another module (those are child modules, not root candidates); directories
   under `.atelier/`.
4. Present the candidates as a flat list with paths and descriptions
   (README first paragraph → path). If exactly one candidate is found, skip
   the list and proceed.
5. Resolve `terraform`'s presence and version (must be >= 1.5; tofu is
   acceptable).
6. Open the TUI on the wrapper. If `main.tf` does not yet exist (a fresh
   init), bootstrap a minimal wrapper with the module reference and a stub
   provider block; the TUI then renders defaults and lets the user configure.

Provider **configuration** via `terraform providers schema -json` is specified
by [ADR-0008](adr/0008-provider-schema-discovery.md) but not yet implemented:
`providers.tf` is written with empty stub blocks derived from the module's
declared `required_providers`, and the TUI does not present provider
attributes. See [ROADMAP.md](ROADMAP.md).

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
   present in the HCL for init to succeed). What carries over is the module's
   input: concrete values, and expressions Atelier cannot evaluate
   (`model_uuid = data.juju_model.x.uuid`). An input the new revision dropped is
   removed, because Terraform would reject it as an unrecognised argument.
   Terraform meta-arguments (`depends_on`, `count`, `for_each`, `providers`) are
   the user's own composition and are left in the block untouched
   ([ADR-0050](adr/0050-tolerant-apply-converges.md)).
3. Runs `terraform init -upgrade` in the wrapper to fetch the new module
   revision and update providers.
4. Re-parses variables from the new ref.
5. Preserves all existing user overrides. Variables that no longer exist in
   the new ref are kept in state as orphaned overrides (recoverable if the
   user switches back).
6. Displays a compact status message in the footer (e.g.
   "Switched cos_lite to ref track/2 (69a6621) · 4 orphaned, 2 new — see [D]
   for details"). Press `D` to open a detail modal listing every orphaned and
   new variable name, any required-unset count, and init status.

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
atelier add <git-url|gallery-name>  # add a module to the wrapper (bootstraps a new one if needed)
atelier add <git-url> --as <name>   # add with explicit HCL block name
atelier add <git-url> --ref <ref>   # add at a specific ref
atelier add <git-url> --module <subdir>  # skip the candidate picker
atelier add <git-url> --dir <path>  # target directory; compose into it when it holds a wrapper (§6.2)
atelier add <git-url> --yes         # skip the target-directory confirmation (§6.5)
atelier add <git-url> --var-file <path|name>  # seed values from a .tfvars file (local path or repo-local name; repeatable/comma-separated)
atelier add <git-url> --var <K=V>   # set a single module input (repeatable; wins over --var-file)
atelier add <git-url> --list-var-files  # print the .tfvars bundles available (local + module repo)
atelier add <git-url> --json      # report where the wrapper went and what was added, as JSON (§6.11)
atelier rm <name> [--force]         # remove a module from the wrapper
atelier ls [--json]                 # list modules in the wrapper
atelier wrappers [PATH] [--json]    # list wrappers directly under PATH (default: CWD)
atelier apply <git-url>             # scaffold a wrapper in a new dir, init, then apply interactively
atelier apply <git-url> --module <subdir>  # skip the candidate picker
atelier apply <git-url> --ref <ref>  # pin a ref; re-points the block when the wrapper already declares the module (§6.9)
atelier apply <git-url> --as <name>  # target directory / HCL block name; selects the block to update
atelier apply <git-url> --dir <path> # target directory; compose into and deploy it when it holds a wrapper (§6.9)
atelier apply <git-url> --var <K=V>  # set a module input (repeatable; wins over --var-file)
atelier import [PROVIDER] [flags]          # import live resources into Terraform state
atelier import [PROVIDER] --json           # report the run as JSON (§6.11)
atelier purge [PATH] [--force]             # remove .atelier/ and .clone/ directories
atelier gallery list [--commands]          # list the bundled gallery quick starts
atelier gallery lint                      # check each entry covers its module's required inputs
atelier presets lint --module <dir> <file.tfvars>  # check preset bundles against a module's variables
atelier --help                             # print usage
```

Every command above also accepts `-h` and `--help`, on its own or after other
arguments, and prints the same usage text rather than reporting an unknown
flag. `atelier import --help` is therefore the same output as `atelier --help`,
which documents every command's flags; help always exits 0.

See [ADR-0038](adr/0038-flat-cli-surface.md) for the flat top-level command
surface (which supersedes [ADR-0018](adr/0018-additive-module-command.md)'s
`module` namespace), [ADR-0027](adr/0027-atelier-import.md) for the `import`
subcommand design, [ADR-0034](adr/0034-module-apply-one-liner.md) for
`atelier apply`, and [ADR-0048](adr/0048-machine-readable-output.md) for
`--json`.

There is no `atelier init`. A wrapper is created and modules are added through
`atelier add <url>`.

That is the complete CLI surface. Notably absent:

- No `atelier plan` (use `terraform plan` directly in the wrapper, or press
  `P` within the TUI).
- No command to apply a wrapper *without naming a module*. `atelier apply` takes
  a module URL and converges the target wrapper on it, whether the wrapper is new
  or already declares it (§6.9, §6.9.1). Deploying the wrapper exactly as it
  stands — no source argument — is `terraform apply` directly, or `A` from the TUI
  plan view.
- No daemon mode or persistent sessions.

There is no `atelier output` subcommand; run `terraform output` directly in the
wrapper. (An in-TUI output view is specified but not implemented — see §7.6.)

### 6.1 Import subcommand

`atelier import [PROVIDER] [flags]` imports a running deployment into Terraform
state. It discovers live resources with `terraform query`, matches them to the
module's resource addresses, and runs `terraform import` for each match — a
state-only operation that cannot alter infrastructure.

Provider-specific behaviour (currently Juju only — see ADR-0028) is reached
through five extension points: a pre-plan preflight step, a post-plan safety
check, post-import normalisation steps, an import-ID builder, and a fallback
matcher for resource types with composite identities. Providers are
registered behind the `Provider` interface in
`internal/importer/providers/providers.go`; the CLI wires whatever the
registered provider implements, and the importer core stays
provider-agnostic (ADR-0033). The Juju provider itself lives in
`internal/importer/providers/juju/`. The importer core also contains the error
hints in `internal/importer/hints.go`. ADR-0028 records the decision to scope
import support to Juju for v1.

Flags:

- `--source <git-url|name>` — clone a remote module, write an Atelier wrapper,
  and import into it. When omitted, imports into an already-initialised
  directory. A gallery entry name (§6.8) is accepted in place of a URL; it
  supplies the module and subdirectory, but **not** its pinned revision, and
  **none** of its presets, because an import must describe a deployment that
  already exists ([ADR-0047](adr/0047-import-gallery-name.md),
  [ADR-0055](adr/0055-gallery-import-names-its-revision.md)). A gallery-sourced
  import therefore requires `--ref`; an explicit `--module` still wins.
- `--module <path>` — skip the candidate picker and use the given module path.
- `--ref <ref>` — check out a specific git ref when cloning.
- `--dir <path>` — target directory (default: current directory). With
  `--source` it follows the `atelier apply` rule
  ([ADR-0044](adr/0044-dir-names-the-wrapper.md)): an existing `main.tf` is
  adopted, a missing directory is created, and one already holding other files is
  refused. Without `--source` nothing is scaffolded, so it must already be an
  initialised Terraform root. It also selects which walk-up `atelier.presets/`
  bundles are visible (§11).
- `--type <T>` — restrict discovery to the given list-resource type(s).
- `--var <K=V>` — supply a module variable value (repeatable). Written to
  `main.tf`.
- `--query-var <K=V>` — supply a value for the query engine's list blocks
  (repeatable), e.g. `model_uuid` for the Juju provider. Not a module input in
  general, so not written to `main.tf` — except that when the module declares a
  variable of the same name and leaves it unset, that input is seeded from it,
  so the same value never has to be given twice.
- `--var-file <path|name>` — seed values from a `.tfvars` file (repeatable): a
  local path, a walk-up `atelier.presets/` bundle, or a name committed to the
  module repo.
- `--list-var-files` — print the `.tfvars` bundles discoverable locally and in
  the module repo (source-labelled), then exit without importing. Requires
  `--source`, since the repo is only searched after a clone.
- `--dry-run` — write an `imports.tf` artifact of the matched resources, plan
  with it in place, report the preview, and stop. Terraform state is untouched.
- `--provider-version <ver>` — pin the provider version constraint.
- `--list` — print the provider's importable list-resource types and exit.
- `--no-init` — skip `terraform init` before importing.
- `--yes` / `-y` — skip the target-directory confirmation (§6.5). Only relevant
  with `--source`, which is the mode that scaffolds a wrapper.
- `--strict` — treat list-resource query errors as fatal (no automatic retry with fewer types).
- `--verbose` — print the full match trace, including every live object.

Variables the module declares without a default must be supplied (via `--var`,
a `--var-file`, or the wrapper): Terraform cannot plan without them, and
`terraform query` — which loads the root module — rejects the run first.
Identity values such as the Juju model UUID are not among them; Atelier seeds or
derives those itself.

`--source` against a directory that already holds a wrapper imports into that
wrapper, so `--source`, `--module` and `--ref` describe a module it already
declares. A component that contradicts `main.tf` is refused rather than dropped:
matching happens against the pinned revision, so an ignored `--ref` would import
against one the user did not ask for. Components the command omits are not a
contradiction — the wrapper's own subdirectory and ref are used.

Generated inputs (`atelier-import.tfquery.hcl`, `imports.tf`,
`atelier-import.auto.tfvars`) are removed when a run succeeds and kept only when
they would help the user retry.

See [docs/how-to/import-juju.md](../docs/how-to/import-juju.md) for a
worked Juju example.

### 6.2 Add, remove, and list modules

`atelier add <git-url|gallery-name>` is the primary entry point for
adding modules:

- If the target — `--dir`, else the current directory — already holds a
  `main.tf`, appends a `module {}` block to that `main.tf`. Otherwise it creates
  a directory — named after the discovered module candidate, or by `--dir`/`--as`
  — and scaffolds a fresh wrapper there, so `atelier add` no longer requires a
  hand-made `mkdir && cd`. `--dir` names *which* wrapper to compose into, so a
  multi-module deployment is built one `add` per module without entering the
  wrapper between steps ([ADR-0044](adr/0044-dir-names-the-wrapper.md)).
- Derives the HCL block name from the candidate directory basename unless
  `--as` is provided.
- Accepts a gallery entry name (§6.8) in place of a URL: the name expands to the
  entry's module, ref, block, and preset; an explicit flag still wins.
- Applies any `--var-file` values (a local path, a walk-up `atelier.presets/`
  bundle, or a name committed to the module repo), then any `--var` overrides,
  and writes them to the wrapper. `--var` wins over `--var-file`. For an
  object/map value the override *deep-merges* key-by-key into whatever is
  already set, so leaving a field out of `--var` preserves the value from the
  file rather than dropping it: `--var-file no-ingress --var
  'ingress={alertmanager=true}'` disables ingress except Alertmanager. Scalars
  and lists replace wholesale.
- Runs the target-directory preflight (§6.5) before writing anything.
- Refuses to add a module the wrapper already references at the same ref (§6.7).
- Does **not** run `terraform init` or `terraform apply`, then launches the TUI
  with the new module focused — `atelier add` is the editor path; `atelier apply`
  (§6.9) is the deploy path. When stdin or stdout is not a terminal — a script,
  CI, or `atelier add … < /dev/null` — the TUI is skipped and the command
  exits after writing the wrapper, so presets can be applied fully
  non-interactively.
- If the bootstrap fails partway, removes the `.atelier/` directory it created,
  leaving the target as it was found.

`atelier rm <name>` removes a module block and its clone under
`.atelier/clone/`. It removes no outputs, because Atelier writes none — see
§7.6. Does not run `terraform apply -destroy` — state cleanup is the user's
responsibility.

`atelier ls` prints a table (name, source, ref) without launching
the TUI. If the current directory has no `main.tf`, it reports that it is not
a wrapper and points at `atelier wrappers` (§6.10).

### 6.3 Purge

`atelier purge [PATH] [--force]` removes Atelier's internal directories
(`.atelier/` and `.clone/`) from the target directory (defaults to CWD).

- Only top-level directories in the target are removed; no recursion.
- Without `--force`, prompts for confirmation listing the directories to be
  removed.
- Prints each removed directory on success; prints "nothing to purge" if
  neither directory exists.
- Does **not** touch `.terraform/`, `*.tfstate`, or any user files.

This is useful for cleaning up Atelier state without disturbing the wrapper
itself, e.g. before archiving a wrapper directory or forcing a fresh
re-introspection on next open.

### 6.4 Behaviour matrix

| CWD state                          | Command            | Behaviour                                                                                  |
|------------------------------------|--------------------|--------------------------------------------------------------------------------------------|
| Empty                              | `atelier`          | Error: `Not a wrapper directory. Run 'atelier add <url>' to bootstrap.`             |
| Has wrapper files **and** `.atelier/` | `atelier`          | Open TUI normally.                                                                         |
| Has wrapper files, missing `.atelier/` | `atelier`          | Auto-rehydrate: parse `main.tf`, re-clone module, repopulate `.atelier/`, open TUI.        |
| Empty                              | `atelier add <url>` | Bootstrap wrapper + add module.                                                    |
| Non-empty, no `main.tf`            | `atelier add <url>` | Preflight warning + confirmation (§6.5); then bootstrap, preserving existing files.  |
| Non-empty, hand-authored `.tf` files | `atelier add <url>` | Preflight warning + confirmation (§6.5); then append, preserving existing blocks.  |
| Has existing wrapper (`main.tf` + `.atelier/`) | `atelier add <url>` | Append module block to existing `main.tf`. No prompt.                  |
| `--dir` names a directory holding `main.tf` | `atelier add <url> --dir PATH` | Append to that wrapper's `main.tf` (§6.5 preflight as above).          |
| `--dir` names a non-empty directory with no `main.tf` | `atelier add <url> --dir PATH` | Refused; nothing written.                          |
| Wrapper already has this module at this ref | `atelier add <url>` | Error naming the existing block; nothing written (§6.7).                |
| Any (has `.atelier/` or `.clone/`)  | `atelier purge`    | Prompt, then remove `.atelier/` and `.clone/`. Wrapper files untouched.                    |
| Any (neither exists)               | `atelier purge`    | Print "nothing to purge".                                                                  |

See [ADR-0002](adr/0002-author-and-plan-scope.md).

### 6.5 Target-directory preflight

`atelier import --source` defaults to the current directory, and `atelier add`
writes into the current directory when one already holds a wrapper. Before
writing anything, they inspect the target and — if anything looks wrong — print
the findings and ask for confirmation. When `atelier add` creates its own
directory (`--dir`/`--as`, or the candidate-derived name), a non-empty target is
refused rather than scaffolded over (§6.9).

`atelier add` and `atelier apply` inspect the same target — `--dir` when given,
else the CWD ([ADR-0044](adr/0044-dir-names-the-wrapper.md)). `atelier add`
asks; `atelier apply` prints without asking, because it rejects `--yes` and
Terraform's plan prompt is its confirmation (§6.9).

Findings are one of two levels:

- **warning** — prompts. The directory holds files Atelier did not put there,
  contains Terraform files, sits inside another wrapper's `.atelier/` or
  `.terraform/`, looks like the root of a project of another kind (`go.mod`,
  `package.json`, `charmcraft.yaml`, …), or is the user's home/config directory
  or the filesystem root.
- **note** — printed but never prompts. Describes a write Atelier is about to
  skip (an existing `required_providers` block or provider configuration), or an
  ordinary directory below another wrapper, which becomes an independent root.

Rules:

- A directory that is already a wrapper (`main.tf` **and** `.atelier/`) is never
  preflighted: the user has already declared its purpose.
- Files Atelier authors itself (`README.md`, `.gitignore`, `LICENSE`, `.git/`,
  `.terraform/`, state files) do not count as clutter. A warning users learn to
  dismiss unread is worse than no warning.
- `--yes` / `-y` skips the prompt. Findings are still printed.
- Without a terminal on stdin the command **fails** rather than proceeding,
  naming `--yes` in the error. Silence is not consent.

The same confirmation path serves `purge` and `atelier rm`.

See [ADR-0030](adr/0030-target-directory-preflight.md).

### 6.6 Declaration collisions

Bootstrap decides what to write by reading the directory's existing
declarations, not by checking filenames:

- `versions.tf` is skipped entirely if any `.tf` file already contains a
  `terraform { required_providers {} }` block, because Terraform permits only
  one per module. The provider requirements Atelier could not add are reported
  so the user can add them to their own block.
- `providers.tf` omits any provider whose local name is already configured,
  which would otherwise be a "Duplicate provider configuration" error.
- `main.tf` is never overwritten; a module block is appended to it.
- An existing `.gitignore` has only the *missing* Atelier patterns appended
  under a marker comment, so `.atelier/` and `.terraform/` do not show up as
  untracked files in the user's repository.

See [ADR-0030](adr/0030-target-directory-preflight.md).

### 6.7 Duplicate modules

`atelier add` compares the module being added against the wrapper's existing
module blocks by *identity* — remote URL, sub-directory, and literal ref — not by
block name. Block names are derived, so a duplicate frequently gets a
non-colliding name and would otherwise pass unnoticed.

| Existing block vs. the add | Behaviour |
|----------------------------|-----------|
| Same repo, same sub-dir, same ref | **Error.** Nothing is written. |
| Same repo, same sub-dir, different ref | **Error.** Nothing is written. |
| Different repo or different sub-dir | Allowed silently. |

Two blocks of one module declare two copies of the same resources. Terraform
accepts the configuration and fails later at apply, on colliding resource names —
far from the cause. The error is therefore a refusal, and `--yes` does **not**
bypass it. `--as NAME` is not an escape hatch either: it names the block to
write, so pointing it at a block that already declares the module is refused the
same way. A genuinely separate instance is a hand-written block in `main.tf`
(§6.9.1).

URL comparison normalises the `git::` prefix, a `.git` suffix, a trailing slash,
and case. Refs are compared as literally written in `main.tf`: resolving each
existing block's ref to a SHA would catch `--ref main` duplicating an unpinned
block already tracking `main`, but at the cost of a network round trip per block
on every add. The literal compare catches the case that actually occurs — the
same command run twice — and never blocks a genuinely distinct revision.

`atelier apply` compares the same way but acts on the answer instead of refusing
it: it updates the block (§6.9.1, [ADR-0050](adr/0050-tolerant-apply-converges.md)).

See [ADR-0030](adr/0030-target-directory-preflight.md).

### 6.8 `.tfvars` preset bundles

Presets are Terraform-native `.tfvars` files, not a bespoke format
([ADR-0031](adr/0031-presets-as-tfvars-bundles.md)). Atelier discovers them from
three sources and applies them to the wrapper's module arguments; the wrapper
shape is always the classic sparse `main.tf` (there is no pass-through mode —
[ADR-0031](adr/0031-presets-as-tfvars-bundles.md)).

A **preset** is not the same thing as Atelier's **gallery**
(`atelier gallery list`): the gallery is a curated set of module quick starts,
and a gallery entry *composes* zero or more presets as its default scenario
([ADR-0039](adr/0039-composed-gallery-presets.md)).

An entry separates the presets it composes from the ones it only *offers*.
`presets` are applied by default; `available_presets` appear in
`--list-var-files` and the gallery card, and the user opts in
with `--var-file <name>`. Declaring an offered preset rather than leaving it
unreferenced is what lets the gallery check bind it against the entry's module,
so it cannot drift out of sync with a variable the module renames or drops. An
entry composes only what it needs to deploy: `cos` composes
`cos-grafana-single-unit`, the smallest change that lets it plan, and offers
`cos-single-unit` and `cos-no-ingress` as opt-ins. An opt-in resolves by name
exactly like any other `--var-file`.

- `--var-file <path|name>` seeds values from a Terraform variable file
  (repeatable; comma-separated names accepted; later files win over earlier
  ones). A local path is used as-is; a bare name is resolved first against
  personal walk-up bundles (`<ancestor>/atelier.presets/<name>.tfvars`, nearest
  ancestor wins), then in the cloned module repository (`<module>/presets/`,
  `<module>/examples/`, `<repo>/terraform/presets/`,
  `<repo>/terraform/examples/`, `<repo>/presets/`, `<repo>/examples/`), and
  finally a preset bundled with Atelier's gallery
  ([ADR-0035](adr/0035-bundled-module-gallery.md)). A name that resolves
  nowhere is skipped with a warning rather than fatal, so a gallery entry's
  preset can be superseded by a bundle the user supplies.
- `--list-var-files` prints the available bundles (source-labelled) without
  writing anything. On `atelier add` it needs no other flag; on `import` it
  requires `--source`, since the repo is only searched after a clone. Bundled
  presets are narrowed to the gallery entries that deploy the module named on
  the command line, matched on repository and sub-directory — so
  `atelier apply cos-lite --list-var-files` reports `cos-lite-no-ingress` and
  not the presets for `cos`, `trino`, and the rest. `--all` drops the narrowing
  and prints every bundled preset; it is only meaningful with
  `--list-var-files`.
- `atelier gallery list [--commands]` renders Atelier's bundled gallery: the
  module, pinned ref, the composed presets (if any), the optional ones, and the
  command to deploy it. An entry whose module needs deployment-specific inputs (a Juju model UUID,
  S3 credentials) lists them under `requires`, each either a bare `name` —
  rendered as `--var name=<name>` — or `name=value`, which renders a working
  default and gives the gallery check a value that satisfies the variable's type
  and validation rules. `--commands` prints the non-applying scaffold form, one
  per entry, which is what `just gallery-check` and CI run; `atelier gallery
  requires <name>` prints an entry's `requires` list one per line, verbatim. The
  gallery is the lowest-precedence `--var-file` source, so a local or
  module-repo bundle of the same name wins.
- `atelier gallery lint` clones each entry's pinned module, reads its schema, and
  checks that every required input — a variable with no default — is supplied by
  one of the entry's *composed* presets or its `requires`. It reports inputs nobody supplies and
  `requires` the module no longer declares, and exits non-zero on either, so a
  module that gains a required input fails here rather than at apply time.
- `just gallery-bump` resolves every entry's module with `git ls-remote` and rewrites only
  `ref`, patching the manifest source so a run's diff is exactly the SHA lines. The
  `gallery-schedule` workflow runs it fortnightly (and on demand), validates the result with the
  gallery check, and opens a pull request when a pin moved — it never edits presets or
  `requires`, so a drifted entry is corrected by hand in that PR
  ([ADR-0040](adr/0040-automated-gallery-refresh.md)).
- A gallery card names a required input once, as a `--var` in the apply
  one-liner it offers, and does not restate the list in prose
  ([ADR-0046](adr/0046-gallery-card-states-inputs-once.md)).
- The GitHub Pages site publishes a second generated page, `/juju/`, beside the
  provider-agnostic `/gallery/`. It is built by the same tool
  ([ADR-0037](adr/0037-gallery-pages-site.md)) and holds the gallery's Juju
  knowledge in `tools/gallerysite`, not in `internal/gallery` or the manifest,
  so the binary and the CLI stay provider-agnostic
  ([ADR-0041](adr/0041-juju-opinionated-gallery-page.md)). A card renders the
  same command as the gallery page, and the Juju-specific variant — deploying
  into the reader's current model, plus S3 credentials and a charm channel from
  the environment — as a collapsed block beneath it. The variant is collapsed
  because it is an opinion: Juju has no single convention for naming a model
  (`model_uuid` takes a UUID, `model` an object whose `uuid` selects an existing
  model, or a model name), and on some modules pinning it overrides what the
  module would otherwise do. The model is read from the environment rather than
  resolved per command: the page's banner exports `CURRENT_MODEL` (the current
  model's UUID) and `CURRENT_MODEL_NAME` (its short name) from `juju show-model`,
  and every pin passes one of them, so a card's command is one `--var` per input
  ([ADR-0049](adr/0049-juju-page-model-from-environment.md)). An entry may need
  more than one pin to make the model pin apply — Charmed Kubeflow reads
  `model_uuid` only when `create_model` is false — and a module that always
  creates its own model, with no input that targets an existing one, gets a card
  that says so instead of a variant
  ([ADR-0042](adr/0042-juju-page-pins-and-own-model.md)).
- A gallery entry's **name** may be given to `atelier add` / `atelier
  apply` / `atelier import --source` in place of a URL. It expands to the entry's
  module, ref, block, and composed presets; an explicit `--ref`, `--module`,
  `--as`, or `--var-file` still wins,
  and a URL or local path is never treated as a name. A gallery-sourced
  `add`/`apply` names the module and revision it resolved to on stderr; an
  explicit `--ref` overrides the entry's pin and is called out, together with the
  entry's composed presets having been validated against that pin
  ([ADR-0039](adr/0039-composed-gallery-presets.md)). A bare name that matches
  nothing is an error naming `atelier gallery list`. `atelier import` differs in
  two ways: it takes the module and subdirectory but **not** the entry's ref — a
  gallery-sourced import must name the revision it deployed with `--ref`
  ([ADR-0055](adr/0055-gallery-import-names-its-revision.md)) — and it takes
  **none** of the composed presets, because it must describe a deployment that
  already exists ([ADR-0047](adr/0047-import-gallery-name.md)); the skipped
  presets are named on stderr.
- `--var <K=V>` sets a single module input directly (repeatable). It is applied
  after every `--var-file`, so it wins, and accepts an HCL expression for
  structured values (e.g. `--var 'ingress={alertmanager=false}'`). An
  object/map value deep-merges over the same variable's current value. A name
  the module does not declare, or a value that does not fit the declared type,
  is reported as a warning and skipped — `--var` is explicit input, so a typo is
  never applied silently.
- An attribute a `--var-file` does not fit — an undeclared name or a
  type-mismatched value — is skipped with a warning; `--strict` makes those
  binding problems fatal. Object/tuple values are not type-checked (Atelier's
  cty view loses `optional()` metadata); Terraform catches nested-shape errors.
- A `--var-file` that resolves to nothing at all — not a path, and no such bundle
  in an ancestor `atelier.presets/`, the module repository, or the gallery — is
  skipped with a warning, because a gallery entry's preset may be superseded by
  one the user supplies. `--strict` makes it fatal: a bundle that contributed
  nothing yields a wrapper that looks complete and is missing every value it
  held, which is the one failure a reader cannot detect.
- `atelier presets lint --module <dir> <file.tfvars>…` checks a bundle against a
  module's `variables.tf` without applying it: it reports names the module does
  not declare, keys nested inside object values that the object type does not
  declare (as dotted paths such as `worker.resources`), and scalar type
  mismatches, exiting non-zero on any finding. It is the CI gate for a committed
  bundle — `--strict` covers the apply path, while `lint` covers a bundle that is
  not being applied.
- **The TUI can write these bundles.** `S` saves the current non-default
  configuration as a new `atelier.presets/<name>.tfvars` in the
  wrapper directory. The `atelier.local.yaml` mechanism is no longer used by the
  TUI (see §11).

### 6.9 `atelier apply` — the one-liner

`atelier apply <git-url|gallery-name>` is the shortest path from a module URL to
running infrastructure. It does the `mkdir && cd` and the `terraform init &&
terraform apply` a user would otherwise do by hand, so "just apply this module"
is one command. See [ADR-0034](adr/0034-module-apply-one-liner.md).

```
atelier apply https://github.com/canonical/observability-stack.git \
  --module terraform/cos-lite --var model_uuid=<MODEL_UUID>
```

The target — `--dir`, else the current directory — decides the shape of the
run. **A target that already holds a `main.tf` composes**: the module block is
appended to that wrapper and `terraform init`/`apply` run in that root, exactly
as `add` does ([ADR-0044](adr/0044-dir-names-the-wrapper.md)). So

```
atelier apply cos-lite                       # new wrapper in ./cos-lite/
atelier apply charmed-spark --dir cos-lite   # composes, then deploys both
```

deploy both modules from one state, and running `atelier apply <url>` with a
wrapper as the CWD deploys that wrapper rather than nesting a second root inside
it. Only a `main.tf` opts in; any other non-empty target is refused, as below.

Note that `--dir` names the directory *literally*: `atelier apply cos-lite --dir
stack/` puts the wrapper in `stack/`, not in `stack/cos-lite/`, and the
candidate-derived name applies only when no `--dir`/`--as` is given.

Sequence, for a target with no `main.tf`:

1. Clone the module and discover candidates, exactly as `atelier add` does
   (same `--module`, `--ref`, `--var-file`, `--var`, `--as`, and candidate
   picker).
2. Choose a **target directory** and create it. By default it is named after
   the module candidate (`terraform/cos-lite` → `cos-lite`; a generic
   `terraform/` candidate falls back to the repository name), with `--as` or
   `--dir <path>` overriding. `--as` names both the directory and the HCL
   block, so a hyphenated `--as my-prom` gives directory `my-prom` and block
   `my_prom` (HCL identifiers cannot contain hyphens). The clone is staged next
   to the target and renamed into place, so there is exactly one clone.
3. Write the wrapper (bootstrap) into the target.
4. Run `terraform init`, then **`terraform apply`**.

The apply is interruptible: Ctrl-C is delivered to Terraform, which cancels and
persists what it had created; a second Ctrl-C, or a Terraform that will not stop,
is ended after a short grace. Atelier does not hard-kill it on the first interrupt.

For a target that already holds a `main.tf`, steps 2 and 3 are replaced by a
compose into that wrapper — same clone, candidate discovery, and
`--var`/`--var-file` handling — and step 4 runs in that target. What the wrapper
already declares decides whether its block is updated or a new one appended:
see §6.9.1.

### 6.9.1 `atelier apply` converges on the block it finds

A compose identifies the block to write **by module, not by ref**. A block *is*
the module being applied when its repository and sub-directory match, at any ref:
the ref is what an apply may change, not what makes a block a different module.

| the wrapper declares | `atelier apply` | `atelier add` |
| --- | --- | --- |
| this module once | **update that block in place** | refuse as a duplicate |
| this module 2+ times | refuse, naming them; `--as` picks one | refuse as a duplicate |
| no such module | append a new block | append a new block |

So the three cases that matter:

- **Re-deploying an identical declaration is idempotent.** Running the same
  command again updates the block with the values it already holds and deploys.
- **A different `--ref` re-points the block** rather than appending a second one.
  Inputs the new revision still declares are carried over, ones it dropped are
  removed, and ones it adds are reported.
- **A `--var` merges into the block's current values.** Every input the flags do
  not mention keeps the value it had. The freshly cloned module carries
  *defaults*, so an apply that did not read the block back would revert
  everything the user configured.

Carrying the block's current declaration onto the newly cloned schema is what
makes the third case safe. It is read against the new schema, so a value arrives
typed and mergeable while a wired expression or a `depends_on` is preserved
verbatim. See [ADR-0050](adr/0050-tolerant-apply-converges.md).

**`--as` selects the block to write; it never declares a second copy.** It names
an existing block of this module, which is updated in place. A name matching no
block falls back to the one block that does declare the module, keeping its own
name and reporting the name it did not use — which is what makes
`atelier apply <gallery-name>` work on a wrapper that holds the module under a
different block name, since a gallery entry supplies the name itself. When the
module is absent, the name becomes the new block's name. Pointing `--as` at a
block of a different module is refused. A genuinely separate instance is a
hand-edited block in `main.tf`; the editor picks it up on the next open, and such
a wrapper stays deployable via `atelier apply --as <block>`.

`add` stays strict: it authors, so a module the wrapper already has is refused
rather than overwriting hand-made configuration. The shared target rule of
[ADR-0044](adr/0044-dir-names-the-wrapper.md) is unchanged — a `main.tf` still
means compose.

The run reports what it changed, because a block rewritten under the same name
otherwise looks like the command did nothing: whether the block already declared
this source or was re-pointed (and from which ref), which arguments the new
revision dropped, which inputs it adds, and which arguments the sparse rule
pruned for now matching their module default.

Rewriting a block normalises it. Two things change as a side effect: an argument
that now matches its module default is removed, and a constant expression is
replaced by its value (`units = 1 + 1` comes back as `units = 2`). Both are
apply-neutral. The run reports the prune by name; it does not report the
constant fold.

The apply is **not auto-approved when a terminal is present**: Terraform prints
the plan and asks `Do you want to perform these actions?`, and the user answers.
There is no `--yes` for this command — that flag means "don't prompt", and here
the prompt *is* the confirmation ([ADR-0002](adr/0002-author-and-plan-scope.md)).

When stdin is **not** a terminal — a script, CI, or the usual
`atelier apply … < /dev/null` — there is no one to answer, so the apply
runs as `terraform apply -auto-approve -input=false`. The scaffolding steps are
non-interactive either way, so the command never needs `< /dev/null` to avoid a
TUI; the redirect only decides whether the final apply prompts.

Rules:

- The exit code says which side failed, never why — the reason is on stderr.
  `0` the wrapper was written and Terraform applied it. `1` Atelier's failure:
  bad usage, a clone that failed, a refusal, the required-input gate. Nothing was
  deployed. `2` Terraform ran and reported failure: the wrapper on disk is
  current and infrastructure may be partly applied, so this is not a safe blind
  re-run. Every other command exits `0` or `1` and carries its reason in the
  `--json` payload or on stderr ([ADR-0052](adr/0051-apply-exit-codes.md)).
- The target directory must be new, empty, or already hold a `main.tf`. Any
  other non-empty target is refused (naming `--dir`/`--as`) instead of
  scaffolded over. A target holding a `main.tf` composes rather than scaffolds,
  so the refusal and the additive case are decided by the same predicate
  `atelier add` uses.
- When composing into an existing wrapper, the §6.5 preflight findings are
  printed but not prompted over: `atelier apply` rejects `--yes`, so Terraform's
  plan prompt is the only confirmation it has, and a question it cannot answer is
  worse than none.
- If the module declares a required variable with no value, `atelier apply`
  writes the wrapper and stops before applying, naming the variable and
  suggesting `--var`. Terraform would otherwise reject the run with a less
  direct message. A required input wired to an expression
  (`model_uuid = module.loki.endpoint`) counts as set — Terraform resolves it
  even though Atelier cannot.
- The wrapper is a normal Atelier wrapper: re-open it with `atelier`, or run
  `terraform` in it directly (SPEC §4). `atelier apply` is a convenience, not a
  new artifact shape.

### 6.10 `atelier wrappers` — read-only wrapper discovery

`atelier wrappers [PATH]` lists the wrappers directly under PATH (default: the
current directory): each immediate child directory holding a `main.tf` or an
`.atelier/`, with the module block names that wrapper declares. Hidden
directories are skipped and the result is sorted by name.

It is read-only and one level deep: it does not open, plan, apply, or
otherwise address a child, and it does not recurse. It exists because
`atelier add`/`atelier apply` create sibling wrappers under a shared parent (e.g.
`tf-testing/`), and `atelier ls` is scoped to the current wrapper. See
[ADR-0036](adr/0036-wrapper-discovery.md).

### 6.11 Machine-readable output

`atelier add`, `ls`, `wrappers` and `import` accept `--json`, which writes the
command's result to stdout as JSON. What happens to the text depends on which
kind of output it is. Where the text is a report — what `add` and `import` print
about what they did — it still goes to stderr, so a run that went wrong still
explains itself. Where the text *is* the result — the table `ls` and `wrappers`
print — the payload replaces it, because both cannot share stdout. See
[ADR-0048](adr/0048-machine-readable-output.md).

`atelier add --json` exits `1` and lists the candidates on stderr when the
source matches several modules, since `--json` has to re-run to get a result.

Every payload is wrapped in one envelope:

```json
{
  "schema": 1,
  "command": "ls",
  "data": { }
}
```

`schema` is the revision of the payload shape. It changes when a field is
removed or its meaning changes; adding a field is not a change, so a consumer can
ignore what it does not recognise. A command that fails writes
`atelier: <error>` to stderr and exits `1`, with no payload.

Conventions:

- An absent optional value is `null`; an empty collection is `[]`, never `null`.
- A module block is reported as `source` (the repository or local path),
  `modulePath` (the `//subdir`) and `ref` — the three values `atelier add`
  takes. `atelier ls` prints the repository and the ref and drops the subdir;
  the payload keeps it, so the fields can be handed straight back to a command.
- `--json` loses nothing the text report has. `import --json` reports every
  unmatched live object's name, where the text report shows three per resource
  type and abbreviates long ones.
- `atelier apply` rejects `--json`: it reports Terraform's own output, which
  Atelier does not control, and a silent no-op would read as success.

| Command | `data` |
| --- | --- |
| `add` | `wrapper`, `added` (a module), `blocks` (every block in the wrapper afterwards) |
| `add --list-var-files` | `bundles`: each bundle's `name`, `path`, `source` (`local`/`repo`/`gallery`), `display`, `description`. `path` for a `repo` bundle points into the scratch clone, which is removed when the command exits; `name` is what to pass to `--var-file`. |
| `ls` | `isWrapper` (false when the directory holds no `main.tf`), `modules` |
| `wrappers` | `wrappers`: each `name`, absolute `path`, `modules` (block names) |
| `import` | `matched` (address → import ID), `imported`, `alreadyInState`, `matchedNothing`, `unresolved`, `unmatchedModule`, `unmatchedLive`, `queriedTypes`, `skippedTypes`, `dryRun`, `preview`, `postImportPlan`, `importsFile`, `queryFile`, `terraformVersion` |
| `import --list` | `terraformVersion`, `available`: each list resource's `type`, `providerKey`, `providerLocal`, `configAttrs` |

`alreadyInState` and `matchedNothing` are the point of the `import` payload.
A successful import that imported nothing is one of three states, and the
counts alone cannot tell them apart: resources were imported; every matched
resource was already in state (a re-run); or nothing matched anything the module
wants (usually a wrong model UUID or a `--query-var` that never reached the
query). `unresolved` is the fourth worth asserting on — resources that matched a
live object but whose import ID could not be built, which a later apply would
*create*, duplicating live infrastructure.

The third of those three is the only one that exits non-zero. It means the running
deployment is not the one the module describes, so a recovery job reading only the
exit code must not pass; the payload is still written, because that is where the
reason lives. A re-run — everything matched already in state — stays a success.

`preview` and `postImportPlan` are two different measurements, and a reader must not
confuse them. `preview` is the plan taken *before* the import, with the pending
imports in place: it answers "what would be imported, and what is still missing?".
It is `null` unless `--dry-run`. `postImportPlan` is the plan taken *after* the
imports **and after the normalization steps have rewritten state**, against the
state they produced: it answers "would a later apply still change anything?", which
is the question that says the import round-tripped. It is planned separately from
the plan the normalization steps read, because a step that fixes what the
configuration wants makes the plan it read stale the moment it returns.

An address in `addAddresses` or `changeAddresses` is drift a CI job can gate on.
A replaced resource is counted in both `add` and `destroy` and listed in
`addAddresses`, because Terraform destroys the live object and creates it again —
the one drift shape that must not pass unnoticed. `addAddresses` and
`changeAddresses` both omit types that can never be imported (e.g.
`terraform_data`), which are reported as `unimportableAdds` instead: a plan that
expects to rewrite bookkeeping is not drift. It carries no `toImport` — there is
nothing left to import by the time it is taken — and it is `null` when the run did
not compute it: every run without `--json` (it costs a `terraform plan`, and only
the payload has a reader for the answer), `--dry-run` (which returns before the
imports exist), and a run that imported nothing (a re-run, where everything matched
already in state). A plan failure there is printed and reported as `null`, never as
an error, because the imports are already in state by then. A reader that gates on
drift must therefore treat `null` as "not measured", not as "clean".

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

#### Multi-module grouping

When the wrapper's `main.tf` contains multiple `module {}` blocks, the left
pane groups variables by module with section headers:

```
── mimir ──────────────────
[✓] channel
[ ] s3_endpoint
── seaweedfs ──────────────
[ ] model_name
```

- Each section header is styled distinctly (bold, secondary colour) and
  rendered as `── <module-name> ──` padded with box-drawing characters.
- Headers are not selectable; the cursor skips over them.
- Within each section, variables follow the same priority sort.
- The primary module (the one Atelier was initialised against) appears first;
  secondary modules are sorted alphabetically.
- In single-module wrappers, no section headers are shown (no visual change
  from the prior single-module experience).

See [ADR-0015](adr/0015-multi-module-grouping.md).

- Each variable has a modified-vs-default marker:
  - `[ ]` — at default
  - `[✓]` — modified
  - `[✓N]` — for object variables: N fields modified out of total optional
    fields
- Required variables (no `default`) show with a distinct marker
  (e.g. `[!]` when unset, indicating user must provide a value).

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
- Editor mode: `[cos_lite] [Tab] pane  [↑↓] navigate  [P] plan  [S] save  [R] ref  [Q] quit  [?] help`
- Plan mode: `[↑↓] navigate  [Enter] toggle  [Tab] focus diff  [P] re-plan  [A] apply  [Esc] back  [?] help`
- Plan loading: `[Esc] cancel  [?] help`
- Small terminal (`height < 15`): `[?] help` only.
- Hints for `[R]`, `[A]`, `[S]`, `[W]`, `[D]` appear only when the
  corresponding feature is available.
- In multi-module wrappers, the footer shows the active module context
  (e.g. `[cos_lite]`) so the user always knows which module `R` will target.

- When a plan or validation emits errors, the first line of the error is shown
  in the footer. Terraform's full output for a plan is in `.atelier/logs/`
  (§7.7).
- On the first plan of each session, Atelier runs `terraform init` to
  ensure the module cache matches the wrapper's current source. After a ref
  switch, it uses `terraform init -upgrade` instead.

### 7.4 Ref switch view (modal)

Triggered by `R` from the left pane. In multi-module wrappers, `R` targets
the module that owns the currently selected variable (determined by
`rowEntry.ModuleIdx`). When the cursor is on a section header, `R` targets
that section's module.

The modal shows the module name prominently, the git source URL, current ref
(with resolved SHA), and an input field for the new ref. Typing filters the
remote's branches and tags below the field by case-insensitive substring
(prefix matches first); `↑`/`↓` move the highlight, `Tab` fills the field with
the highlighted ref, and `Enter` switches to whatever is typed (free text — an
arbitrary SHA or an unlisted ref — is always accepted). The field is the shared
readline cell (ADR-0020), so caret motion and word-delete apply.

```
╭─ Switch module ref ─────────────────────╮
│ Module:  cos_lite                       │
│ Source:  git::https://github.com/...    │
│ Current: main (827b891)                 │
│                                         │
│ New ref: mode▏                          │
│ ▸ cos-lite-model-topology               │
│   cos-lite-model-fixes                  │
│   1/2                                   │
╰─────────────────────────────────────────╯
```

On `Enter`, the module is re-cloned and reinitialised; a spinner shows
progress. On completion, the user returns to the editor with the new ref
active. See §5.3.1.

See [ADR-0018](adr/0018-additive-module-command.md) for the context-aware
ref switch design and [ADR-0025](adr/0025-ref-selection-matcher.md) for the
interactive ref selection.

### 7.5 Plan view (modal-ish)

Triggered by `P`. Replaces the right pane (and optionally expands across both)
with the plan output:

```
Plan: 12 to add, 0 to change, 0 to destroy.  |  State: 54 resource(s) across 8 modules

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
- Module and type rows carry a trailing tally of the actions beneath them
  (`+N ~N -N ↻N`, zero buckets omitted), so a collapsed subtree still shows how
  much work it holds. A long module path truncates; the tally does not.
- Selecting a leaf opens an attribute diff in a side pane.
- Both the plan tree and the diff pane are independently scrollable when
  content exceeds the available height. The tree scrolls with
  `↑↓/PgUp/PgDn` and `Home`/`End` for top/bottom. A scroll indicator
  shows position percentage when content overflows.
- The summary header shows both the plan delta and a state context line
  (total resource count and module count), read directly from
  `terraform.tfstate` without invoking terraform.
- Pressing `S` toggles between the plan diff view and a **state view** that
  shows the full resource tree from the current state with attribute values
  in the right pane. When the plan has no changes, the state view is shown
  automatically.
- Pressing `A` from the plan view applies the cached plan file. The TUI
  releases the terminal so Terraform prints its own progress and any provider
  prompts, then returns to the plan view. Success invalidates the plan (since
  the infrastructure now matches) and reloads the state. `A` is the
  confirmation — the plan has already been read in the tree — so the apply
  runs `terraform apply -auto-approve -input=false <plan file>` and does not
  prompt a second time. The flags come before the plan file: Terraform accepts
  one positional argument and treats a trailing flag as a second. A failure is
  already on screen; the status bar says only that it failed.
- A failed `P` opens a failure view showing terraform's first error line and the
  path to the full output (`.atelier/logs/tf-stderr.log`); `Esc` returns to the
  editor and `P` re-plans. See §7.7.
- `Esc` returns to the editor.
- Inline per-attribute diffs *inside* tree nodes are not yet implemented; see
  [ADR-0011](adr/0011-plan-output-tree.md).

See [ADR-0006](adr/0006-two-pane-ui-layout.md), [ADR-0011](adr/0011-plan-output-tree.md),
[ADR-0014](adr/0014-unified-layout-budget.md), and
[ADR-0052](adr/0052-hand-the-terminal-to-terraform.md).

### 7.6 Output view

Not implemented. Earlier drafts specified an `O`-triggered modal and a
generated `outputs.tf`; neither exists in the code today. `terraform output`
still works by running it directly in the wrapper. Tracked in
[ROADMAP.md](ROADMAP.md).

### 7.7 Terraform output

There is no in-TUI logs view. Apply output reaches the user because `A` releases
the terminal to Terraform ([ADR-0052](adr/0052-hand-the-terminal-to-terraform.md)),
and both the TUI's plan and the CLI's `init`/`apply` leave their output on disk:

```
.atelier/logs/tf-stderr.log    errors, warnings, diagnostics
.atelier/logs/tf-stdout.log    phase progress and resource operations
.atelier/logs/tf-trace.log     terraform's own TRACE log (ATELIER_DEBUG only)
```

Both files are appended, never truncated, so a record survives across sessions.
Each action writes a block around its output. The header names the terraform
command, the wrapper directory, and an RFC3339 timestamp, e.g.
`=== 2026-10-08T14:47:02-04:00 terraform apply (/home/me/proj) ===`; the stdout
and stderr halves of one action share the same header, so they pair up. The
header is written only when the action actually produces output on that stream,
and a `... finished ===` line closes a block that has one, so an empty action
leaves no orphan header. The logs are plain text — ANSI color escapes are
stripped from what is appended, while a terminal the CLI apply mirrors to keeps
its color. The TUI's `init`, `plan`, and ref-switch `init -upgrade` stream only
to the logs (the TUI renders the plan tree itself); the CLI `apply` streams to
the terminal and mirrors to the logs, so an apply that fails after Terraform
printed an error still leaves that error behind to read.

A failed plan opens the failure view (§7.5), which names
`.atelier/logs/tf-stderr.log`; the diagnostics are there. `.atelier/` is
internal and regenerable state — deleting it loses the logs, not the wrapper.

See [ADR-0029](adr/0029-live-logs-view.md) for the superseded in-TUI view and
[ADR-0052](adr/0052-hand-the-terminal-to-terraform.md) for the decision that
replaced it.

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
| `map(string)` as an object field          | one editable `key = value` line per entry; `Enter` adds a line, `Alt+Delete` removes one |
| `map(object(...))`                        | rows of `[key] [edit ▸] [-]`, drill into a sub-form for the object value                |
| `list(string)` / `list(number)`           | one editable line per entry; `Enter` adds a line, `Alt+Delete` removes one               |
| `list(object(...))`                       | read-only: entry count and a pointer to `main.tf` / `--var`                              |
| `set(string)`                             | same widget as `list(string)`; duplicates fold, header tagged `Set`                     |
| `set(object(...))`                        | read-only, as `list(object(...))`                                                        |
| `any`, `tuple([...])`                     | read-only HCL rendering with `[E]` to open `$EDITOR` on the wrapper                     |

A collection field's row in an object sub-form summarises what it holds rather
than counting entries: `config (retention_time,replicas)`, an empty collection
`(empty)`. At most three names are listed and the rest is a `+N` count
([ADR-0054](adr/0054-nested-map-string-edits-as-hcl-lines.md)).

### 8.1 Reordering

Not implemented. A scalar list keeps the order it was written in, but there is
no reorder hotkey.

### 8.2 Set semantics

A set renders tagged `Set` rather than `List` and folds duplicate entries, so
two identical lines collapse to one.

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
  validation block — the first diagnostic's summary and severity, prefixed
  `validate: `. The header carries the persistent `✓ Valid` / `N errors`
  indicator.
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

### 10.3 Generated files at bootstrap

When `atelier add` bootstraps a new wrapper, it writes:

- `main.tf` — `module "<name>" { source = "...?ref=..." }` plus required
  variable placeholders (or `# TODO` comments for required values the user
  hasn't supplied yet).
- `versions.tf` — `terraform { required_providers { ... } }` with the
  module's declared provider requirements.
- `providers.tf` — one `provider "<name>" {}` block per required provider,
  with stub attribute values the user will fill via the TUI.
- `.gitignore` — Atelier-managed entries:
  ```
  .atelier/
  .terraform/
  terraform.tfstate
  terraform.tfstate.backup
  *.tfstate
  *.tfstate.backup
  ```
- `README.md` — minimal scaffolding: what this directory is, how to apply
  (`terraform init && terraform apply`), and a note that `.atelier/` is
  internal.

See [ADR-0007](adr/0007-sparse-wrapper-write-rule.md).

## 11. Presets (`.tfvars` bundles)

Presets are named `.tfvars` bundles. Atelier discovers them from two sources
([ADR-0031](adr/0031-presets-as-tfvars-bundles.md)):

- **Personal** bundles in an `atelier.presets/` directory at any ancestor of the
  wrapper, discovered by walking up (nearest wins). One shared directory at a
  parent serves every wrapper beneath it.
- **Product** presets committed to the module repo (`<module>/presets/`; the
  older `<module>/examples/` location is still supported for runnable examples
  and existing value files, and `presets/` wins on a name collision).

`--list-var-files` lists both sources (source-labelled, with the description
from each file's leading comment) and `--var-file <name>` applies one. The TUI
writes bundles with `S`, which saves the current non-default configuration as a
new `atelier.presets/<name>.tfvars`.
(§6.8); `--list-var-files` prints what is available.

Personal bundles are found at the wrapper directory itself and at every ancestor
above it, so `my_wrapper/atelier.presets/foo.tfvars` works without a parent
directory; a bundle nearer the wrapper overrides a same-named one further up.

## 12. Provider configuration

The wrapper must contain `provider "<name>" {}` blocks for any provider the
module requires. Atelier derives these from the module's declared
`required_providers` and writes empty stub blocks into `providers.tf`; the user
fills their attributes in by hand.

Schema-driven provider configuration — reading the provider's configuration
schema via `terraform providers schema -json` and presenting its attributes as
a top-level pseudo-group in the left pane (`Provider: <name>`) — is specified
by [ADR-0008](adr/0008-provider-schema-discovery.md) but **not yet
implemented**. See [ROADMAP.md](ROADMAP.md).

### 12.1 Sensitive provider attributes

Not implemented, because schema-driven provider configuration is not (see
above). When it lands, attributes flagged `sensitive: true` would be written as
literal arguments like any other value, with no separate secrets file, and
masked in the TUI.

## 13. Operational details

### 13.1 Auto-save

Every variable edit triggers a write to `main.tf`. There is no draft / published
distinction. The file on disk always reflects what the user sees in the TUI.

### 13.2 Plan invocation

`terraform plan` runs only on explicit user request (`P` key). It runs as a
background task; the TUI shows a spinner in the status pane while in-flight.
A new edit during an in-flight plan does not cancel it — the existing plan
finishes, the user can re-plan if needed. Plan results are cached in memory
for the session.

See [ADR-0002](adr/0002-author-and-plan-scope.md).

### 13.2.1 `terraform init` is serialised

`terraform init` runs at most once at a time per wrapper. The `P` key, the
debounced `terraform validate`, and `Apply` can all need it, and Terraform's
module installer writes into one shared `.terraform/modules/` tree — two
overlapping inits clobber each other's git packfiles and both fail with
`invalid index-pack output` or `Module installation was canceled by an
interrupt signal`.

A caller that finds an init in flight waits for it and reports its result: a
failed init surfaces to every waiter rather than letting one proceed against a
half-populated cache. A caller whose context expires while waiting returns
without running init. After a successful init the wrapper is considered
initialised and later callers return immediately.

After a ref switch (`R`), the next init runs `terraform init -upgrade`, since
only the `?ref=` query changed. A reset invalidates an init already in flight
rather than cancelling it — that run described the old module source — and the
next caller re-initialises. The `-upgrade` request is consumed only by a run
that succeeded, so a failed one retries as an `-upgrade`.

### 13.3 Error handling

| Error class                                      | Handling                                                                                        |
|--------------------------------------------------|-------------------------------------------------------------------------------------------------|
| `terraform` binary missing or version too old    | CLI-level error before TUI launch.                                                              |
| `git clone` fails (network / not found)          | CLI-level error before TUI launch.                                                              |
| `terraform init` fails at bootstrap              | CLI-level error before TUI launch.                                                              |
| `terraform validate` errors in session           | Surface the first diagnostic in the status pane; non-blocking.                                   |
| `terraform plan` fails in session                | Open the failure view with the first error line and the log path; `Esc` returns to the editor to fix and re-plan.               |
| `git ls-remote` fails when resolving ref         | Show the literal ref but hide the resolved SHA; warn in status pane; user can retry.            |

## 14. Implementation notes

### 14.1 Language and key libraries

- **Language:** Go (>= 1.21). See [ADR-0005](adr/0005-implementation-language-go.md).
- **TUI:** [`github.com/charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea),
  [`bubbles`](https://github.com/charmbracelet/bubbles),
  [`lipgloss`](https://github.com/charmbracelet/lipgloss).
- **HCL:** [`github.com/hashicorp/hcl/v2`](https://github.com/hashicorp/hcl)
  (parser, writer, AST-preserving round-trip).
- **Terraform invocation:** [`github.com/hashicorp/terraform-exec`](https://github.com/hashicorp/terraform-exec).
- **Git operations:** shell out to `git`. Atelier does not embed a git library.
- **Manifest parsing:** `gopkg.in/yaml.v3`.

### 14.2 Distribution

- Single static binary. Release tarballs for `linux/amd64` and `linux/arm64`
  at minimum.
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

## 15. Inter-module wiring

**Not implemented.** This section is the specification for a feature that has no
code today: Atelier does not parse `output` blocks, so it knows nothing about
what the modules in a wrapper expose. What *is* implemented is preservation of
a reference the user hand-writes — a variable whose value is an expression
Atelier cannot evaluate (`module.x.y`, `data.juju_model.z.uuid`) is stored
verbatim and re-emitted on every save, shown as `[→]` in the list pane and
`wired to expression` in the editor ([ADR-0007](adr/0007-sparse-wrapper-write-rule.md)
§10.2). Everything below describes what Atelier should additionally *offer*.
Tracked in [ROADMAP.md](ROADMAP.md).

When the wrapper contains multiple modules, Atelier should offer **wire
suggestions** — type-compatible output→input connections between modules.
See [ADR-0017](adr/0017-inter-module-wiring.md).

### 15.1 Wire suggestions in the editor

When the user focuses a variable in module B, and another module in the
wrapper declares an output whose type is assignable to the variable's type,
a wire suggestion appears below the editor widget:

```
model_name (string, required)
  ╰─ Wire to: module.cos_lite.model_name (string)
```

Suggestions are sorted by name similarity then alphabetically. Selecting a
suggestion writes a standard Terraform module reference:

```hcl
module "alerting" {
  source     = "..."
  model_name = module.cos_lite.model_name
}
```

### 15.2 Wire indicator

Variables wired to module references show `[→]` in the left pane instead of
`[✓]`. This distinguishes wired values from user-edited literals.

### 15.3 Unwiring

Editing a wired variable replaces the reference with a literal value.
`Ctrl+R` (reset to default) removes the wire and restores the default.

### 15.4 Scope

- Specified for: wire suggestions for scalar types (`string`, `number`, `bool`).
- Future: collection and object type wiring.

## 16. Open questions

These are minor and can be settled during implementation; flagged here so
they don't get lost.

- **Provider lock file (`.terraform.lock.hcl`)**: generated by `terraform
  init`. Should Atelier surface it in the TUI? Probably no — it's a Terraform
  artifact, not an Atelier artifact. The user manages it like any other
  Terraform project would.
- **Module updates**: when `terraform init -upgrade` is needed (e.g. provider
  upgrades). Atelier leaves this to the user; the README mentions it.
- **Wrapper naming**: the default `module "<name>"` block name uses the
  module candidate's directory basename (e.g., `cos-lite` → `module "cos_lite"`).
  Not yet configurable.
- **Multiple module instances**: not supported. A user who wants two
  COS Lite deployments uses two wrapper directories.
