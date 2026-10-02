# ADR-0044: `--dir` names the wrapper; an existing wrapper is always the additive case

## Status

Accepted — amends [ADR-0034](0034-module-apply-one-liner.md) and
[ADR-0030](0030-target-directory-preflight.md).

## Context

`atelier add` used to write into the current directory, so a multi-module
wrapper was built by running `add` twice. It now creates a directory of its own,
which removed the `mkdir && cd` but hardened `--dir` into a **create-only**
flag. Two consequences:

1. `atelier add <m> --dir <wrapper>` and `atelier apply <m> --dir <wrapper>`
   both refuse a target that already holds a wrapper (`--dir is only valid when
   creating a new wrapper`; `target directory … already exists and is not
   empty`).
2. `atelier apply <m>` run *inside* a wrapper silently scaffolded a second root
   **inside** it, because `apply` had no additive case at all and derived a
   candidate-named directory under the CWD.

So composition survived only as `cd <wrapper> && atelier add <m>`. Multi-module
composition within a single root is in scope (ADR-0016 §5, ADR-0015), and SPEC
§6.2 and the §6.4 matrix both promise `add` appends — but the promise held only
for an implicitly-named target, and `--dir` had quietly lost its meaning.

The artifact was never the problem: a wrapper declaring `cos_lite` and
`charmed_spark` initialises and validates, and the TUI already groups and
context-switches across module blocks.

## Decision

**A target that already holds a `main.tf` is the additive case, for both `add`
and `apply`, whether the target came from `--dir` or from the CWD.** `--dir`
names *which* wrapper; only a target with no `main.tf` is a scaffold target.

- The append body moves out of `addModuleToWrapper` into
  `appendModuleBlock(cwd, dir, opts, prompt)`, which returns the state it wrote
  instead of opening the TUI. `add` (append, then open the editor) and `apply`
  (append, then `init`/`apply` that root) therefore share one implementation.
- `cwd` is the invocation directory, passed as `SourceBaseDir`. The additive
  path never had one, which was invisible while the target was always the CWD;
  it becomes wrong the moment `--dir` points elsewhere, since a relative local
  `source` would then resolve under the wrapper.
- The predicate is `mainTFExists`, not `isWrapperDir`: a hand-authored
  Terraform root is something Atelier already appends to rather than
  scaffolding over, so it composes the same way.
- `checkApplyTarget` is unchanged, so `apply --dir` into a non-wrapper,
  non-empty directory is still refused. Only an existing `main.tf` opts into the
  additive case.
- `--dir` pointing at a wrapper while the CWD holds a different one is no
  longer an error; the named target wins. An explicit `--dir`/`--as` that does
  *not* name a wrapper still scaffolds there.
- `--dir` names its directory literally. `atelier apply <m> --dir stack/` puts
  the wrapper in `stack/`; the candidate-derived name applies only when neither
  `--dir` nor `--as` is given.

### Preflight in `apply`'s additive path

`apply` rejects `--yes`, on the grounds that Terraform's plan prompt *is* its
confirmation. So the preflight prompt has no escape hatch here: with no
terminal, `confirmTargetDir` fails naming a flag the command refuses.
`appendModuleBlock` therefore takes `prompt`: `add` passes `opts.Yes` and
prompts as before, `apply` passes false so findings are printed and not
prompted over. This matches how `apply` already treats a target directory — it
refuses a bad one rather than asking about it — and leaves Terraform's plan as
the single gate.

### `apply` initialises with `-upgrade`

Composing a module into an already-initialised root widens that root's provider
constraints. A plain `terraform init` then refuses the lock file's existing
selection — *"locked provider … does not match configured version constraint …
must use terraform init -upgrade"* — so the composed wrapper validated and
initialised but would not deploy, with an error naming a flag Atelier owns.
`applyWrapper` therefore runs `init -upgrade`.

This is the same condition the TUI already models as `ResetInit`: a ref switch
rewrites a module source, composing rewrites the module set, and both change
what the root's providers must resolve to. For a freshly scaffolded wrapper
there is no lock file, so `-upgrade` changes nothing.

## Alternatives considered

- **Revert `add` to writing into the CWD.** Rejected: the reason `add` gained
  its own directory was real — a mistyped `cd` dropped a wrapper into a repo
  root. This decision keeps that fix and restores composability through the
  target path instead, so there is nothing to revert.
- **Leave `apply` create-only and only fix `add --dir`.** Rejected: it leaves
  `apply` nesting a root inside the wrapper you ran it in, and leaves `add` and
  `apply` disagreeing about what a wrapper is. One rule for both verbs is the
  smaller surface to document and to reason about.
- **A separate `atelier compose` / `atelier add-to` verb.** Rejected: it adds a
  spelling for a distinction `--dir` can already express, and every new verb is
  a second entry point to document and test.
- **Multi-positional `atelier add <a> <b>`.** Deferred, not rejected. It is
  sound only for positionals that are gallery entry names, since each carries
  its own module, ref, block and preset; `--ref`/`--as`/`--var` are inherently
  per-module and applying one to N modules would be a footgun. It also depends
  on this decision, since modules 2..N append through `appendModuleBlock`.
  Nothing here forecloses it.
- **Prompt in `apply` and accept `--yes`.** Rejected: it makes `--yes` a second
  spelling of auto-approve, which is the ambiguity ADR-0034 removed.

## Consequences

- `atelier add <m> --dir <wrapper>` and `atelier apply <m> --dir <wrapper>`
  compose, and `atelier apply <m>` inside a wrapper deploys that wrapper instead
  of nesting a second root inside it.
- A hand-authored Terraform root can now be the target of `apply`, which will
  run `terraform apply` against it. Findings print; Terraform's plan prompt
  remains the confirmation.
- `--dir` is no longer rejected when the CWD is a wrapper. Scripts that relied
  on that error should name the wrapper they meant.
- SPEC §6.2, §6.4 and §6.9 are updated. The integration tier gains eight
  multi-module composition tests, which its absence is why this regressed.
- `atelier apply` now runs `terraform init -upgrade`. This makes the lock file
  move when a composed module widens the root's provider constraints, which is
  a user-visible (and intended) change to `.terraform.lock.hcl`.