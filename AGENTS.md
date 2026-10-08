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

1. [docs/SPEC.md](docs/SPEC.md) — the current surface and behavior (source of truth).
2. [docs/adr/](docs/adr/) — immutable decisions and their rationale. Read the
   index in [docs/adr/README.md](docs/adr/README.md); read any ADR that touches
   the area you're changing.
3. [docs/ROADMAP.md](docs/ROADMAP.md) — what is done, what is deliberately not
   done, and what is out of scope.

If SPEC and code disagree, the code is the bug. If a change needs a new
architectural decision, write an ADR first (see below).

## Commands

Go ≥ 1.25. Terraform on `PATH` for anything that plans or validates. Common
flows are wrapped in the [`justfile`](justfile); run `just --list` to see all
recipes.

```bash
just check                    # fmt-check + build + vet + race tests — the "is it green?" gate
just fmt                      # rewrite files with gofmt (never hand-format)
just test-pkg ./internal/tui  # unit tests for one package (fast iteration)
just docs-check               # ADR index / ADR-reference / markdown-link drift checks
just build-bin                # build the dev binary the integration tiers use
just test-integration         # fast integration tier (no Juju)
just test-cloud               # cloud tier: needs a Juju controller on K8s (slow)
```

CI runs these same recipes
([.github/workflows/ci.yml](.github/workflows/ci.yml)): the `build · vet · test`
job runs `just check`, and the integration jobs run `just test-integration` and
`just test-import`. If a recipe passes locally it is
the same gate CI enforces. Underneath them are `gofmt -l .`, `go build ./...`,
`go vet ./...`, and `go test -race ./...` if you prefer the raw commands.

Integration tests live in `tests/integration/` and are tiered. The `wrapper/`
tier needs only Terraform; `import/` is marked `cloud` and needs
Juju + Canonical K8s. See
[tests/integration/README.md](tests/integration/README.md) for local invocation.

## Architecture map

Entry point is `cmd/atelier` (package `main`). Product logic lives under
`internal/`; keep it that way. Repository tooling lives under `tools/`.

| Package | Responsibility |
| --- | --- |
| `internal/bootstrap` | First-run init and rehydrate flows; orchestration core: wrapper creation, module-block loading (`FreshWrapper`, `LoadSecondaryModules`). |
| `internal/candidate` | Heuristically discovers module candidates inside a cloned repo. |
| `internal/gitops` | Shells out to `git` to clone/fetch module sources. |
| `internal/modulesource` | Parses Terraform module source addresses (remote, `//subpath`, `?ref`, local paths). |
| `internal/wrapper` | Reads/writes the wrapper `main.tf`; sparse-plus-required write rule. |
| `internal/tftypes` | Models Terraform variable types and values. |
| `internal/tfvars` | Parses `variable` blocks from a module. |
| `internal/state` | Reads `terraform.tfstate` (v4) directly from disk. |
| `internal/session` | Persists in-band metadata to `.atelier/session.json`. |
| `internal/tfexec` | Narrow wrapper over `hashicorp/terraform-exec`. |
| `internal/tui` | The Bubble Tea TUI (model, view, editors, plan/preset views). |
| `internal/importer` | `atelier import` runtime; `providers/juju` is the only provider today. |

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
- **Read only Terraform-native `.tfvars` from upstream, and only when named**
  ([ADR-0031](docs/adr/0031-presets-as-tfvars-bundles.md)): presets are `.tfvars`
  bundles — personal ones in a walk-up `atelier.presets/` directory, product
  examples under `<module>/examples/`. Never read an Atelier-specific manifest
  from upstream.
