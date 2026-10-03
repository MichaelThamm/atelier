# internal/wrapper

Core domain package: the in-memory model of a wrapper and the rules for
turning it into files on disk. Read the root [AGENTS.md](../../AGENTS.md)
first; this file only adds what is local to the package.

## File map

| File | Responsibility |
| --- | --- |
| `state.go` | `State` — the in-memory wrapper: directory, module, variables, values. |
| `sparse.go` | `ShouldEmit` — the ADR-0007 sparse-plus-required decision. The single source of that rule. |
| `apply.go` | `ApplyVarOverrides` / `ApplyVarFiles` / `ConvertStringToCty` — the one home for `--var` / `--var-file` value application. |
| `write.go` | Managed filenames; `RenderMain` and writing `main.tf`. |
| `tfvars.go` | `.tfvars` preset handling (ADR-0031): reading a bundle against a module schema with binding diagnostics, rendering a bundle from current values (`RenderTFVarsValues`), and `PresetsDir`. |
| `read.go` | `ParsedMain` — structured view of an existing `main.tf`. |
| `bootstrap.go` | First-run file creation (`versions.tf`, `providers.tf`, `.gitignore`, `README.md`). |
| `scan.go` | Summary of Terraform declarations already present, for collision safety. |
| `remove.go` | Removing a `module` block. |

## Invariants

- **Sparse-plus-required** ([ADR-0007](../../docs/adr/0007-sparse-wrapper-write-rule.md))
  lives only in `sparse.go`. Do not re-derive "should this be emitted?"
  anywhere else; call `ShouldEmit`.
- **Value application lives only in `apply.go`.** Converting a string to a
  variable's declared type, deep-merging object overrides, and reading a
  `.tfvars` bundle against the schema are all here; the CLI and TUI call these
  rather than re-deriving them.
- **The wrapper is the durable artifact** ([ADR-0001](../../docs/adr/0001-wrapper-as-durable-artifact.md)).
  Everything under `.atelier/` is internal and regenerable. The wrapper must
  run with Atelier uninstalled.
- **Bootstrap writes once; later writes only touch `main.tf`.** `versions.tf`,
  `providers.tf`, `.gitignore`, and `README.md` are generated at bootstrap and
  then left alone because the user may have edited them.
- **Hand edits and comments are preserved.** Reads parse existing HCL and
  writes are AST-backed (`hclwrite`); never regenerate `main.tf` from scratch
  in a way that discards user content.
- **Writes are atomic** (temp file + rename) because the TUI's `terraform
  validate` watcher can race a write.
- **One `State` per `module` block; a wrapper may hold several**
  ([ADR-0015](../../docs/adr/0015-multi-module-grouping.md)). `ModuleBlockName`
  selects the block that `RenderMain`, `RenameModuleBlock`, and `RemoveModuleBlock`
  act on, so a write never reaches a sibling's arguments — and the sparse rule
  is per module, not per wrapper.
- **`versions.tf` reflects the module that bootstrapped the wrapper.**
  `Bootstrap` runs once, from the first module; a module composed into an
  existing wrapper never rewrites it, which is why applying a composed wrapper
  needs `terraform init -upgrade`
  ([ADR-0044](../../docs/adr/0044-dir-names-the-wrapper.md)).

## Testing

Table-driven tests are colocated (`sparse_test.go`, `write_test.go`,
`read_test.go`, `bootstrap_test.go`, ...). Round-trip and golden-style tests
are the norm: write, re-read, assert. Run `go test ./internal/wrapper/...`.
