# ADR-0031: Presets as Terraform-native `.tfvars` bundles

## Status

Accepted

Supersedes [ADR-0022](0022-local-presets.md) and
[ADR-0026](0026-save-preset.md).

## Context

[ADR-0022](0022-local-presets.md) drew a hard boundary: Atelier never reads any
file from the upstream module repository, and reusable value bundles live in a
bespoke, user-owned `atelier.local.yaml`.

That boundary became friction:

- Product teams (COS, COS Lite) want to ship canonical values — "single-unit dev
  deployment", "S3 backend configuration" — *with the module*, so users and CI
  can apply a known-good configuration without hand-copying YAML.
- `.tfvars` is a Terraform first-class artifact, not an Atelier manifest: useful
  to the product repo with or without Atelier installed, typed and validated by
  Terraform, and the native `-var-file` / `*.auto.tfvars` mechanism.

Keeping `atelier.local.yaml` alongside `.tfvars` would mean two bundle
mechanisms, two parsers, and two ways to seed a wrapper.

A separate question was whether the wrapper should adopt Terraform's value
tooling through a generated pass-through shape: a `variables.tf` mirroring the
module's inputs and a `main.tf` forwarding `name = var.name`. It was rejected
(see Alternatives).

## Decision

### Presets are `.tfvars` bundles

There is one bundle mechanism: Terraform variable files. `atelier.local.yaml`,
`--preset`, and `internal/manifest` are removed. The TUI `F` picker and `S` save
flow operate on bundles: `F` applies a discovered one, `S` writes
`atelier.presets/<name>.tfvars`. Values are otherwise edited in the TUI, which
writes the classic sparse `main.tf`.

### Discovery

Atelier reads `.tfvars` from upstream only when the user names them
(`--var-file <name>`); a local filesystem path always wins. Resolution order for
a bare name (first match wins):

1. `<wrapper-ancestor>/atelier.presets/<name>[.tfvars]`, nearest ancestor first
   — personal, user-owned bundles;
2. `<module-dir>/<name>[.tfvars]`
3. `<module-dir>/presets/<name>[.tfvars]`
4. `<module-dir>/examples/<name>[.tfvars]`
5. `<repo-root>/terraform/presets/<name>[.tfvars]`
6. `<repo-root>/terraform/examples/<name>[.tfvars]`
7. `<repo-root>/presets/<name>[.tfvars]`
8. `<repo-root>/examples/<name>[.tfvars]`

`presets/` beside the module is the recommended home for committed value
bundles: dedicated to value files (so it is unambiguous against runnable
`examples/` roots), co-located with the schema it targets, and needs no
namespacing. `examples/` stays supported, and `presets/` wins on a name
collision. Several bundles may be named in one invocation, comma-separated or as
repeated flags.

Committed value files must not be named `terraform.tfvars` or `*.auto.tfvars`: a
Terraform root auto-loads those, the opposite of an opt-in bundle. If a name is
not found, the error lists the discovered bundles; `--list-var-files` prints
them without writing.

### Binding diagnostics

An attribute the module does not declare, or whose value does not fit the
declared type, is skipped and reported. Terraform only *warns* on an undeclared
name in a `-var-file`, so a committed bundle could otherwise rot unnoticed when
a variable is renamed; `--strict` makes these reports fatal. Object and tuple
types are not type-checked: Atelier's cty view loses `optional()` metadata, so a
legitimate partial object would produce false mismatches, and Terraform reports
nested-shape errors itself.

### The wrapper keeps its classic shape

Atelier has exactly one wrapper shape: the classic sparse `main.tf`
([ADR-0007](0007-sparse-wrapper-write-rule.md)). The pass-through shape is
removed entirely; `.tfvars` files are preset bundles only, not a wrapper shape.

## Alternatives considered

- **Keep `atelier.local.yaml` and add `.tfvars` discovery alongside it.**
  Rejected: two bundle mechanisms for the same job is the friction being
  removed.
- **Read upstream `.tfvars` automatically (e.g. every file in `examples/`).**
  Rejected: silently applying values from a remote repository, including
  sensitive ones, is a footgun; explicit naming keeps intent visible.
- **Keep the ADR-0022 boundary and require copying upstream values locally.**
  Rejected: it defeats product teams shipping known-good values.
- **A generated pass-through wrapper (`--tfvars`), opt-in or default.**
  Rejected: it duplicates the module's API (a second source of truth that can
  drift), turns `main.tf` into a wall of `var.*` forwards, and adds a second
  read/write path (mode detection, ref-switch propagation, a single-module
  constraint, parallel tests) for a goal the preset bundles already meet. Users
  who want a root `variables.tf` interface can hand-write one.

## Consequences

- Product repositories publish per-module value bundles that both Atelier and
  vanilla Terraform consume.
- The ADR-0022 absolute ("never read an upstream file") is withdrawn; the rule
  is narrower — Atelier reads Terraform-native `.tfvars`, only when named.
- `atelier.local.yaml` / `--preset` users must migrate to `.tfvars`; a breaking
  change, landed on one branch.
- One wrapper shape, one read/write path, fewer guards and tests.
- `--var-file` gains repo-local name resolution, so the clone directory and
  module sub-path reach the CLI from `internal/bootstrap`.
