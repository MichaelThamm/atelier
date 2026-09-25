# AGENTS.md

Guidance for AI agents working in the Atelier repository. Human contributors
should read [README.md](README.md), [docs/SPEC.md](docs/SPEC.md), and
[docs/adr/](docs/adr/) too — this file is the fast path, not a replacement.

## What this is

Atelier is a provider-agnostic terminal UI (Go, Bubble Tea) for configuring
Terraform modules. It treats a module's `variables.tf` as its API surface and
writes a sparse, independently-runnable Terraform wrapper into the user's
directory. One static binary; no server; no network beyond `git` and
`terraform`.

Read in this order before making non-trivial changes:

1. [docs/SPEC.md](docs/SPEC.md) — the current surface and behaviour (source of truth).
2. [docs/adr/](docs/adr/) — immutable decisions and their rationale. Read the
   index in [docs/adr/README.md](docs/adr/README.md); read any ADR that touches
   the area you're changing.
3. [docs/ROADMAP.md](docs/ROADMAP.md) — what is done, what is deliberately not
   done, and what is out of scope.

If SPEC and code disagree, the code is the bug. If a change needs a new
architectural decision, write an ADR first (see below).

## Commands

Go ≥ 1.25. Terraform on `PATH` for anything that plans or validates.

```bash
gofmt -l .            # formatting check (must print nothing)
go build ./...        # compile everything
go vet ./...          # static checks
go test -race ./...   # full unit suite (this is the default "is it green?")
go test ./internal/tui/...   # fast iteration on one package
```

The exact CI gate is `.github/workflows/ci.yml` (`build · vet · test`). Run
those four commands before declaring a change done. A `make check` wrapper is
planned; until then use the raw commands.

Integration tests live in `tests/integration/` and are tiered. The `wrapper/`
tier needs only Terraform; `prometheus/` and `import/` are marked `cloud` and
need Juju + Canonical K8s. See
[tests/integration/README.md](tests/integration/README.md) for local invocation.

## Architecture map

Entry point is `cmd/atelier` (package `main`). All logic lives under
`internal/`; keep it that way.

| Package | Responsibility |
| --- | --- |
| `internal/bootstrap` | First-run init and rehydrate flows; orchestrates wrapper creation. |
| `internal/candidate` | Heuristically discovers module candidates inside a cloned repo. |
| `internal/gitops` | Shells out to `git` to clone/fetch module sources. |
| `internal/wrapper` | Reads/writes the wrapper `main.tf`; sparse-plus-required write rule. |
| `internal/tftypes` | Models Terraform variable types and values. |
| `internal/tfvars` | Parses `variable` blocks from a module. |
| `internal/state` | Reads `terraform.tfstate` (v4) directly from disk. |
| `internal/session` | Persists in-band metadata to `.atelier/session.json`. |
| `internal/tfexec` | Narrow wrapper over `hashicorp/terraform-exec`. |
| `internal/tui` | The Bubble Tea TUI (model, view, editors, plan/preset views). |
| `internal/tidy` | `atelier tidy` — headless prune to sparse form. |
| `internal/convert` | `atelier convert` — adopting an existing module. |
| `internal/importer` | `atelier import` runtime; `providers/juju` is the only provider today. |
| `internal/manifest` | Parses the local presets file `atelier.local.yaml`. |

Design rules that recur in the ADRs and must stay true:

- **Sparse-plus-required writes** ([ADR-0007](docs/adr/0007-sparse-wrapper-write-rule.md)):
  emit required variables always; emit optionals only when they differ from
  the declared default. Preserve the user's hand-edited HCL and comments.
- **The wrapper is the artifact** ([ADR-0001](docs/adr/0001-wrapper-as-durable-artifact.md)):
  `.atelier/` is internal, regenerable state; the wrapper must run without
  Atelier installed.
- **No orchestration** ([ADR-0016](docs/adr/0016-scope-boundaries-no-orchestration.md)):
  Atelier configures one module's inputs and drives plan/apply. It is not
  Terragrunt and does not do cross-module orchestration.
