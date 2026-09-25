# internal/wrapper

Core domain package: the in-memory model of a wrapper and the rules for
turning it into files on disk. Read the root [AGENTS.md](../../AGENTS.md)
first; this file only adds what is local to the package.

## File map

| File | Responsibility |
| --- | --- |
| `state.go` | `State` — the in-memory wrapper: directory, module, variables, values. |
| `sparse.go` | `ShouldEmit` — the ADR-0007 sparse-plus-required decision. The single source of that rule. |
| `write.go` | Managed filenames; `RenderMain` and writing `main.tf`. |
| `tfvars.go` | Opt-in pass-through shape (ADR-0031): `variables.tf` mirroring, forwarding `main.tf`, and `terraform.tfvars` read/write + mode detection. |
| `read.go` | `ParsedMain` — structured view of an existing `main.tf`. |
| `bootstrap.go` | First-run file creation (`versions.tf`, `providers.tf`, `.gitignore`, `README.md`). |
| `scan.go` | Summary of Terraform declarations already present, for collision safety. |
| `remove.go` | Removing a `module` block. |

## Invariants

- **Sparse-plus-required** ([ADR-0007](../../docs/adr/0007-sparse-wrapper-write-rule.md))
  lives only in `sparse.go`. Do not re-derive "should this be emitted?"
  anywhere else; call `ShouldEmit`.
- **The wrapper is the durable artifact** ([ADR-0001](../../docs/adr/0001-wrapper-as-durable-artifact.md)).
  Everything under `.atelier/` is internal and regenerable. The wrapper must
  run with Atelier uninstalled.
- **Bootstrap writes once; later writes only touch `main.tf`.** `versions.tf`,
  `providers.tf`, `.gitignore`, and `README.md` are generated at bootstrap and
  then left alone because the user may have edited them. Exception: in the
  opt-in pass-through shape (ADR-0031), `variables.tf`, the forwarding
  `main.tf`, and `terraform.tfvars` are all derived and rewritten on every save
  by `writeTFVarsMode`.
- **Hand edits and comments are preserved.** Reads parse existing HCL and
  writes are AST-backed (`hclwrite`); never regenerate `main.tf` from scratch
  in a way that discards user content.
- **Writes are atomic** (temp file + rename) because the TUI's `terraform
  validate` watcher can race a write.
- Multiple `module` blocks in one wrapper are not supported.

## Testing

Table-driven tests are colocated (`sparse_test.go`, `write_test.go`,
`read_test.go`, `bootstrap_test.go`, ...). Round-trip and golden-style tests
are the norm: write, re-read, assert. Run `go test ./internal/wrapper/...`.
