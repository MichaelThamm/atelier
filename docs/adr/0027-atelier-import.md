# ADR-0027: `atelier import` — import live infrastructure into an existing module

## Status

Accepted (revision 3) — supersedes revisions 1 and 2, whose execution models are
recorded in the changelog below and under *Alternatives considered*. Depends on
[ADR-0028](0028-provider-specific-import-ids.md) for the provider-specific
extension points.

## Changelog

- **Revision 1** (original): Imperative `terraform import` per matched resource.
  No reviewable artifact. Provider-specific logic wired inline.
- **Revision 2** (superseded): Proposed generating declarative `import {}` blocks
  in `imports.tf` and executing the import with `terraform apply`. Added
  `--plan-only` and `--purge`.
- **Revision 3** (current): Keeps `terraform import` as the **executor**, after
  measuring that `import {}` blocks do not produce an import-only plan — a
  `terraform apply` would also create every resource the artifact fails to
  cover, duplicating live infrastructure. `imports.tf` is retained as a
  reviewable/retry **artifact**, decoupled from execution. Replaces
  `--plan-only` with `--dry-run` and `--purge` with an automatic artifact
  lifecycle. Adds a pre-plan preflight phase so provider identity (the Juju
  model UUID) is known *before* the module is planned, which removes the need
  for `--var` entirely. Adds a model-mismatch guard.

## Context

Atelier authors a *wrapper*: a thin `module {}` call whose `variable` overrides
are the user's statement of intent ([ADR-0001](0001-wrapper-as-durable-artifact.md),
[SPEC §1](../SPEC.md)). Every existing flow starts from a module's *variable
API* and moves forward — clone, parse variables, edit, plan, apply.

Users increasingly ask the inverse: *"I already have a running deployment I
created by hand (or with the CLI). Can Atelier help me pull it under Terraform
management?"*

The target workflow is:

1. Deploy COS (or similar) from a concrete Terraform module.
2. Lose the state file, or deploy without Terraform, or decide to adopt Terraform.
3. Run `atelier import`.
4. Continue regular module operations.

### The problem space

The user has infrastructure running and wants Terraform to manage it without
re-deploying. This requires:

1. **Discovery** — what resources actually exist in the live environment?
2. **Matching** — which live resource corresponds to which module address?
3. **Import** — writing those resources into Terraform state.

Steps 1 and 2 are the hard parts. Step 3 is mechanical. Today, users must
perform all three manually: running `terraform query`, parsing JSON output,
building a mapping table, and typing 30+ `terraform import` commands by hand.

### Why not `terraform query -generate-config-out`?

`terraform query -generate-config-out` emits flat `resource` blocks with literal
values and matching `import {}` blocks. That is designed for the case where **no
module config exists** — it generates one from scratch. For a user who already
has an intact module, the generated flat config is a competing, literal-valued
duplicate of what the module already declares. It creates more work (manual
cross-referencing, default pruning) rather than less.

The correct approach for state reconstruction is: **import directly into the
module's existing resource addresses**, not generate a new flat root.

### Positioning against upstream tools

**Terragrunt** provides import workflow scaffolding: state segmentation per unit,
`import {}` blocks via `generate {}`, dependency resolution with mocks, and
backend configuration. It has no discovery — the user must know every resource
address and ID before starting.

**Terraform** provides `terraform import` (imperative) and `import {}` blocks
(declarative, TF ≥ 1.5). It has `terraform query` for live resource discovery.
But it has no automated matching between discovered resources and module
addresses.

**Atelier** fills the gap: it discovers live resources via `terraform query`,
matches them to the module's declared resource addresses, and imports them.
It can also emit the matching as an `imports.tf` artifact, which is portable —
it works with plain Terraform, Terragrunt, or CI pipelines.

Atelier does not compete with Terragrunt. It can produce the same artifact
(`imports.tf`) but fills it automatically instead of requiring the user to
hand-author import blocks.

### The tension with Atelier's core model

This feature does not fit the wrapper model cleanly:

- **Import IDs are provider-specific.** The format (`<model_uuid>:<app_name>`
  for Juju, `arn:...` for AWS) is not derivable from the provider schema alone.
  See [ADR-0028](0028-provider-specific-import-ids.md).
- **Import mutates state**, not just infrastructure. Atelier's apply contract
  ([ADR-0002](0002-author-and-plan-scope.md)) assumes plan-then-apply against
  a wrapper the user authored; import adopts pre-existing real resources into
  state.
