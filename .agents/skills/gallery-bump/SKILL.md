---
name: Gallery bump
description: >-
  Bump a pinned module revision in the bundled gallery and refresh its preset
  when `just gallery-check` fails. Use when a gallery entry's ref or preset has
  drifted from its module.
---

# Bump a gallery entry

Each entry pins a module to a full commit SHA in `internal/gallery/gallery.json`
([ADR-0035](../../../docs/adr/0035-bundled-module-gallery.md)). A pinned SHA
freezes the schema, so the check only fails when you move the pin, or when the
module's own dependencies change under it. The fix is to bump the `ref` and
refresh the preset.

## Workflow

1. Identify the failing entry from `just gallery-check` output (each block is
   headed `==> <command>`). The module URL, subdir, ref, and preset are in
   `internal/gallery/gallery.json`.

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
   retyped; a required variable with no value is reported by the module schema.

4. Update the preset (`internal/gallery/presets/<preset>.tfvars`) for any renamed
   variable or nested object key, then lint it directly against the module
   Terraform downloaded:

   ```bash
   atelier presets lint --module "$scratch"/.terraform/modules/<block>/... \
     internal/gallery/presets/<preset>.tfvars
   ```

5. Update `ref` — and `block`/`preset` if they changed — in
   `internal/gallery/gallery.json`, run `just gallery-check`, and commit. Keep
   one entry per commit so the diff names the module it bumps.
