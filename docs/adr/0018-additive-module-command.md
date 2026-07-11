# ADR-0018: Additive `atelier module` command

## Status

Accepted — supersedes the "bootstrap from URL" path of `atelier init`.

## Context

`atelier init <url>` currently serves two purposes:

1. Bootstrap a wrapper from a module URL (new wrapper).
2. Adopt/convert an existing Terraform project (no URL).

With multi-module support (ADR-0015), users want to **add** modules to an
existing wrapper incrementally. The `init` verb implies a one-time action and
doesn't communicate additive intent. Running `atelier init <url>` on an
already-initialised wrapper errors today.

Additionally, when multiple modules exist, the `R` (ref switch) keybinding
is ambiguous — which module's ref is being switched?

## Decision

### New CLI surface

```
atelier module add <git-url> [--as <name>] [--ref <ref>] [--module <subdir>]
atelier module rm <name> [--force]
atelier module list
```

### `atelier module add`

Adds a module to the wrapper's `main.tf`:

1. If no wrapper exists (no `main.tf`, no `.atelier/`), bootstraps a fresh
   wrapper (same behaviour as today's `atelier init <url>`).
2. If a wrapper already exists, appends a new `module {}` block to `main.tf`.
3. Clones the repository, discovers candidates (same logic as init).
4. Resolves `--as <name>` for the HCL block name:
   - If provided, uses it verbatim (sanitised to valid HCL identifier).
   - If omitted, derives from the candidate directory basename (same as
     today's convention: `cos-lite` → `cos_lite`).
   - If the derived name collides with an existing block, appends `_2`, `_3`,
     etc.
5. Writes the module block with source and `?ref=`.
6. Runs `terraform init` to fetch the new module.
7. Launches (or returns to) the TUI with the new module's variables focused.

### `atelier module rm`

Removes a module from the wrapper:

1. Removes the `module "<name>" {}` block from `main.tf`.
2. Removes associated outputs from `outputs.tf`.
3. Removes the clone directory from `.atelier/clone/`.
4. Without `--force`, prompts for confirmation.
5. Does NOT run `terraform apply -destroy` — state cleanup is the user's
   responsibility (documented in output message).

### `atelier module list`

Prints a table of modules in the current wrapper:

```
NAME          SOURCE                                                         REF
cos_lite      git::https://github.com/canonical/observability-stack.git      main
alerting      git::https://github.com/canonical/alertmanager-k8s-operator    rev/42
```

No TUI launch. Useful for scripting and quick inventory.

### `atelier init` (preserved, reduced scope)

`atelier init` without a URL retains its adopt/convert behaviour:

- Adopt: existing project with git module blocks → create `.atelier/`.
- Relocate: flat root module → move to subdir, generate wrapper.

`atelier init <url>` is **removed**. The only way to add a module from a URL
is `atelier module add <url>`. This avoids two entry points for the same
operation and keeps the CLI surface clean.

### TUI: context-aware `R`

With multiple modules, `R` applies to the module that owns the currently
selected variable (determined by `rowEntry.ModuleIdx`). The ref switch modal
now shows the module name prominently in its header:

```
╭─ Switch ref: cos_lite ──────────────────╮
│ Source: git::https://github.com/...     │
│ Current: main (827b891)                 │
│                                         │
│ New ref: █                              │
╰─────────────────────────────────────────╯
```

When the cursor is on a section header, `R` targets that section's module.

### TUI: status bar module indicator

The footer always shows the active module context:

```
[cos_lite] [Tab] pane  [↑↓] navigate  [P] plan  [R] ref  [Q] quit
```

## Alternatives considered

### `atelier add <url>` (top-level verb)

Simpler, but doesn't group with `rm` and `list` naturally. A subcommand tree
(`module add|rm|list`) is more extensible and clearer in `--help` output.

### Keep `atelier init <url>` as an alias

Having two entry points (`init <url>` and `module add <url>`) for the same
operation creates confusion about which to use. A single clear command is
preferred.

## Consequences

- Clear separation: `init` = adopt/convert; `module add` = add from URL.
- The additive flow works incrementally — users build up a deployment one
  module at a time.
- `module rm` and `module list` complete the lifecycle without requiring
  hand-edits.
- `R` becomes unambiguous in multi-module wrappers.
- The wrapper remains standard Terraform throughout — `module add` is just
  a convenience for writing and configuring a module block.