- **Import writes to `main.tf`.** In `--source` mode Atelier authors the
  wrapper outright and persists every value it was given (`--var`, `--preset`)
  into it, as the other flows do. What is new to import is that Atelier also
  writes a value it *derived itself*: to plan the module correctly it must know
  which deployment is being imported (for Juju, the model UUID), and that is
  folded into the wrapper before planning. Derived writes are bounded — identity
  values only, never overwriting a value the user set, always reported.

## Decision

Add **`atelier import [PROVIDER]`**, a command that reconstructs Terraform
state for an existing module from a running deployment. Atelier owns discovery
and matching; `terraform import` performs the state write.

### Behaviour

1. **Discover live resources generically.** Write a `*.tfquery.hcl` listing the
   provider's list-resource types, then run `terraform query -json` to enumerate
   every live object. The query is schema-driven: whatever the provider declares
   as list resources is what Atelier offers.
2. **Preflight: fold live-derived identity into the wrapper.** Before planning,
   run provider-specific preflight steps against the live objects. For Juju this
   derives the model UUID and writes it into the wrapper. **Ordering is
   load-bearing:** modules commonly branch on this value (COS-Lite's
   `local.create_model = var.model.uuid == null` decides whether `juju_model` is
   a managed resource or a data source, and feeds every application's
   `model_uuid`), so planning without it yields both the wrong resource
   addresses and an unknown `model_uuid` — which makes Terraform plan a *replace*
   for every application.
3. **Plan the existing module.** Resources the module wants to *create* are the
   import candidates; their full module addresses (e.g.
   `module.cos.juju_application.alertmanager`) are the import targets. Resources
   already in state are also collected, so matching still works against a
   partially-populated state.
4. **Check the plan against the live deployment.** Provider-specific plan checks
   run here, before anything is imported, and refuse a run whose configuration
   contradicts what was discovered (see *The model-mismatch guard*).
5. **Match by identity, then name, then attributes.** Pair each planned resource
   to a live object using a three-phase strategy (see
   [ADR-0028](0028-provider-specific-import-ids.md)). Zero or ambiguous matches
   are reported for manual resolution — on every run, including one that imports
   nothing.
6. **Build import IDs from provider identity where possible.** A live object's
   provider-declared identity is preferred over any string Atelier constructs
   (see ADR-0028). Matches for which no ID can be built are reported explicitly
   as *matched but not imported* — never silently dropped.
7. **Import, or preview.**
   - Default: run `terraform import <address> <id>` per match. This is a pure
     state operation and cannot alter infrastructure.
   - `--dry-run`: write `imports.tf` (when there is anything to write), re-plan
     with it in place, report `N to import / M to add`, and stop. Terraform state
     is untouched. Note that a dry run still performs the preflight write to
     `main.tf`, since the plan it previews depends on it.
8. **Normalise state.** Run post-import steps that neutralise quirks which
   would otherwise show as spurious diffs. One of them — null/empty
   reconciliation — asks Terraform what the configuration wants, by planning the
   module as imported and reading the before/after pairs, rather than inferring it
   from variable declarations. The others apply fixed provider corrections and
   need no plan, so the plan is computed lazily and shared.

### Why `terraform import` and not `terraform apply` with `import {}` blocks

Revision 2 proposed executing the import via `terraform apply`. This was
rejected on measurement. With two `import {}` blocks present against an empty
state, `terraform plan` reports:

```
Plan: 2 to import, 44 to add, 2 to change, 0 to destroy.
```

`import {}` blocks do not produce an import-only plan. They land in the same
plan as every other pending action, and `terraform apply` executes all of it —
so those 44 creates are live resources that already exist. Apply-based import is
therefore only safe when `imports.tf` is **complete**, and the case that
motivates having an artifact at all — "something went wrong, let me edit it and
retry" — is precisely the case where it is **partial**.

`terraform import` has the opposite property: it only writes state, so it cannot
create, modify or destroy infrastructure regardless of how partial or wrong the
input is. That safety is worth more than the ~2.5 minutes an apply-based path
would save (measured: ~4.9 s per `terraform import`, ~3.5 min for 43 resources).

The genuine advantage of `import {}` blocks is **preview**, not execution:
`terraform plan` with them in place reveals the post-import diff before state is
touched. Revision 3 keeps that benefit and drops the hazard, by using import
blocks to *see* and `terraform import` to *do*.

### Artifacts and their lifecycle

Generated Terraform inputs are removed on success and kept only when they would
help the user retry.

