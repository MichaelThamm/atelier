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

Atelier does **not** run `terraform apply`. The wrapper it produces is a normal
Terraform project that the user (or their CI) applies through whatever
workflow they already use.

## Design intent

- **Generic.** Works with any Terraform module that declares variables, not
  just COS Lite or any other Canonical product. Module-specific knowledge —
  friendly names, descriptions — comes from an optional
  `atelier.yaml` manifest the maintainer can commit alongside the module.
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
- [`examples/`](examples/) — sample manifests and wrappers.

## Status

Pre-implementation. This directory captures the design agreed during the
initial grilling session. No code yet.