- **Never read Atelier files from upstream** ([ADR-0022](docs/adr/0022-local-presets.md)):
  presets are user-owned and wrapper-local.

Three subtrees carry their own `AGENTS.md` with a local file map, invariants,
and test patterns: [`internal/wrapper/`](internal/wrapper/AGENTS.md),
[`internal/tui/`](internal/tui/AGENTS.md), and
[`internal/importer/`](internal/importer/AGENTS.md). Nested files must stay
short and limited to durable structure and invariants — not a function
inventory. Update one only when that package's responsibilities or invariants
change; if you find yourself documenting a specific function, it belongs in a
comment instead.

## Conventions

- **Commits:** conventional-style prefixes (`feat:`, `fix:`, `chore:`, `docs:`),
  imperative subject, one logical change per commit. Formatting changes must
  not be mixed into logic changes.
- **Tests:** table-driven Go tests colocated with the code as `*_test.go`. A
  behaviour change needs a test that fails without the change. Prefer small,
  named test functions over one large one. Clarity beats brevity in tests —
  they are the executable spec.
- **Comments:** optimise for the *why*, not the *what*. Encouraged:
  package/exported-symbol docs, invariants, ordering/concurrency notes, and
  rationale with an ADR cross-reference (e.g.
  `// Sparse-plus-required; see ADR-0007`). Rejected: comments that restate the
  next line, change-log or "previously did X" comments, and commented-out code.
  In an agent-native repo stale prose is worse than none — it is read as
  authoritative and copied into new code. Comment *density* in Go may be higher
  than a human-only team would choose, but **diff size stays small regardless**:
  one logical change per commit.
- **Language:** American English in new code, comments, commit messages, and
  docs (e.g. "behavior", "color"). Some existing docs predate this rule; do not
  churn prose just to change spelling, fix it only where you are already
  editing.
- **Dependencies:** think hard before adding one. The existing stack is
  Bubble Tea/Bubbles/Lip Gloss, `hcl/v2`, `terraform-exec`, `terraform-json`,
  and `go-cty`. Bump versions deliberately, not incidentally.

## Architecture Decision Records

New decisions are recorded, not debated in code comments. To add one:

1. Copy the structure of an existing ADR and name it
   `docs/adr/NNNN-short-slug.md` using the next number.
2. Sections: `## Status`, `## Context`, `## Decision`, `## Alternatives considered`,
   `## Consequences`.
3. Add a row to the index table in [docs/adr/README.md](docs/adr/README.md).
4. Statuses: `Proposed`, `Accepted`, `Deprecated`, or `Superseded by ADR-NNNN`.
5. **Accepted ADRs are immutable.** To change a decision, write a new ADR that
   supersedes it and update the old one's status and backlink.

Do not renumber ADRs. Do not edit an accepted ADR's decision; supersede it.

## Definition of done

A change is done when all of the following hold:

- `gofmt -l .` prints nothing; `go build ./...`, `go vet ./...`, and
  `go test -race ./...` all pass.
- New or changed behaviour has a colocated test that fails without the change.
- User-visible surface changes are reflected in `docs/SPEC.md`, and the README
  keybinding/prose tables if applicable.
- Any new decision is captured as an ADR, with the index updated.
- The change matches the scope boundaries above (no orchestration, no new
  configuration language, wrapper stays independently runnable).

## Do not

- Do not commit generated, vendored, or state artifacts. `.atelier/`,
  `.terraform/`, `*.tfstate`, `atelier-import.auto.tfvars`, and
  `tests/integration/.venv/` are gitignored for good reasons — and Terraform
  state can contain secrets. Do not read or index
  `docs/examples/tf-testing/**` or the vendored trees under
  `docs/examples/**/.terraform/**`; they are scratch fixtures, not source.
- Do not add a web UI, replace `terraform apply`, or support non-HCL
  configuration languages. See the "Out of scope" section of
  [docs/ROADMAP.md](docs/ROADMAP.md).
- Do not make correctness depend on an MCP server or external context service.
  The verification spine is `go test`, CI, and the ADR record.
