---
name: Gallery bump
description: >-
  Bump a pinned module revision in the bundled gallery and refresh its presets
  and requires when `atelier gallery lint` or `just gallery-check` fails. Use
  when a gallery entry's ref, presets, or required-input list has drifted from
  its module.
---

# Bump a gallery entry

`just gallery-bump` — and the `gallery-schedule` workflow that runs it weekly —
already rewrites each entry's `ref` to its module's current tracked ref and opens
a pull request when one moved
([ADR-0040](../../../docs/adr/0040-automated-gallery-refresh.md)). This skill
covers what the bump deliberately does not do: correcting the entry when the new
pin invalidated it. Run it on that pull request, or by hand after a check
failure.

Each entry pins a module to a full commit SHA in `internal/gallery/gallery.json`
([ADR-0035](../../../docs/adr/0035-bundled-module-gallery.md)), composes zero or
more presets from `internal/gallery/presets/`, and lists the inputs it leaves to
the user under `requires`
([ADR-0039](../../../docs/adr/0039-composed-gallery-presets.md)). A pinned SHA
freezes the schema, so the checks only fail when you move the pin, or when the
module's own dependencies change under it.

## Workflow

1. Identify the failing entry. `atelier gallery lint` reports a required input
   nobody supplies, or a `requires` entry the module no longer declares;
   `just gallery-check` reports binding or validation failures (each block is
   headed `==> <command>`). The module URL, subdir, ref, presets, and requires
   are in `internal/gallery/gallery.json`.

2. Resolve the new full SHA:

   ```bash
   git ls-remote https://github.com/canonical/<repo> HEAD
   # or a tag: git ls-remote --tags https://github.com/canonical/<repo> <tag>
   ```

3. Re-run the entry's scaffold command with the new `--ref` in a scratch
   directory and read the binding warnings:

   ```bash
   atelier gallery list --commands   # copy the entry's line, then edit --ref
   ```

   `--var-file` binding warnings name any variable the module renamed or
   retyped.

4. Update the presets (`internal/gallery/presets/<preset>.tfvars`) for any renamed
   variable or nested object key, then lint them directly against the module
   Terraform downloaded:

   ```bash
   atelier presets lint --module "$scratch"/.terraform/modules/<block>/... \
     internal/gallery/presets/<preset>.tfvars
   ```

5. If the module gained a required input, add it to the entry's `requires` —
   bare `name` when any plausible value works, `name=value` when the variable's
   own `validation` rules demand a specific value. If it dropped a required
   input, remove the stale `requires` entry. `atelier gallery lint` confirms
   coverage either way.

6. Update `ref` — and `block`/`presets` if they changed — in
   `internal/gallery/gallery.json`, run `atelier gallery lint && just
   gallery-check`, and commit. Keep one entry per commit so the diff names the
   module it bumps.