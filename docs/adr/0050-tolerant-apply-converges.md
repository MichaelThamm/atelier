# ADR-0050: `atelier apply` converges on the block it finds

## Status

Accepted — amends [ADR-0034](0034-module-apply-one-liner.md) and
[ADR-0044](0044-dir-names-the-wrapper.md).

## Context

Re-running the same deployment command is the normal case in CI, and it did not
work. Inside a wrapper that already declares the module, `atelier apply` took one
of two wrong turns:

- **Same ref.** The duplicate guard refused: *"module mimir already references
  this module at the same ref"*, suggesting the user edit `main.tf` and run
  `terraform apply`. So an identical re-run was an error.
- **Different `--ref`.** The guard passed, `uniqueBlockName` appended a second
  block, and the wrapper grew `module "mimir_2"` — a re-point that was neither an
  update nor a duplicate guard. The two blocks then collide at apply with
  resource-name conflicts.

Both come from one cause. ADR-0044 made an existing wrapper always the additive
case for both verbs, and the append path always forced a *fresh unique* block
name. There was no path that recognised "the wrapper already declares this
module".

Fixing the name alone is not enough, and this is the part worth being precise
about. `State.Write()` does update a block in place when `ModuleBlockName`
matches an existing label (`findOrCreateModuleBlock`), so pointing the state at
the right name is a one-line change. But the state that would be written comes
from `bootstrap.PrepareModule`, which builds `Values` empty so that
`VariableValue` falls back to each variable's **declared default** — which the
sparse rule then treats as nothing worth writing (SPEC §10.1, ADR-0007). Writing
that over a block the user has configured reverts every input the flags do not
mention. `atelier apply --var model_uuid=x` against a wrapper with four other
configured inputs would drop four of them.

So the update has to carry the block's current declaration onto the freshly
cloned schema before layering `--var` on top. That ordering is the feature.

## Decision

**A compose identifies the block to write by module, not by ref.** A block is the
module the user asked for when its repository and sub-directory match, at any ref:
the ref is what a compose *changes*, not what makes a block a different module.

Given the blocks the wrapper already declares:

| the wrapper declares | `atelier apply` | `atelier add` |
| --- | --- | --- |
| this module once | **update that block in place** | refuse as a duplicate |
| this module 2+ times | refuse, naming them; `--as` picks one | refuse as a duplicate |
| no such module | append a new block | append a new block |

An `--as` that matches no block falls back to the single block declaring the
module, reporting the name it did not use; with the module absent it names the
new block instead.

The update path, for the single matching block:

1. Read the block's current declaration out of `main.tf` **against the newly
   cloned schema** — `wrapper.ReadMainForBlock(dir, block, state.Vars)`. Reading
   against the new schema is what separates a value (typed, mergeable) from a
   wired expression or meta-argument (preserved verbatim).
2. Carry it over with `wrapper.State.AdoptPrior`, filtering to what the new
   revision still declares.
3. Layer `--var-file` then `--var` on top (ADR-0031 order, unchanged).
4. Write under the block's existing name, which is what makes `RenderMain`
   update rather than append.