| File | Purpose | Lifecycle |
|------|---------|-----------|
| `atelier-import.tfquery.hcl` | Discovery input for `terraform query`. Mandatory: `terraform query` has no CLI-argument equivalent and discovers `*.tfquery.hcl` by scanning the working directory, so it must exist in the root. | Removed when the whole run succeeds. Retained whenever the run does not complete — a query failure, a failed plan check, a failed import — and under `--dry-run`. |
| `imports.tf` | Reviewable `import {}` artifact | Written under `--dry-run` when there is something to import, and when an import fails part-way (then containing the *remaining* resources). Cleared before the candidate plan — see below. |
| `atelier-import.auto.tfvars` | Variable values for the plan, when there is no wrapper to hold them | Always removed; the cleanup is registered before the plan so a failed plan cannot leak it. |
| `.atelier/import-scan.tfplan` | Plan binary Atelier reads back as JSON | Always removed |
| `main.tf` / `versions.tf` / `providers.tf` | Wrapper scaffolding | Permanent |
| `terraform.tfstate` | The actual goal | Permanent |

Retention keys off the run's **outcome**, never off which list-resource types
failed. Deciding from the identity of a failure would mean encoding, per
provider and per provider version, which failures are routine — the query engine
legitimately errors for types whose facade does not apply to the target (a
Kubernetes Juju model cannot answer for spaces or SSH keys), and those are
expected rather than actionable. Types dropped along the way are reported as
facts, and `--strict` promotes them to a hard failure for anyone who wants the
reproducing file. When retained, the *first, complete* rendering is written
back: the retry loop prunes failing list blocks, and a pruned file cannot
reproduce the failure it was kept to explain.

`imports.tf` is cleared once, **before the candidate plan**, and this is
correctness rather than tidiness. With `import {}` blocks present, Terraform
reports those resources as `Importing`, and Atelier deliberately skips
`Importing` entries when collecting import candidates. A stale artifact from an
earlier `--dry-run` therefore makes the next real run find zero candidates and do
nothing but print "No live resources matched". (A dry run's *second* plan is
deliberately run with the artifact in place — that is what produces the preview.)
Cleanup is gated on a generated-by header so a hand-written `imports.tf` is never
deleted.

### CLI surface

| Flag | Behaviour |
|------|-----------|
| *(default)* | Full pipeline: discover → preflight → plan → check → match → `terraform import` → normalise. |
| `--dry-run` | Write `imports.tf` (when non-empty), plan with it in place, report the preview, stop. Terraform state untouched. |
| `--source URL`, `--module PATH`, `--ref REF` | Clone an upstream module and author a wrapper to import into. Without `--source`, Atelier imports into the existing root as-is. |
| `--dir PATH` | Target directory (defaults to the working directory). |
| `--query-var K=V` | Values for the query engine's list blocks (e.g. `model_uuid`). |
| `--var K=V` | Module input values. Written into the wrapper when there is one, and also threaded to any list block that accepts the key. |
| `--preset NAME` | Apply a named preset from `atelier.local.yaml`. |
| `--type T` | Restrict the query to these list-resource types (repeatable). |
| `--strict` | Make any list-resource query error fatal instead of skipping the type. |
| `--provider-version V` | Version constraint for a scaffolded provider. |
| `--no-init` | Skip the automatic `terraform init`. |
| `--verbose` | Full match-debug output, including every live object. |
| `--list` | Print the provider's importable list-resource types and exit. |

Note that `--type` narrows the schema-driven default: absent it, every list
resource the provider declares is queried. There is no `--purge`, because
artifacts are managed automatically.

### Import should not require variables it can determine itself

`atelier import` must never ask for a value it can derive, but it cannot invent
values the module requires. Terraform will not plan without them, and a
required-but-unset variable is omitted from the wrapper's `module {}` block
rather than written as a placeholder — see the end of this section for why that
is the better failure.

Module variables fall into three tiers:

| Tier | Examples (COS-Lite) | Affects | Handling |
|------|--------------------|---------|----------|
| **Identity** | `model.uuid`, `model.name` | Import IDs, and topology via `local.create_model` | Never asked for. Seeded from a like-named query variable before discovery, or derived from the discovered objects in preflight. |
| **Topology** | `internal_tls`, `ingress.*`, `postgresql_offer_url`, `*.app_name` | Which addresses exist (count/for_each/labels) | Not inferred. Mismatches are *reported*: unmatched live objects are listed by type, and `--dry-run` gives the count of resources that would be created rather than imported. |
| **Fidelity** | `risk`, `*.revision`, `*.config`, `*.constraints`, `*.units` | Nothing in matching — only the post-import plan diff | Out of scope for import. After import the state holds the live values, so the plan diff tells the user what to set. Asking up front is strictly worse than showing the diff after. |