- **Lean by default; a feature must earn its maintenance.** Do not add a
  feature for its own sake. A feature or fix is only worth its ongoing
  maintenance cost if it delivers clear, current value. Prefer the smaller
  change — fewer added lines and fewer moving parts is better, and
  [**reuse before you build**](#reuse-before-you-build) is the first move, not
  the last.

Three subtrees carry their own `AGENTS.md` with a local file map, invariants,
and test patterns: [`internal/wrapper/`](internal/wrapper/AGENTS.md),
[`internal/tui/`](internal/tui/AGENTS.md), and
[`internal/importer/`](internal/importer/AGENTS.md). Keep them short and
limited to durable structure and invariants — not a function inventory; if you
find yourself documenting a specific function, it belongs in a comment
instead. **The canonical reuse inventory lives only in the root file above**;
nested files name the invariants of their package, not their own copy of the
inventory, so the two cannot drift.

## Reuse before you build

The recurring failure in this repository is not missing abstractions — it is
new code that does not find the ones that already exist. Module-source parsing
was copied into four packages; the `--var`/`--var-file` value logic sat in
`package main` while `internal/wrapper` owned the state it operated on; a
700-line `internal/convert` package was left with no caller. Every one of those
was caught only by a manual audit. So, before writing a function that parses,
converts, clones, or writes, find the one that already does it:

| If you are about to… | Use |
| --- | --- |
| parse a module source (`git::`, `//subpath`, `?ref=`, local path) | `internal/modulesource` (`Decompose`, `Remote`, `ModulePath`, `Compose`, `IsLocal`, `IsFullSHA`) |
| apply `--var` / `--var-file` values, or convert a string to a variable's type | `wrapper.ApplyVarOverrides`, `wrapper.ApplyVarFiles`, `wrapper.ConvertStringToCty` |
| clone + read a module block's schema and values, or re-read after a ref change | `bootstrap.BlockLoader.LoadModuleBlock`, `bootstrap.LoadRefState` |
| write `main.tf` / any file atomically | `wrapper.RenderMain` + `State.Write`; leaf packages that cannot import `wrapper` (session, state) keep their own |
| decide whether a variable is emitted | `wrapper.ShouldEmit` / `wrapper.SparseValue` |

If you cannot name the existing function, search before writing a new one.
This table is the enforcement for duplication, backed by review — it is not
worth a bespoke linter, and the standard duplicate-code tooling is noisy enough
that projects routinely disable it. Two structural rules *are* checked
mechanically: `tools/codecheck` fails `just check` on any `internal/` package
unreachable from `cmd/`, and `tools/deadcodecheck` fails on any function
reachable from neither the binary nor a test (`deadcode -test`). Together they
catch dead code without a dependency: the second runs the pinned
`golang.org/x/tools/cmd/deadcode` at a fixed version. When a standard tool
exists (as here), prefer it to a bespoke one.

## Layering

Dependencies point downward only. The intended direction is:

```
leaves (gitops, session, state, tfexec, tftypes, tfvars, modulesource, candidate)
   → domain (wrapper)
   → orchestration (bootstrap)
   → adapters (cmd/atelier, internal/tui, internal/importer)
```

`cmd/atelier` and `internal/tui` are **adapters**: flag parsing, presentation,
and wiring. They must not own value conversion, source parsing, clone
orchestration, or filesystem write paths — those belong in the layer below. A
helper that operates on `wrapper.State` belongs in `internal/wrapper` even if
its only caller today is the CLI. When in doubt, move logic *down* into the
shared layer rather than sideways between adapters. `go vet` cannot check this;
the DAG above and review are the enforcement, so deviations need a reason in
the PR.

## Reusable procedures (skills)

On-demand, tool-neutral procedures live under `.agents/skills/` in the Agent
Skills (`SKILL.md`) format. Tools that discover skills natively (Cursor,
Copilot, OpenCode, Codex, Claude Code) pick them up automatically; agents
without native discovery should read the matching `SKILL.md` when a task
matches its description.

- [`pr-description`](.agents/skills/pr-description/SKILL.md) — draft a concise
  PR description from the branch diff, following
  [`.github/pull_request_template.md`](.github/pull_request_template.md).

## Conventions

- **Commits:** conventional-style prefixes (`feat:`, `fix:`, `chore:`, `docs:`),
  imperative subject, one logical change per commit. Formatting changes must
  not be mixed into logic changes.
- **Tests:** table-driven Go tests colocated with the code as `*_test.go`. A
  behavior change needs a test that fails without the change. Prefer small,
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
- **Keep comments short and about the code.** A comment explains what a future
  reader of *this file* needs to know; it is not a place for project history,
  motivation for a refactor, or the journey that produced the change. Those
  belong in the commit message or PR, which is where a reviewer looks for them.
  Concretely, do not write: "X was copied into four packages", "this used to
  live in Y", editorials on why a design is better, or restatements of the
  commit subject. Prefer one or two sentences over a paragraph; if a comment
  runs past a short paragraph, the extra is almost always reviewer context that
  belongs in the commit. A doc comment on an exported symbol earns a little more
  room, but still states its contract, not its backstory.
  - Bad: `// This used to be duplicated in three places and we finally
    // consolidated it; it is the one home for ...`
  - Good: `// parseCandidates reports ...` (contract), or a single invariant
    line with an ADR cross-reference.
- **Docs:** `docs/adr/README.md` is the index of record — every ADR needs a row
  there. `just check` runs `tools/docscheck`, which fails on a missing or
  inconsistent index row, an unresolved `ADR-NNNN` reference, or a broken
  relative Markdown link. Run `just docs-check` for a focused pass.
- **Public-facing output:** what a *user* reads is not the same document as what
  a *contributor* reads. The generated wrapper `README.md`, the CLI usage/help
  text, error messages, and the generated site (`website/docs/`) describe the
  product and must not reference Atelier's development artefacts — no `ADR-NNNN`,
  no `docs/SPEC.md` or `docs/ROADMAP.md`, no `internal/…` package paths, and no
  design rationale. Those belong in the commit, the PR, or the ADR they justify.
  Write that surface for someone who has never read the repo: state what the
  thing does, not why it is built that way, and drop hedging and restatement — a
  public page citing an ADR is a bug report waiting to happen. The repository
  `README.md` is the one exception: its Documentation table may link `docs/`,
  because that file is itself contributor-facing. The
  `TestPublicFacing*_hasNoInternalReferences` tests enforce the mechanical half.
- **Pull requests:** fill in `.github/pull_request_template.md`; it mirrors the
  definition of done below.
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

- `just check` passes (format check, build, vet, and race tests).
- New or changed behavior has a colocated test that fails without the change.
- User-visible surface changes are reflected in `docs/SPEC.md`; keybinding
  changes are reflected in the TUI `?` help modal (the source of truth), and in
  the README's prose where relevant.
- Any new decision is captured as an ADR, with the index updated.
- The change matches the scope boundaries above (no orchestration, no new
  configuration language, wrapper stays independently runnable).
- `just code-check` passes (no internal package unreachable from `cmd/`, no
  function unreachable from the binary and all tests).

A **refactor** additionally satisfies: it names the duplication or layering
problem it removes in the commit or PR; it does not add behavior; existing
tests prove equivalence (moved logic moves its tests with it — do not leave a
test behind asserting the moved code through its old caller); and when it
changes a shared primitive it updates the reuse table above. Sequence shared
extraction before caller thinning: extract pure primitives first, then reshape
orchestration, then thin the adapters.

## Do not

- Do not commit generated, vendored, or state artifacts. `.atelier/`,
  `.terraform/`, `tf-testing/`, `*.tfstate*`, `.terraform.lock.hcl`,
  `atelier-import.auto.tfvars`, and `.venv/` are gitignored for good reasons —
  Terraform state can contain secrets. Do not read or index those trees
  either; they are scratch fixtures, not source. If you create test fixtures,
  put them under `tf-testing/` (or another ignored path), never in a tracked
  directory.
- Do not add a web UI, replace `terraform apply`, or support non-HCL
  configuration languages. See the "Out of scope" section of
  [docs/ROADMAP.md](docs/ROADMAP.md).
- Do not make correctness depend on an MCP server or external context service.
  The verification spine is `go test`, CI, and the ADR record.
