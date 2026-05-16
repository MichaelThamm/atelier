# Atelier

A terminal UI for configuring Terraform modules.

Atelier sits between you and `terraform`. Point it at a Terraform module
(typically a public git repo) and it presents the module's variables as a
visual configuration surface — checkboxes for booleans, text inputs for
strings, sub-forms for object types, key-value rows for maps. When you're
done, Atelier writes a small wrapper Terraform project to the current
directory: a `main.tf` calling the module via its git source, your variable
overrides, plus `versions.tf`, `providers.tf`, `.gitignore`, and `README.md`.

Atelier does **not** run `terraform apply`. The wrapper is a normal Terraform
project that you (or your CI) apply through whatever workflow you already
use.

Design docs and ADRs live in [`docs/`](docs/) — start with [`docs/README.md`](docs/README.md)
and [`docs/SPEC.md`](docs/SPEC.md) for the full picture.

## Status

Pre-release. The core data model, HCL read/write, candidate discovery, git
and terraform integration, manifest parsing, session handling, the plan
view, and a working TUI are all implemented and tested (116 tests across 11
packages). Known gaps in this build:

- Provider attributes from `terraform providers schema -json` are fetched
  via [`internal/tfexec`](internal/tfexec) but the bootstrap doesn't yet
  populate `providers.tf` from them — you'll fill in provider config
  manually for now.
- `terraform validate` debounce on edit (ADR-0012) isn't connected, so the
  status bar always reads `✓ Valid`.

The wrapper format and on-disk semantics are stable; the gaps above are
additive and will land without changing what's already on disk.

## Prerequisites

- **Go** ≥ 1.22 (for building from source)
- **Terraform** ≥ 1.5 (OpenTofu works too) on `$PATH`
- **git** on `$PATH`

## Install

```sh
go install github.com/canonical/atelier/cmd/atelier@latest
# Or from a local checkout:
git clone https://github.com/canonical/atelier && cd atelier
go build -o /usr/local/bin/atelier ./cmd/atelier
```

That's the entire install. The binary is statically linked Go with no
runtime dependencies beyond `terraform` and `git`.

## Quickstart

### Configure COS Lite from the canonical observability-stack repo

```sh
mkdir -p ~/cos-lite-wrapper && cd ~/cos-lite-wrapper

atelier init https://github.com/canonical/observability-stack.git \
  --module terraform/cos-lite \
  --ref main
```

Atelier will:

1. Shallow-clone the repo into `.atelier/clone/observability-stack/`.
2. Resolve `main` to a commit SHA via `git ls-remote`.
3. Parse the module's `variables.tf` and `versions.tf`.
4. Write `main.tf`, `providers.tf`, `versions.tf`, `variables.tf`,
   `.gitignore`, `README.md` into the current directory.
5. Save `.atelier/session.json`.
6. Open the two-pane TUI.

If the repo has only one module candidate, `--module` is optional. The
observability-stack repo has three (`terraform/cos-lite`, `terraform/cos`,
`terraform/cos-dev`); without `--module` Atelier prints the list and asks
you to pick.

### Bootstrap from a local module (no network)

```sh
git clone --depth 1 https://github.com/canonical/observability-stack.git ~/obs-stack

mkdir -p ~/cos-lite-local && cd ~/cos-lite-local
atelier init --source ~/obs-stack/terraform/cos-lite
```

### Re-open a wrapper

The wrapper directory is durable. Re-open it any time:

```sh
cd ~/cos-lite-wrapper
atelier
```

If `.atelier/` has been deleted (e.g. a colleague just cloned the wrapper
from git), Atelier rehydrates automatically: it parses `main.tf`, re-clones
the module, repopulates `.atelier/`, and opens the TUI normally.

### Apply the wrapper

The wrapper is a standard Terraform project. Atelier is out of the loop
once you exit:

```sh
cd ~/cos-lite-wrapper
terraform init
terraform plan
terraform apply
```

Works on any machine with Terraform — Atelier need not be installed.

## TUI keybindings

### Editor mode

| Key                   | Action                                          |
|-----------------------|-------------------------------------------------|
| `↑` / `↓` / `k` / `j` | Move cursor in variable list                    |
| `→` / `Enter` / `l`   | Focus the editor (right pane)                   |
| `←` / `Esc`           | Return focus to the variable list (left pane)   |
| `Tab`                 | Toggle focus between panes                      |
| `space`               | Toggle boolean widget                           |
| `+` / `-`             | Step a number widget                            |
| `a`                   | Add a row to a list/map widget                  |
| `d`                   | Delete the last row of a list/map widget        |
| `P`                   | Run `terraform plan` and open the plan view     |
| `q`                   | Quit (only from the left pane)                  |
| `Ctrl+C`              | Quit immediately from anywhere                  |

