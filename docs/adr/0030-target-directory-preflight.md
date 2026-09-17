# ADR-0030: Target-directory preflight before scaffolding

## Status

Accepted — adds a confirmation step to `atelier module add` and `atelier import
--source`, refuses to add a module the wrapper already references, introduces a
single shared confirmation helper for the CLI, and replaces filename-based
collision detection in the wrapper bootstrap with content-based detection.
Amends [ADR-0018](0018-additive-module-command.md) (adds step 0 to `module add`,
and constrains its block-name uniquification) and
[ADR-0004](0004-wrapper-layout.md) (which assumed a target directory that is
"typically empty").

## Context

`atelier module add` takes no path argument. It writes into `os.Getwd()`
unconditionally, branching only on whether `main.tf` exists. `atelier import
--source` does the same, defaulting to the CWD with `--dir` as an override.

The result is that a single mistyped `cd` — or simply forgetting which directory
a shell is in — produces `main.tf`, `versions.tf`, `providers.tf`, `README.md`,
`.gitignore`, and a `.atelier/` clone tree in the user's home directory, in the
root of an unrelated Go or npm project, or inside another wrapper. Nothing warned
about any of this, and SPEC §6.4 explicitly blessed the non-empty case as
"bootstrap; preserve existing files".

Three further problems shared the same root cause — decisions made from
filenames and existence checks rather than from content:

