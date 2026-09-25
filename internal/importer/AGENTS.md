# internal/importer

`atelier import`: pull a running deployment under Terraform management. Read
the root [AGENTS.md](../../AGENTS.md) first; this file only adds what is local
to the package.

## File map

| File | Responsibility |
| --- | --- |
| `importer.go` | Pipeline (`Discover`, `Generate`) and the `PreflightStep` / `PlanCheck` / `PostImportStep` / `ImportIDFunc` contracts. |
| `query.go` | Schema-derived list resources, `*.tfquery.hcl` generation, `terraform query -generate-config-out`. |
| `match.go` | Match live resources to the module's planned resource addresses. |
| `apply.go` | Run `terraform import` for each match. |
| `imports.go` | The generated declarative-import artifact (`imports.tf`). |
| `normalize.go` | Reconcile null/empty representation diffs after import. |
| `hints.go` | Turn common terraform errors into actionable guidance. |
| `scaffold.go` | Scaffold provider configuration when the target lacks it. |
| `providers/` | The provider extension point: `Provider` interface and registry. |
| `providers/juju/` | The Juju implementation; the canonical example of the contract. |

## Invariants

- **Provider-agnostic core** ([ADR-0027](../../docs/adr/0027-atelier-import.md)).
  The importable resource types and their arguments come from the provider's
  own schema, never from a hardcoded list of resource names.
- **Provider-specific behavior lives behind the `Provider` interface** in
  `providers/`, invoked at fixed points of the pipeline. Adding a provider
  must not touch core files — extend the interface and register it in
  `providers.All()`.
- **Core does not cross-reference or prune generated resources**
  ([ADR-0028](../../docs/adr/0028-provider-specific-import-ids.md)); that
  needs provider knowledge and stays in a provider implementation.
- **Import is state-only.** It must never modify infrastructure. `--dry-run`
  is a first-class path and must stay safe and side-effect free.
- Normalization is *representational* only: it reconciles null vs. empty
  collections, never semantic drift.

## Testing

Focused tests per file (`match_test.go`, `apply_test.go`, `hints_test.go`,
`query_test.go`, `normalize_test.go`, ...), with provider-specific tests under
`providers/juju/`. The pipeline is built to accept fake appliers and tfexec,
so most paths are covered without a real cloud. Run
`go test ./internal/importer/...`.