Cutting across those tiers: **a variable with no default must be supplied**,
whatever tier it falls in. COS-Lite gives every variable a default, which is why
importing it needs no `--var` at all; that is a property of the module, not a
guarantee of the command.

The identity tier is exempt, and getting there needs two mechanisms rather than
one, because of an ordering constraint. Discovery must run before preflight —
preflight derives its values from the discovered objects — but `terraform query`
loads the root module, so it is the first command to reject a module argument the
wrapper omits. A module declaring `model_uuid` as required therefore fails at
discovery, before preflight could ever supply it.

Derivation cannot be moved earlier, but for this case nothing needs deriving: the
Juju provider requires `model_uuid` as a config attribute on most of its list
resources, so every real import already supplies it via `--query-var`. So before
anything runs, Atelier seeds any module variable the wrapper leaves unset from a
like-named query variable. That is provider-agnostic — it fires only when the
names coincide and the variable is unset — and it removes the need to pass the
same value twice. `loki-operators` needs only `--query-var model_uuid=<uuid>`;
COS-Lite, whose `model = { uuid = optional(string) }` shares no name with a query
variable, is covered by preflight derivation instead.

A value the user has set is never overwritten by seeding, even when it disagrees.
A genuine disagreement is refused by the model-consistency plan check, which is
better than silently rewriting their configuration.

Required-but-unset variables are omitted from the wrapper's `module {}` block
rather than written as an explicit `null` placeholder. That is deliberate:
Terraform then reports `The argument "x" is required, but no definition was
found` for every missing variable at once, which the error classifier turns into
a single actionable message. An explicit `x = null` would instead be accepted and
fail later, deep inside the module, at whatever consumed the value.

Topology inference is deliberately not attempted. The signal that matters is
already exact and comes from Terraform rather than a heuristic: `--dry-run`
reports how many resources the module would still create, and lists them.

### The model-mismatch guard

For Juju, `model_uuid` forces replacement on every resource. If state holds
resources from one model while the configuration names another, the next apply
destroys everything. Measured against a live deployment, with 43 imported
resources and a wrapper edited to name a different model:

```
Plan: 46 to add, 0 to change, 43 to destroy.
```

Atelier therefore **refuses** to import when the planned configuration targets a
different model than the live resources came from, failing before anything is
written. This is an error rather than a warning: there is no reading of "import
model B's resources into a configuration declaring model A" whose outcome is not
destruction.

The check reads the *planned* `model_uuid`, not a wrapper variable. That is what
gives it coverage: without `--source` there is no wrapper, and the user's root may
carry the UUID in a tfvars file, a `-var`, an environment variable or a literal.
Terraform has resolved all of those by the time it produces a plan, so the plan is
the one place the answer is always available. An earlier wrapper-based version of
this guard silently did nothing in `--source`-less mode.

Relatedly, the model the live objects actually came from is treated as ground
truth, outranking any requested value. Honouring a `--var` that disagrees with
the live data would write a mismatched UUID into `main.tf` — the same failure by
a different route.

### Idempotency: import is meant to be re-run

The contract is *run with no vars → read the report → adjust → re-run*, not
*guess every variable correctly, run once*. Matches whose addresses are not
planned creates are skipped silently, because they are already in state and
there is nothing to import. A second run over an already-imported deployment is
therefore a clean no-op, and reports itself as one:

```
Nothing to import: all 43 matched resource(s) are already in state.
```

This is deliberately distinguished from having matched nothing — the two look
identical in the shape of the run but mean opposite things.

### Scaffolding and auto-init

When the target directory has no provider configuration, `atelier import`
scaffolds a minimal `versions.tf`/`providers.tf` from a generic `PROVIDER`
positional argument and runs `terraform init` automatically.

### Explicit non-goals

- **Cross-referencing.** Not needed: the module already cross-references its
  resources via `var.*` and resource references. Import puts state behind the
  existing config; no config changes are required.
- **Default/implicit-resource pruning.** Not attempted generically. Implicit
  resources (peer relations, auto-created secrets, default storage pools) that
  the module does not declare are reported as unmatched-live and left alone.
- **Topology inference.** Atelier does not guess which variable gates a missing
  resource. It reports the discrepancy exactly and lets the user decide.
- **Product version detection.** Users know which version they are running.
  Atelier does not attempt to identify the product version — the user specifies
  the module and ref.
- **State backend configuration.** Atelier scaffolds local state only. Remote
  backend configuration is the user's responsibility (or handled by Terragrunt).
