# ADR-0032: Upstream `.tfvars` discovery; retire `atelier.local.yaml` presets

## Status

Accepted

Supersedes [ADR-0022](0022-local-presets.md). The withdrawal of local presets
is staged: the discovery half lands now, and the removal of `atelier.local.yaml`
(and the `F`/`S` keys, `--preset`, and the `internal/manifest` package) is a
tracked follow-up that this ADR authorizes.

## Context

[ADR-0022](0022-local-presets.md) drew a hard boundary: **Atelier never reads
any file from the upstream module repository**, and reusable value bundles live
in a bespoke, user-owned `atelier.local.yaml` discovered by walking up from the
wrapper.

That boundary was drawn to keep Atelier-specific manifests out of product repos.
It has since become the main friction in two ways:

- Product teams (COS, COS Lite) want to ship canonical example values — "single
  unit dev deployment", "S3 backend configuration" — *with the module*, so that
  users and CI can apply a known-good configuration without hand-copying YAML.
- `.tfvars` is a **Terraform first-class artifact**, not an Atelier manifest. It
  is useful to the product repo with or without Atelier installed, is typed and
  validated by Terraform, and is the native `-var-file` / `*.auto.tfvars`
  mechanism. Reading it upstream is categorically different from reading an
  `atelier.yaml`.

The remaining friction is that a bespoke `.tfvars`-alternative still exists:
`atelier.local.yaml` presets. Two overlapping bundle mechanisms (YAML presets vs
`.tfvars`) means two mental models, two parsers, and two ways to seed a wrapper.

## Decision

### 1. Read upstream `.tfvars` on explicit request

Atelier may read `.tfvars` files from the cloned module repository. The read is
always **explicit and named**, never automatic: the user passes
`--var-file <name>`. A local filesystem path still wins, so `--var-file
./local.tfvars` is unchanged.

Resolution order for a bare `<name>` (first match wins):

1. `<wrapper-ancestor>/atelier.presets/<name>[.tfvars]`, nearest ancestor
   first — **personal, user-owned bundles**, the walk-up replacement for
   `atelier.local.yaml`;
2. `<module-dir>/<name>` or `<module-dir>/<name>.tfvars`
3. `<module-dir>/examples/<name>[.tfvars]`
4. `<repo-root>/terraform/examples/<name>[.tfvars]`
5. `<repo-root>/examples/<name>[.tfvars]`

A local filesystem path still wins over all of these, and a personal bundle
may shadow a product example of the same name. Several bundles may be named in
one invocation, comma-separated (`--var-file cos-s3,cos-units`) or as repeated
flags.

The `examples/` subdirectory beside the module is the recommended home for
committed value files: it co-locates a file with the schema it targets, needs no
namespacing, and mirrors the existing `tests/` sibling. Repo-level
`terraform/examples/` is supported as a fallback catalog for shared values.

If a name is not found, the error lists the `.tfvars` files discovered in the
clone, so names are discoverable rather than guessed. `--list-var-files` prints
that list without writing anything.

Committed value files must not be named `*.auto.tfvars`: those auto-apply during
`terraform test` and local development, which is the opposite of an opt-in
example. `terraform.tfvars` is reserved for the wrapper's own managed values.

An upstream file can set any value, including `sensitive` ones. It is therefore
never applied without the user naming it, and Atelier prints which resolved path
was used.

### Binding diagnostics

An attribute the module does not declare, or whose value does not fit the
declared type, is **skipped and reported** rather than silently ignored or
written. This matters because Terraform itself only *warns* on an undeclared
name in a `-var-file`, so a committed example could otherwise rot unnoticed
when a variable is renamed. `--strict` turns these reports into a hard failure.
Object and tuple types are not type-checked, because Atelier's cty view of a
type loses `optional()` metadata and converting a legitimate partial object
would produce false mismatches; Terraform reports nested-shape errors itself.
Product-repo CI should treat undeclared names as failures (see the
`check-tfvars` recipe in the observability-stack `justfile`).

### 2. `.tfvars` replaces `atelier.local.yaml` presets

There will be one bundle mechanism: Terraform variable files.

- **Shared/product values** live in the upstream module repo
  (`<module>/examples/<name>.tfvars`), applied with `--var-file <name>`.
- **Personal values** live in an `atelier.presets/` directory at any ancestor
  of the wrapper, discovered by walking up (nearer wins), so one directory
  shared across sibling wrappers serves all of them. They are applied by name
  with `--var-file <name>`, or with a local path (`--var-file ./path.tfvars`),
  and can be edited directly in the pass-through shape's `terraform.tfvars`.

`atelier.local.yaml`, the `F` preset picker, the `S` save-preset flow,
`--preset`, and the `internal/manifest` package are deprecated and will be
removed. The wrapper-local pass-through shape (`--tfvars`) remains the place to
customise values in the TUI.

### Removal (implemented)

1. `internal/manifest` and its tests are deleted.
2. `--preset` is removed from `module add` and `import`. The TUI `F`/`S` keys
   remain but operate on `.tfvars` bundles: `F` picks a local or repo bundle,
   `S` writes `atelier.presets/<name>.tfvars`.
3. Seeding from a file is entirely `--var-file` (repo-local name, walk-up
   bundle, or local path).
4. Atelier's SPEC, README, and ADR index are updated. Product-repo docs that
   reference `--preset` (e.g. `observability-stack/docs/`) remain a TODO.

## Alternatives considered

- **Keep `atelier.local.yaml` and add `.tfvars` discovery alongside it.**
  Rejected: two bundle mechanisms for the same job is the friction we are
  removing; users would have to learn both.
- **Read upstream `.tfvars` automatically (e.g. all files in `examples/`).**
  Rejected: silently applying values from a remote repository, including
  sensitive ones, is a footgun. Explicit naming keeps intent visible.
- **A repo-level `terraform/examples/` catalog with namespaced names
  (`cos-s3.tfvars`).** Supported as a fallback, but not the primary: it
  separates values from the schema they belong to and forces namespacing.
- **Keep the ADR-0022 boundary and require users to copy upstream values into a
  local file.** Rejected: defeats the purpose of product teams shipping
  known-good example values.

## Consequences

- Product repositories can publish per-module example values that both Atelier
  and vanilla Terraform consume.
- The ADR-0022 absolute ("never read an upstream file") is withdrawn; the new
  rule is narrower — Atelier reads **Terraform-native `.tfvars`, only when the
  user names them**.
- Existing users of `atelier.local.yaml` / `--preset` will need to migrate to
  `.tfvars`; this is a breaking change, staged so the discovery can land first.
- `--var-file` gains repo-local name resolution, which requires the clone
  directory and module sub-path to reach the CLI from `internal/bootstrap`.