### Plan view

| Key                   | Action                                          |
|-----------------------|-------------------------------------------------|
| `↑` / `↓` / `k` / `j` | Move cursor through the resource tree           |
| `Enter` / `space`     | Collapse / expand the focused module or type    |
| `P`                   | Re-run plan (refresh)                           |
| `Esc` / `q`           | Return to editor mode                           |

Pressing `P` runs `terraform init` (idempotent — only on first invocation,
or after a manual `rm -rf .terraform/`) then `terraform plan`. Plan output
is parsed via `terraform show -json` and rendered as a tree grouped by
module path then resource type. Selecting a resource leaf shows its
attribute-level diff in the right pane, with sensitive values masked as
`<sensitive>`.

Edits auto-save to disk on every change. There is no separate "save" step.

## What gets written

After `atelier init` against a public git source, the wrapper directory
contains:

```
~/cos-lite-wrapper/
├── main.tf              # module {} block calling the chosen module via git
├── versions.tf          # terraform { required_providers {...} }
├── providers.tf         # provider "X" {...} stubs
├── variables.tf         # only if a sensitive provider attribute is in play
├── .gitignore           # auto-generated; includes secrets.auto.tfvars
├── README.md            # one-time scaffold; safe to edit
└── .atelier/            # internal state; gitignored, regenerable
    ├── clone/           # shallow clone of the module repo
    └── session.json
```

`main.tf` follows the **sparse-plus-required** write rule
([ADR-0007](docs/adr/0007-sparse-wrapper-write-rule.md)): required variables
are always emitted (with a `null` placeholder when unset); optional
variables are emitted only when their value differs from the module's
declared default. For object variables, the rule recurses field-by-field.

Hand-edits to `main.tf` between sessions are preserved: Atelier parses the
existing file, preserves comments and unknown arguments (like `count`,
`for_each`, `providers`, `depends_on`), and only touches the attributes it
manages.

## CLI surface

```
atelier                                      Open the wrapper in CWD.
atelier init <git-url> [--ref REF] [--module SUBDIR]
                                              Bootstrap from a git URL.
atelier init --source PATH [--module SUBDIR]
                                              Bootstrap from a local path.
atelier --help                                Print help.
```

That is the complete v1 CLI surface. Notably absent: no `atelier plan` /
`atelier apply` — use `terraform` directly. See [`docs/SPEC.md`](docs/SPEC.md)
§6 for the full behaviour matrix.

## Development

```sh
go test ./...           # 89 tests; runs in <1s
go vet ./...
go build ./...
```

Integration-style tests that require `terraform` on `$PATH` skip cleanly
when the binary isn't available.

The packages, in approximate dependency order:

| Package                              | Role                                                  |
|--------------------------------------|-------------------------------------------------------|
| [`internal/tftypes`](internal/tftypes) | Variable types, value equality, sparse semantics     |
| [`internal/tfvars`](internal/tfvars)   | Parse `variable {}` blocks from a module             |
| [`internal/manifest`](internal/manifest) | Parse the optional `atelier.yaml` manifest         |
| [`internal/wrapper`](internal/wrapper) | Read/write the wrapper files; sparse-write rule      |
| [`internal/candidate`](internal/candidate) | Discover module candidates in a clone             |
| [`internal/gitops`](internal/gitops)   | Shell out to git; parse ls-remote                    |
| [`internal/tfexec`](internal/tfexec)   | Wrap terraform-exec for init/validate/plan/schema    |
| [`internal/session`](internal/session) | `.atelier/session.json` persistence                  |
| [`internal/bootstrap`](internal/bootstrap) | Orchestrate the init and rehydrate flows         |
| [`internal/tui`](internal/tui)         | Bubble Tea model, type-specific editors, view        |
| [`cmd/atelier`](cmd/atelier)           | CLI entrypoint                                       |

## Learn more

- [`docs/SPEC.md`](docs/SPEC.md) — comprehensive v1 specification.
- [`docs/ROADMAP.md`](docs/ROADMAP.md) — v1 scope, deferred items, parked threads.
- [`docs/adr/`](docs/adr/) — architecture decision records.
- [`docs/examples/cos-lite.atelier.yaml`](docs/examples/cos-lite.atelier.yaml)
  — sample manifest for module maintainers.