- **Multi-model imports.** The query is single-model by construction. A module
  managing several models at once is out of scope.

## Alternatives considered

### `terraform query -generate-config-out`

Rejected. Generates flat resource blocks that duplicate the existing module's
config, requiring manual cross-referencing and pruning. For state
reconstruction (config already exists), importing into the module's own
addresses is simpler and more correct.

### Executing the import via `terraform apply` with `import {}` blocks (revision 2)

Rejected on measurement — see *Why `terraform import` and not `terraform apply`*
above. `Plan: 2 to import, 44 to add` demonstrates that apply-based import
cannot be made safe for a partial artifact, and a partial artifact is exactly
what the retry workflow produces. The artifact itself was kept; only its role as
the execution path was dropped.

### Gating an apply-based path on an "import-only" plan

Considered as a way to keep apply-based execution safely: refuse to apply unless
the plan contains no action other than imports. Rejected as maintenance burden
for little value. The plan is never strictly import-only in practice — COS-Lite
legitimately creates three `terraform_data` resources that have no live
counterpart — so the gate would need a maintained allow-list of
never-importable types to be usable at all, and it buys only a couple of
minutes.

### Imperative `terraform import` per resource, without any artifact (revision 1)

Superseded rather than rejected. The execution mechanism was correct; what was
missing was the reviewable artifact and the ability to preview. Revision 3 adds
both without changing the executor.

### Retaining artifacts based on which resource types failed

Rejected. It would require encoding, per provider and per provider version,
which query failures are expected — a list that silently rots. Retention keys
off the run's outcome instead.

### Fully provider-agnostic import IDs

Rejected for v1. Import ID formats are not fully derivable from the provider
schema. Revision 3 narrows the gap considerably by preferring the provider's own
resource identity where it is the import ID (see ADR-0028), but a residue of
provider knowledge remains.

## Consequences

- **Execution cannot damage infrastructure.** `terraform import` only writes
  state. A wrong, partial or stale import set produces a bad state file, not a
  destroyed deployment. This is the property that ruled out apply-based import.
- **Import asks only for what it cannot determine.** Identity values are derived
  from live data before planning, and fidelity values belong to the post-import
  plan diff rather than to import. Variables the module declares without a
  default still have to be supplied, since Terraform cannot plan without them.
- **Ordering is part of the contract.** Preflight runs between the query and the
  plan. Anything that must influence the plan has to happen there; doing it
  after import is too late, and a step that derives its input from imported
  state cannot bootstrap itself.
- **`--dry-run` is the correctness check.** `N to import / M to add` is the
  Terraform-sourced signal for "did the import set cover everything", with
  unexpected creates listed by address. `M` includes resource types that can
  never be imported (`terraform_data`); those are sub-counted in the report and
  deliberately left out of the listed addresses, so the number to act on is `M`
  minus that sub-count and the listed addresses are exactly the ones worth
  attention. A dry run reports
  the preview on every run, including one where nothing matched or everything was
  already in state — those are the runs where it matters most.
- **Matched-but-not-imported is reported.** A match for which no import ID could
  be built appears in its own section, because a later apply would try to create
  a resource that already exists. Silence here was a real defect: it hid seven
  unimported `juju_application` resources behind a matched count of 43 and an
  imported count of 36.
- **Re-running is safe and expected.** Already-in-state matches are skipped, and
  the report distinguishes "already done" from "matched nothing".
- **Atelier writes to `main.tf` during import.** Bounded to identity values,
  never overwriting a value the user set, always reported, and rendered sparsely
  (COS-Lite receives exactly `model = { uuid = "…" }`). This is a deviation from
  ADR-0001's model that only `import` makes.
- **A model mismatch is fatal.** Refusing costs nothing, since no state has been
  written at that point; proceeding costs the deployment. The check is plan-based
  so it covers both modes, including a root Atelier did not author.
- **Normalisation asks rather than infers.** Reconciling state against the
  configuration is driven by the post-import plan, restricted to differences that
  are purely null-versus-empty-collection. Nothing outside that shape is touched,
  so real drift — a charm revision moving 198 to 199 — survives untouched. The
  previous approach pooled variable defaults into one flat attribute-name
  namespace and applied it to every imported resource, which gave resources
  defaults belonging to unrelated variables. Cost: one extra plan per import.
- **A per-command version gate is retained** for `terraform query` (≥ 1.14).
  `import {}` blocks, used for `--dry-run` previews, require TF ≥ 1.5, which is
  already the minimum for all Atelier commands.