**`--as` is a selector, never a multiplier.** It names the block to write: an
existing block of this module is updated, and a name matching no block is
honoured only when the module is absent, in which case it becomes the new
block's name. Pointing it at a block of a *different* module is refused. This
withdraws `atelier add --as <name>` as the escape hatch for a deliberate second
instance — the duplicate error now says "add the block to `main.tf` by hand"
instead. The capability is reachable by editing the artifact (ADR-0001: the
wrapper is the user's), the TUI picks such a block up on the next open, and a
wrapper that already has two stays deployable via `atelier apply --as <block>`.

### An `--as` that matches nothing falls back to the block that does

A gallery entry supplies its own block name: `resolveSource` copies
`entry.Block` into `--as` when the user gave none, so `atelier apply cos-lite`
arrives carrying a name the user never typed. If the wrapper already declares
that module under any other name — because it was renamed, or because the entry
pins a name the module's own basename would not derive — then nothing matches
and the strict reading dead-ends the command: *"edit `main.tf` by hand"* for a
flow where the user simply asked to deploy.

So when `--as` matches no block and the module is declared **exactly once**, that
block is what was meant. It is updated under its own name, and the run says
`--as <name> names no block in this wrapper; updating "<block>", which already
declares this module`. The name is reported rather than obeyed, and it still
never creates a second copy. With two or more matching blocks there is nothing
unambiguous to fall back to, so the request is refused as below.

A tighter rule — ignore the name only when it came from a gallery entry — was
rejected: it needs a flag threaded through the options purely to tell a name the
user typed from one the gallery filled in, and the fallback is the more useful
behaviour for the typed case too, since refusing there has no upside.

### The required-input gate honours a wired expression

`apply`'s pre-flight gate stopped a run whose required input had no *value*.
A wired reference (`model_uuid = module.loki.endpoint`) is an input Terraform
resolves and Atelier cannot, so the gate counted it missing and refused a wrapper
that deploys fine. It now treats a wired expression as set, matching the TUI's
own required-unset test.

### The carry-over is one shared rule

`wrapper.State.AdoptPrior` is the single implementation, replacing three copies of
the same filter (ADR-0050 supersedes the inline loops in `bootstrap.LoadRefState`
and `Model.applyRefSwitch`). It keeps a prior attribute when the new schema still
declares it.

**It deliberately does not carry a meta-argument.** `depends_on` / `count` /
`for_each` / `providers` say how a module is composed, not what the module takes,
and Atelier holds no opinion on composition: if a user wires one module to
another, they write that line themselves. Carrying it into the state would be
modelling it.

Nothing is lost by not carrying it. Every write is AST-backed on the existing
`main.tf`, so an attribute already in the file is never rewritten, and
`RenderMain`'s orphan prune keeps the meta-argument names unconditionally. A
hand-written `depends_on` therefore survives a TUI ref switch and an `atelier
apply` re-point — checked on the previous release for both, and pinned by
`TestAdoptPrior_dependsOnReachesTheBlock`.

An earlier draft of this ADR claimed the carry-over was a data-loss fix here. It
was not, and the claim is withdrawn: the previous filter dropped meta-arguments
from the *loaded state*, never from the file. The rule that is visible at file
level is the adjacent one, a wired expression on a *declared* variable, which the
variable loop does remove when its value is unset; that case was already handled
and already tested.

### `add` stays strict

The two verbs share one compose body and one decision function, differing in one
`composeMode`. `add` authors and `apply` deploys: `add` refuses a module the
wrapper already has rather than overwriting configuration the user made by hand
without asking. ADR-0044's shared-predicate decision is unaffected — the *target*
rule (a `main.tf` means compose, anything else scaffolds) is unchanged; only what
happens to the block within that target differs.

### A re-point updates `.atelier/session.json`

`session.json` records which module and revision the wrapper is pinned at, and
`LoadExisting` trusts it over `main.tf`. A CLI re-point that left it alone would
reopen the TUI on the *previous* ref, and the next save from there would write
that ref back — silently reverting the change the user just asked for.

This is the normal case, not an edge case: a wrapper scaffolded by `atelier apply`
*does* have a session, written by `FreshWrapper` → `InitNew`. Only the compose
path has never written one, so a wrapper assembled entirely by composing has none
to correct.

`apply` therefore reconciles the session when the block it rewrote is the one the
session tracks, mirroring what `prodRefSwitcher.SwitchRef` already does, and
creating nothing where there is nothing — a wrapper Atelier has never opened has
no session to correct, and inventing them is not this change's business. A
reconcile failure warns and the deploy continues: `main.tf` is the artifact and is
already correct.

### What a rewrite does and does not touch

Rewriting a block round-trips what the user wrote: meta-arguments, wired
expressions, and comments all survive, because the write is AST-backed on the
existing file. Two things are normalised, and a deploy command should not pretend
otherwise:

- **An argument equal to its module default is removed** (ADR-0007). Semantically
  inert, and rare — Atelier's own writer never emits one, so it only exists in a
  hand-written or upstream-seeded `main.tf`.
- **A constant expression is replaced by its value.** A hand-written
  `units = 1 + 1` comes back as `units = 2`, because the value evaluates and the
  writer renders values.

Both are pre-existing writer behaviour, not something this change introduces; what
it adds is that `apply` can now trigger them on a block you already have. The run
reports the prune by name; it does not report the constant fold.

## Alternatives considered

- **Just pin the block name and write.** The one-line version, and the reason the
  data-loss risk was worth naming: it silently reverts every input not named on
  the command line to its default. Rejected.
- **Keep refusing a same-ref duplicate, only make a re-point an update.** Then
  CI still cannot re-run the command it already runs, which is the case that
  most needs to work. Rejected.
- **Merge by reading `main.tf` with an empty variable list** (the degraded path's
  approach). Every value then arrives as a preserved raw expression rather than a
  value, so nothing is typed, `--var` cannot deep-merge an object into what is
  there, and a `null`-valued argument is indistinguishable from an unset one.
  Reading against the new schema costs nothing extra — the clone already
  happened — and is strictly more informative. Rejected.
- **A separate `atelier update` / `atelier set` verb.** Rejected: `--ref` and
  `--var` already say what to converge to, and a second verb is a second entry
  point to document and test. ADR-0044 already rejected a compose verb for the
  same reason.
- **Keep `--as` as a multiplier** (`--as <free name>` declares a second copy).
  It keeps a capability, at the cost of three states in the decision table instead
  of two and a flag that means "pick the block" in one case and "add a module" in
  another. The capability is reachable by editing the artifact. Rejected.
- **Disambiguate two matching blocks by exact-source match.** If one of them
  already declares precisely the source requested, converging on it is arguably
  not a guess. Rejected as a rule to carry: the state only exists because the
  previous behaviour appended rather than updated, Atelier is pre-1.0 with little
  installed use, and the other way a wrapper gets two blocks of one module is
  someone hand-writing them — where two blocks usually mean something, and asking
  is the right answer. `--as` already resolves it without a new rule.
- **Make `apply --json` part of this.** It does not exist
  ([ADR-0048](0048-machine-readable-output.md)) because `apply` reports
  Terraform's output rather than a result Atelier owns; a payload would have to
  invent one. Separate decision, and the open blocker on
  [#55](https://github.com/MichaelThamm/atelier/issues/55).

## Consequences

- Re-running an identical `atelier apply` is idempotent, and a re-pointed `--ref`
  updates the block instead of appending a second one. Both are the behaviour CI
  expects from a deployment command.
- A wrapper composed with an earlier version — which may hold `mimir` *and*
  `mimir_2` — is refused for that module rather than silently extended. `--as`
  names the block to update; `atelier rm` removes the other. That state has
  essentially no installed base, and `--as` is the way out of it.
- `atelier apply <gallery-name>` converges on the existing block when the
  wrapper holds the module under another name, where before it appended a second
  copy of the same module.
- The sparse-rule note names what it pruned, but pruning during a deploy remains
  a normalisation the user did not request. It is rare, because Atelier's writer
  never emits an at-default argument; only a hand-written or upstream-seeded
  `main.tf` has one.
- `add`'s duplicate error no longer offers `add --as <name>`; it points at
  `atelier apply … --ref <ref>` and at editing `main.tf`. Nothing else about
  `add` changes.
- The carry-over models module inputs only. `depends_on` and the other
  meta-arguments are the user's composition decision and are left in the file
  untouched; they survive every rewrite without Atelier's help.
- `atelier apply` no longer needs `cd` into the wrapper for a second module, but
  nothing about a *fresh* target changes: a directory with no `main.tf` still
  scaffolds, and a non-empty non-wrapper target is still refused (ADR-0030).
- The required-input gate no longer refuses a block whose required input is a
  wired reference, matching what the TUI already did.