# ADR-0028: Provider-specific import IDs — scoped to Juju for v1

## Status

Accepted

## Context

[ADR-0027](0027-atelier-import.md) adds `atelier import`, which
reconstructs Terraform state for an existing module from a running deployment.
The flow is: discover live resources via `terraform query`, plan the module to
find import candidates, match by resource type and name, and run
`terraform import <address> <id>` for each match.

The import ID is inherently provider-specific. The Juju provider documents:

```
terraform import juju_application.wordpress <model-uuid>:wordpress
```

This is not derivable from the provider schema — the `<model_uuid>:<name>`
format is Juju-specific knowledge. For other providers the format is different
(e.g. AWS uses ARNs, GCP uses numeric IDs).

Post-import normalization steps (null attribute patching, schema version
injection, model UUID injection) are also provider-specific: they address
workarounds for provider bugs or storage quirks that other providers don't
have.

## Decision

**Provider-specific code is isolated behind two extension points:**

1. **`PostImportStep` interface** — provider-specific normalization that runs
   after `terraform import` but before the user runs `plan`/`apply`. Each
   provider implements the steps its resources need; the pipeline calls them
   in order.

2. **`ImportIDFunc`** — a function type that constructs the provider-specific
   import ID string for a matched resource. Each provider supplies its own
   implementation.

### What is provider-specific

All provider-specific code lives in dedicated files, clearly named:

- **`internal/importer/juju_steps.go`** — Three `PostImportStep` implementations:
  - `JujuNullNormalization` — replaces null state values with non-null defaults
    (Juju stores null for empty maps; the module defaults are `{}`).
  - `JujuSchemaVersions` — sets `schema_version` for resource types where
    the provider declares a non-zero `Version` but doesn't implement
    `UpgradeState()`.
  - `JujuModelUUIDInjection` — injects the model UUID into the wrapper so
    `terraform plan` sees a concrete `model_uuid` instead of
    `"(known after apply)"`, preventing RequiresReplace cascades.
- **`internal/importer/juju_steps.go`** — `JujuBuildImportID` function that
  constructs `<model_uuid>:<app_name>` for `juju_application` and
  `<model_uuid>` for `juju_model`.
- **`internal/state/juju.go`** — `ExtractModelUUID` and `ExtractModelName`
  methods on `*State` (Juju-specific state extraction).
- **`internal/wrapper/juju.go`** — `InjectModelUUID` method on `*State`
  (Juju-specific wrapper variable injection).
- **`cmd/atelier/import.go`** — Provider detection via
  `strings.Contains(provider, "juju")` in the CLI layer, wiring Juju steps
  and `JujuBuildImportID` into the importer `Options`.

### What remains generic

Everything else is provider-agnostic:

- **Discovery:** `terraform query` list-resource enumeration is schema-driven.
- **Matching:** resource type + name matching works for any provider. Matching
  uses a three-phase strategy:
  1. **Identity match** (preferred): when the planned resource has a provider-
     declared identity (TF 1.14+), find live objects whose identity matches
     on all shared keys. If exactly one matches, use it.
  2. **Name-based fallback:** match by display name against the Terraform
     resource label or the planned attribute `name`.
  3. **Attribute-based match** for integration/offer resources: match by
     `application` + `endpoint` pair when the planned and live resources
     share those attributes.
- **Import execution:** `terraform import <address> <id>` is a generic Terraform
  command.
- **tfquery.hcl generation:** the query file is schema-driven (list blocks
  with config attributes from the provider schema).
- **Post-import pipeline:** the `PostImportStep` loop in `Generate` is
  provider-agnostic; it just calls whatever steps the caller provides.

### Adding a new provider

Adding a new provider means:

1. Implementing `PostImportStep` implementations for any provider-specific
   normalization (if needed).
2. Implementing an `ImportIDFunc` that constructs the correct import ID format.
3. Wiring the steps and function in `cmd/atelier/import.go` under the
   provider-detection block.

No architectural changes to the importer are needed.

## Consequences

- **The audit invariant softens for import.** The headline claim shifts from
  "zero provider code in the binary" to "provider code exists only in
  dedicated files (`juju_steps.go`, `juju.go`), scoped to import." This is
  a bounded, documented deviation.
- **Unsupported resource types are reported gracefully.** When
  `BuildImportID` returns empty (missing config or unknown type), the
  resource is reported as unmatched rather than erroring. The user can
  import it manually.
- **The Juju scope is explicit.** The command's help text, ADR, and error
  messages state that v1 supports Juju applications. Users on other providers
  know to expect it later.
- **Identity matching improves genericity.** When providers support TF 1.14+
  resource identity, matching uses provider-declared identity rather than
  name heuristics, making matching more reliable across all providers.
- **Attribute-based matching handles integrations/offers.** Phase 3 of the
  matching strategy uses `application` + `endpoint` pairs to match
  `juju_integration` and `juju_offer` resources that can't be identified by
  name alone. This is currently scoped to Juju resource types but the pattern
  (attribute-based matching for non-named resources) is generic.
