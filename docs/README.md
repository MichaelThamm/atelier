# Atelier

A terminal UI for configuring Terraform modules.

Atelier sits between a user and `terraform`. Point it at a Terraform module
(typically a public git repo), and it presents the module's variables as a
visual configuration surface — checkboxes for booleans, text inputs for
strings, sub-forms for object types, key-value rows for maps, and so on. When
the user is satisfied, Atelier writes out a small wrapper Terraform project in
the current directory: a `main.tf` calling the module via its git source, the
user's variable overrides, and a `.gitignore` and `README.md` to round out the
artifact.

Atelier does **not** run `terraform apply` from the command line. The wrapper
it produces is a normal Terraform project that the user (or their CI) applies
through whatever workflow they already use. However, Atelier's TUI includes a
built-in plan view (press `P`) with an optional apply action (`A`), and an
output viewer (`O`) that shows planned or live output values with syntax
highlighting.

## Design intent

- **Generic.** Works with any Terraform module that declares variables, not
  just COS Lite or any other Canonical product. Candidate discovery is purely
  heuristic; Atelier never requires or reads any file in the upstream module
  repository.
- **User-owned presets.** Reusable variable bundles live in a wrapper-local
  `atelier.local.yaml`, discovered by walking up from the wrapper directory, so
  one file can be shared across sibling wrappers. See
  [SPEC §11](SPEC.md#11-local-presets-atelierlocalyaml).
- **Wrapper-as-artifact.** The wrapper directory is the durable output. It is
  version-controllable, shareable, runnable without Atelier installed, and
  CI-compatible. Atelier's internal state lives in a `.atelier/` subdirectory
  that is regenerable from the wrapper.
- **Author-and-plan, not apply.** Atelier owns the configure → plan iteration
  loop and hands off to `terraform apply` cleanly. It does not try to replace
  the user's existing apply workflow.

## Documents in this directory

- [`SPEC.md`](SPEC.md) — comprehensive v1 specification.
- [`ROADMAP.md`](ROADMAP.md) — v1 scope, deferred items, parked threads.
- [`adr/`](adr/) — architecture decision records.
- [`examples/`](examples/) — sample `atelier.local.yaml` presets and wrappers.

## Status

Implemented. Atelier is a single Go binary built with Bubble Tea and lipgloss.
The TUI uses a Catppuccin Mocha/Latte colour palette with rounded-border
panels throughout.