1. **Duplicate declarations.** `writeIfMissing` skipped `versions.tf` only if a
   file of that name existed. A directory declaring `required_providers` inside
   `main.tf` or `terraform.tf` received a second `terraform { required_providers
   {} }` block, which Terraform rejects outright ("Duplicate required providers
   configuration"). The same applied to `provider` blocks. A bootstrap intended
   to be helpful produced a root that would not initialise.

2. **Unignored Atelier state.** An existing `.gitignore` was skipped entirely, so
   `.atelier/` and `.terraform/` were left untracked-but-unignored in the user's
   repository, appearing in their `git status` and their next `git add .`.

3. **Debris after a failed bootstrap.** `.atelier/clone/…` survived a bootstrap
   that failed partway, in a directory that never became a wrapper.

Separately, the CLI had two hand-rolled `[y/N]` prompts (`purge`, `module rm`)
that disagreed on reader, output stream, and accepted answers, and neither
checked for a terminal — so in CI both blocked on a closed stdin rather than
failing with a usable message.

## Decision

### 1. A preflight that runs before any write

`module add` and `import --source` call `confirmTargetDir(dir, action, yes)`
before touching the filesystem. It collects findings from `inspectTarget(dir)`,
prints them, and prompts if any is at warning level.

Findings have two levels:

- **warning** — prompts. Emitted when the directory holds files Atelier did not
  author, contains Terraform files, sits inside another wrapper, looks like the
  root of another kind of project (`go.mod`, `package.json`, `charmcraft.yaml`,
  …), or is the user's home directory, their config directory, or `/`.
- **note** — printed, never prompts. Describes a write Atelier is about to skip,
  e.g. an existing `required_providers` block.

Hand-authored Terraform is distinguished from wrapper-shaped Terraform by block
type: Atelier only ever writes `module` (with a remote source), `terraform`, and
`provider` blocks, so a `resource`, `data`, `locals`, or `output` block — or a
module pointed at a local path — identifies the directory as somebody's own
project.

### 2. An established wrapper is never preflighted

`main.tf` **and** `.atelier/` together mean the user has already declared this
directory's purpose. Prompting there would fire on every routine `module add`.

`main.tf` alone does not qualify: a bare Terraform root is exactly the case worth
warning about, and auto-rehydration (SPEC §6.1) means Atelier's own wrappers can
also reach that state — so the warning names what will happen rather than
refusing.

### 3. Benign entries do not count as clutter

`.git/`, `.gitignore`, `README.md`, `LICENSE`, `.terraform/`, state files, and
Atelier's own artifacts are excluded from the non-empty check. A warning that
fires on a directory containing only a `README.md` teaches users to dismiss the
prompt unread, which is worse than having no prompt.

### 4. `--yes` / `-y`, and failing closed without a terminal

`--yes` skips the prompt; findings are still printed. Without a terminal on
stdin the command **fails**, naming `--yes` in the error. Proceeding silently
would deliver the clutter to CI anyway; blocking on a read would hang the job.

This required fixing `isTerminal`, which tested `os.ModeCharDevice` and therefore
called `/dev/null` a terminal — so `module add < /dev/null` read an instant EOF
instead of erroring. It now delegates to `isatty`, already in the module graph
via `termenv`.

### 5. One confirmation helper

`confirm(prompt string) (bool, error)` is the only confirmation path in the CLI.
It prompts on stderr (so piping stdout never swallows the question), accepts
`y`/`yes` case-insensitively, and consults `isTerminal(os.Stdin)`. `purge` and
`module rm` use it; `--yes` is accepted as an alias for their `--force`.

### 6. Bootstrap decides from content, and reports what it did

`wrapper.ScanDeclarations(dir)` parses the top-level `*.tf` files and returns the
`required_providers` entries, provider configurations, module blocks, and any
block types Atelier never authors. `wrapper.Bootstrap` uses it to:

- skip `versions.tf` entirely when a `required_providers` block exists anywhere,
  reporting which provider requirements the user must add themselves;
- omit from `providers.tf` any provider whose local name is already configured;
- append only the *missing* patterns to an existing `.gitignore`, under a marker
  comment.

`Bootstrap` now returns a `*wrapper.Report` listing files created and notes about
skipped writes. `InitNew` folds the notes into `Result.Warnings`, which both
`module add` and `import` already print. A silent skip is how a bootstrap ends up
producing a root that `terraform init` rejects.

### 7. Fresh bootstrap is transactional

`module add` and `import --source` record whether `.atelier/` existed before the
run and remove it on failure only if the run created it — so a retry inside a
wrapper that already had state never destroys that state.

### 8. Adding a module the wrapper already has is an error

The additive path compares the resolved module reference against every existing
block by identity — normalised remote URL, sub-directory, literal ref — and
refuses an exact match.

This is a distinct check from the directory preflight, asked at a different
point, and it deliberately does **not** go through `confirm()`.

The previous behaviour was worse than a missing prompt. `uniqueBlockName` sees
only names, and names are *derived* — for a repo whose Terraform lives under
`terraform/`, the derived name comes from the repository basename, so
`atelier module add <mimir-url>` run twice in a wrapper whose first block was
named `mimir` produced a second block named `mimir_operators`. No name collided,
nothing was renamed, and nothing was reported: two blocks with an identical
`source`, silently. Terraform accepts that and fails at apply on colliding
resource names, a long way from the cause.

Refusal rather than a prompt, because there is an exact way to express the
legitimate intent:

| Existing block vs. the add | Behaviour |
|----------------------------|-----------|
| Same repo, sub-dir, and ref | Error; nothing written |
| Same, plus a free `--as NAME` | Allowed, with a warning |
| Same repo and sub-dir, different ref | Allowed, with a note |
| Different repo or sub-dir | Allowed silently |

`--as NAME` is the escape hatch and `--yes` is not: the flag means "do not ask
me", not "let me build a wrapper that cannot apply". An `--as` whose name is
already taken is not treated as the signal — the user named the thing that
already exists, which is not evidence they want a second one.

Refs are compared literally rather than resolved to SHAs. Resolving would catch
`--ref main` duplicating an unpinned block already on `main`, at the cost of a
network round trip per existing block on every add. The literal compare catches
the case that actually happens — the same command twice — and never blocks a
distinct revision; the different-ref note covers the remainder.

Same repo at a different ref stays allowed because it is an existing supported
configuration: modules are keyed by HCL label precisely so that one module can
appear at two revisions (`loadSecondaryModules`).

## Alternatives considered

- **Refuse outright in a non-empty directory.** Rejected: bootstrapping
  alongside an existing `README.md` and `.gitignore` is a legitimate and common
  workflow, and a hard error has no escape hatch short of moving files.

- **A bare "directory is not empty" check.** Rejected as too weak a proxy: it
  fires on a directory holding only a `LICENSE` while staying silent about a
  `main.tf` full of somebody else's resources.

- **Add a `--dir` flag to `module add` instead of prompting.** Does not help. The
  failure mode is not the absence of a way to name the directory; it is the
  absence of any signal that the directory is the wrong one.

- **A "confirm on write" hook in `internal/wrapper`.** Rejected: the wrapper
  package is used by the TUI and by `convert`, neither of which should ever
  prompt. Interaction belongs in the CLI layer.

- **Deduplicating by block name.** Already what the code did, via
  `uniqueBlockName`, and it does not work: names are derived, so a duplicate
  module usually does not produce a name collision at all.

- **Prompting on a duplicate rather than refusing.** Rejected on the grounds
  that a yes/no is the wrong shape for the question — the useful answer is a
  *name* for the second instance, which `--as` already provides.

## Consequences

- `module add` in an unintended directory now warns and asks, and in CI fails
  with a message naming `--yes`.
- `import --source` shares the check; a `--dir` typo is caught the same way.
- A bootstrap into a directory with its own provider declarations no longer
  produces a Terraform root that fails to initialise.
- `.atelier/` and `.terraform/` are ignored in repositories that already had a
  `.gitignore`.
- Scripted callers of `module add` and `import --source` in non-empty directories
  must pass `--yes`. This is a deliberate breaking change for the non-empty case;
  empty directories and established wrappers are unaffected.
- `purge` and `module rm` without a terminal now fail with the same message
  instead of aborting silently (exit 0). Scripts must pass `--force`/`--yes`.
  Silent success for a command that did nothing was the worse contract.
- `wrapper.Bootstrap`'s signature changes to return `(*Report, error)`.
- Re-running the same `module add` in a wrapper now fails instead of silently
  adding a second copy of the module. Scripts that relied on the add being
  idempotent-looking must pass `--as NAME` if a second instance was intended.
- SPEC §5.2 (step 0), §6.1, §6.2, §6.4 (behaviour matrix), and new §6.5, §6.6
  and §6.7 are updated.
