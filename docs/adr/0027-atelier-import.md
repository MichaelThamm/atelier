# ADR-0027: `atelier import` — import live infrastructure into an existing module

## Status

Proposed

## Context

Atelier authors a *wrapper*: a thin `module {}` call whose `variable` overrides
are the user's statement of intent ([ADR-0001](0001-wrapper-as-durable-artifact.md),
[SPEC §1](../SPEC.md)). Every existing flow starts from a module's *variable
API* and moves forward — clone, parse variables, edit, plan, apply.

Users increasingly ask the inverse: *"I already have a running deployment I
created by hand (or with the CLI). Can Atelier help me pull it under Terraform
management?"*

The target workflow is:

1. Deploy COS (or similar) from a concrete Terraform module.
2. Remove the state file (accidentally or deliberately).
3. Run `atelier import`.
4. Continue regular module operations.

The Juju provider documents `terraform import` for this exact scenario:

```
terraform import juju_application.wordpress <model-uuid>:wordpress
```

This imports a Juju application by its resource type, model UUID, and
application name. The import ID format is provider-specific, but the workflow is
generic: discover what lives exist, match them to the module's resource
addresses, and import each one.

### Why not `terraform query -generate-config-out`?

`terraform query -generate-config-out` emits flat `resource` blocks with literal
values and matching `import {}` blocks. That is designed for the case where **no
module config exists** — it generates one from scratch. For a user who already
has an intact module, the generated flat config is a competing, literal-valued
duplicate of what the module already declares. It creates more work (manual
cross-referencing, default pruning) rather than less.

The correct approach for state reconstruction is: **import directly into the
module's existing resource addresses**, not generate a new flat root.

### The tension with Atelier's core model

This feature does not fit the wrapper model cleanly:

- **`terraform import` is provider-specific.** The import ID format
  (`<model_uuid>:<app_name>` for Juju, `arn:...` for AWS, etc.) is not
  derivable from the provider schema alone. This puts provider knowledge into
  the import flow. See [ADR-0028](0028-provider-specific-import-ids.md) for the
  decision to scope this initially to Juju applications only.
- **Import mutates state**, not just infrastructure. Atelier's apply contract
  ([ADR-0002](0002-author-and-plan-scope.md)) assumes plan-then-apply against
  a wrapper the user authored; import-adopt adopts pre-existing real resources
  into state.

## Decision

Add **`atelier import [PROVIDER]`**, a command that reconstructs Terraform
state for an existing module from a running deployment. It uses `terraform query`
to discover live resources, `terraform plan` to identify the module's import
candidates, matches them by resource type and name, and runs `terraform import`
for each.

### Behaviour

1. **Discover live resources generically.** Write a `*.tfquery.hcl` listing the
   provider's list-resource types, then run `terraform query -json` to enumerate
   every live object. The query is schema-driven: whatever the provider declares
   as list resources is what Atelier offers.
2. **Plan the existing module.** Run `terraform plan` against the module with
   empty (or partial) state. Resources the module wants to *create* are the
   import candidates — their full module addresses (e.g.
   `module.cos.juju_application.alertmanager`) are the import targets.
3. **Match by resource type and name.** Pair each planned create to a live
   object with the same resource type and display name. A match is consumed by
   at most one planned resource; zero or ambiguous matches are reported for
   manual resolution.
4. **Import via `terraform import`.** For each match, construct the
   provider-specific import ID (currently Juju:
   `<model_uuid>:<app_name>`) and run `terraform import <address> <id>`.
   The model UUID is supplied by the user via `--query-var model_uuid=<uuid>`.
5. **Report results.** List matched, imported, and unmatched resources. No
   config is generated — the module already declares everything.

### Provider-specific import IDs (ADR-0028)

The import ID format is inherently provider-specific. This is the one place
where Atelier must encode provider knowledge. Rather than abstract this behind
a generic mechanism that cannot generically exist, Atelier scopes the initial
implementation to **Juju applications** (`juju_application` resources only),
with a clear path to generalise:

- `buildImportID()` constructs the ID from the matched resource type and user-
  supplied config (`model_uuid`).
- Other providers and resource types are added by extending `buildImportID()`
  with new format cases — this is additive, not architectural.
- The first version documents the Juju-only scope clearly and fails
  gracefully for unsupported resource types (reported as unmatched).

### Scaffolding and auto-init

Same as before: when the target directory has no provider configuration,
`atelier import` scaffolds a minimal `versions.tf`/`providers.tf` from a generic
`PROVIDER` positional argument and runs `terraform init` automatically.

### Explicit non-goals

- **Cross-referencing.** Not needed: the module already cross-references its
  resources via `var.*` and resource references. Import puts state behind the
  existing config; no config changes are required.
- **Default/implicit-resource pruning.** Not attempted generically. Implicit
  resources (default storage pools, SSH keys) that the module does not declare
  are reported as unmatched-live and left alone.

## Alternatives considered

### `terraform query -generate-config-out` (original proposal)

Rejected. It generates flat resource blocks that duplicate the existing module's
config, requiring manual cross-referencing and pruning. For state
reconstruction (config already exists), direct `terraform import` is simpler and
more correct.

### `import {}` blocks + `terraform apply` (TF 1.14)

Considered but deferred. Import blocks are batchable and previewable (plan
first), but they require TF ≥ 1.14 for the blocks themselves (not just the
query), and the current `terraform import` CLI is sufficient for the initial
scope. May be revisited for bulk imports or preview workflows.

### Fully provider-agnostic import IDs

Rejected for v1. Import ID formats are not derivable from the provider schema.
A generic mechanism would need to enumerate every provider's import ID grammar,
which is disproportionate to the problem. Starting with Juju-only and
generalising incrementally is the honest approach.

## Consequences

- **Provider-specific import IDs are introduced.** The `buildImportID()` function
  encodes Juju's `<model_uuid>:<app_name>` format. This is the first place
  Atelier contains provider-specific logic for import. ADR-0028 records the
  decision to scope this initially to Juju and generalise later.
- **No config is generated.** The module already declares all resources; import
  puts state behind existing config. There is nothing to cross-reference or
  prune.
- **`terraform query` is still used for discovery** (schema-driven list-resource
  enumeration), even though the import itself uses `terraform import` CLI. The
  query infrastructure (tfquery.hcl, ListResource, schema discovery) is retained.
- **The `import {}` blocks approach is dropped** from the initial implementation.
  It may be revisited for batch-import or preview workflows in the future.
- **A per-command version gate is introduced** for `terraform query` (≥ 1.14),
  same as before. Every other Atelier command keeps working on ≥ 1.5.0.
