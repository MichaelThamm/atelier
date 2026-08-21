# ADR-0028: Provider-specific import IDs — scoped to Juju for v1

## Status

Accepted (revision 2) — scopes [ADR-0027](0027-atelier-import.md)'s
provider-specific surface. Revision 2 narrows that surface by preferring the
provider's own resource identity over constructed import IDs, and splits the
extension points by pipeline phase.

## Changelog

- **Revision 1** (original): `ImportIDFunc` constructs every import ID from the
  resource type plus user-supplied config. `PostImportStep` covers all
  provider-specific normalisation.
- **Revision 2** (current): Prefers the provider's own **resource identity** as
  the import ID, so most resources need no constructed string and no
  user-supplied config at all. Splits the extension points into a **pre-plan
  `PreflightStep`** and a **post-import `PostImportStep`**, because the two need
  different inputs and run at points that are not interchangeable. Documents the
  one remaining hardcoded provider exception and why the obvious alternatives
  are worse.

## Context

[ADR-0027](0027-atelier-import.md) adds `atelier import`, which reconstructs
Terraform state for an existing module from a running deployment. The flow is:
discover live resources via `terraform query`, fold live-derived identity into
the wrapper, plan the module to find import candidates, match them, and run
`terraform import <address> <id>` for each match.

The import ID is provider-specific. The Juju provider documents:

```
terraform import juju_application.wordpress <model-uuid>:wordpress
```

Historically Atelier constructed that string itself, which required the user to
supply `model_uuid` as a module variable. That coupling was the source of a real
defect: a run that supplied the UUID only to the *query engine* produced empty
import IDs for every `juju_application`, and those matches were then silently
dropped — 43 matched, 36 imported, seven applications missing with no error.

Two observations reshape the decision:

1. **Terraform 1.14+ resource identity is usually the import ID already.** A
   live `juju_application` reports `identity = {"id": "<model_uuid>:alertmanager"}`,
   which is exactly what `terraform import` expects. The provider authored it,
   so using it cannot drift from the provider's own format.
2. **Some provider knowledge must exist before the plan, not after it.** Modules
   branch on identity values (COS-Lite's `local.create_model =
   var.model.uuid == null`), so a step that runs after import cannot fix the
   addresses the plan already produced. Worse, the original
   `JujuModelUUIDInjection` derived the UUID from an imported `juju_application`
   — circular, since importing applications was what needed the UUID.

Post-import normalisation (null attribute patching, schema version injection,
offer defaults) remains provider-specific: these are workarounds for provider
bugs or storage quirks that other providers do not have.

## Decision

**Provider-specific code is isolated behind four extension points:**

1. **`PreflightStep`** — runs *after* the live query and *before* `terraform
   plan`. It receives the live objects, the wrapper state, and the config map,
   and may mutate the wrapper so the plan that follows produces the right
   resource addresses. This is the only window in which a step can still
   influence the plan.

2. **`PostImportStep`** — runs after `terraform import` completes, to normalise
   state against provider quirks that would otherwise show as spurious diffs.

3. **`PlanCheck`** — runs after the module is planned and before anything is
   imported. It is where a run is refused because its plan contradicts the live
   deployment. Neither neighbour can do this job: preflight runs before a plan
   exists, and post-import runs after the damage is recorded in state.

4. **`ImportIDFunc`** — constructs the import ID for a matched resource, given
   the match (including the live object's identity) and the config.

The step interfaces are deliberately separate rather than one interface with a
phase flag. Their contexts differ — preflight needs `Live` and a mutable `Config`,
a plan check needs the `Plan`, post-import needs `Imported` and a way to plan
again — and a single context type would carry fields that are nil most of the
time. Keeping them apart also puts the ordering constraint in the type system: a
check that needs the plan cannot be registered as a preflight step.

### Import IDs prefer provider identity

`ImportIDFunc` implementations should use the live object's provider-declared
identity whenever it is available, falling back to a constructed string only
when it is absent or known to differ.

The Juju rule is not per-type. It is: **use the identity `id` unless the type is
on a short exception list**; only when the identity is missing does a constructed
string apply — `<model_uuid>:<name>`, or a bare `<model_uuid>` for `juju_model`,
or an empty ID (reported, not imported) when no `model_uuid` is known. The
identities encountered in practice look like this:

| Resource type | Identity `id`, which is also the import ID |
|---|---|
| `juju_application` | `<model_uuid>:<app_name>` |
| `juju_integration` | `<model_uuid>:<app1>:<ep1>:<app2>:<ep2>` |
| `juju_offer` | the offer URL, e.g. `admin/cos-lite.certificates` |
| `juju_model` | `<model_uuid>` |

This makes import IDs independent of user-supplied variables for every type that
exposes an identity, which is what allows `atelier import` to require no `--var`
([ADR-0027](0027-atelier-import.md)).

### The one hardcoded provider exception

`juju_secret` is excluded from identity-based IDs. Its identity is
`<model_uuid>:<generated_secret_id>` (e.g. `…:coj8mulh8b41e8nv6p90`), while
import expects the secret's *name*. The exception is evidence-backed rather than
guessed: the provider's own documentation imports secrets by name
(`terraform import juju_secret.secret-name testmodel:secret-name`), so the
generated ID in the identity is not the accepted form.

Two ways to eliminate the exception were considered and are worse:

- **Try identity, fall back on failure.** Requires distinguishing "wrong ID
  format" from "authentication failed" or "resource gone" by pattern-matching
  provider error text — *more* provider knowledge than a one-entry list, and
  less stable.
- **Structural heuristic: use identity when its trailing segment equals the
  resource name.** Works for `juju_application` (`<uuid>:alertmanager`) but
  breaks for `juju_integration` (`<uuid>:a:b:c:d`, whose trailing segment is an
  endpoint), which is precisely the type that *needs* identity.

Its failure mode is benign: if the provider later makes secret identity the
import ID, the exception simply keeps using the constructed form, which still
works.

The separate `unimportableTypes` list (`terraform_data`) is not a provider
exception — `terraform_data` is a Terraform *core* builtin, documented as
existing only in state, and is the only managed resource type Terraform core
defines. That list is provably complete and cannot drift with provider versions.

### Identity values are matched by convention, not inferred

The Juju preflight step must place the model UUID into a module variable. It
does so by matching two conventions literally:

1. a variable named `model` of object type with a string attribute named `uuid`;
2. a variable named `model_uuid` of string type.

This is convention matching, not inference, and it is a deliberate choice rather
than a shortcut: **real dataflow inference is not available.** Tracing which
variable feeds `juju_application.model_uuid` through the plan JSON gives

```
juju_application.model_uuid  ->  references: ["var.model_uuid"]   (submodule)
module call's model_uuid     ->  references: ["local.model_uuid"] (cos-lite)
locals exposed in plan JSON  ->  false
```

The chain dead-ends at the local, and Terraform does not publish local
*definitions* in the plan's `configuration` block. Resolving it would mean
parsing the module's HCL and evaluating its reference graph — including
`local.model_uuid = local.create_model ? juju_model.cos[0].uuid :
data.juju_model.cos[0].uuid`, a conditional branching on the very variable being
sought. That is reimplementing part of Terraform's evaluator.

Convention matching is therefore fallible by construction, and is handled
accordingly:

- **It is no longer load-bearing.** Import IDs come from provider identity, so a
  miss cannot silently drop resources.
- **A miss is loud.** The result is three-valued — injected, already set, or *no
  matching variable* — so "nothing recognised to write into" is reported with the
  conventions it looked for, rather than being confused with "the user already
  set it".
- **The consequence is independently detectable.** `--dry-run` reports
  `N to import / M to add` from Terraform's own plan, owing nothing to the
  heuristic.

A UUID the user already set is never overwritten, and other fields of an object
variable are preserved — the step may run against a wrapper the user has edited.

### Identity mismatches are fatal

Because `model_uuid` forces replacement on every Juju resource, importing one
model's resources under a configuration that declares another destroys the
deployment on the next apply (measured: `Plan: 46 to add, 0 to change, 43 to
destroy`). `JujuModelConsistency` therefore **errors** when the planned
`model_uuid` differs from the model the live objects came from. It runs as a
`PlanCheck`, after the plan and before anything is imported, so no state has been
written when it fires.

It reads the *planned* value rather than a wrapper variable, and that is what
gives it coverage. An earlier version of this guard read
`wrapper.State.ModelUUID()`, which meant it silently did nothing without
`--source` — the mode in which there is no wrapper and the user's root may carry
the UUID in a tfvars file, a `-var`, an environment variable or a literal.
Terraform has resolved all of those by the time it produces a plan.

The model the live objects came from is treated as ground truth, outranking any
requested value, because it is what every import ID encodes and what lands in
state. Honouring a `--var` that disagrees would write a mismatched UUID into
`main.tf` — the same destruction by a different route.

### What is provider-specific

- **`internal/importer/juju_steps.go`**
  - `JujuModelIdentity` (`PreflightStep`) — derives the model UUID from live
    identities (or the query variable) and folds it into the wrapper *before* the
    plan. It never overwrites a UUID the user set, and warns when no variable
    matches a known convention.

  - `JujuSchemaVersions` (`PostImportStep`) — writes `schema_version` for a
    hardcoded set of resource types (currently `juju_application` → 1) whose
    instances would otherwise sit at version 0. The *rationale* is that the
    provider declares a non-zero `Version` without implementing `UpgradeState()`;
    the *mechanism* is a constant, not a schema read, and so is a third piece of
    hardcoded provider knowledge alongside `jujuIdentityNotImportID` and the
    variable-name conventions.
  - `JujuOfferDefaults` (`PostImportStep`) — normalises null
    `allow_force_destroy` on offers to `false` (schema default).
  - `JujuModelUUIDInjection` (`PostImportStep`) — retained only as a safety net
    for when the preflight step could not determine the UUID.
  - `JujuBuildImportID` (`ImportIDFunc`) — identity-first, with the
    `<model_uuid>:<name>` fallback and the `juju_secret` exception.
  - `JujuModelConsistency` (`PlanCheck`) — refuses a run whose planned
    `model_uuid` differs from the model the live objects came from.
- **`internal/state/juju.go`** — `ExtractModelUUID` and `ExtractModelName`.
- **`internal/wrapper/juju.go`** — `InjectModelUUID` and `ModelUUID` (the
  convention matching described above).
- **`internal/importer/match.go`** — phase 3 of matching is gated on the literal
  type names `juju_integration` and `juju_offer`. Phases 1 and 2 are generic;
  phase 3 is not, and this file is therefore part of the provider surface.
- **`internal/importer/hints.go`** — error classification with Juju-specific
  remediation text (`juju models`, `juju models --format yaml | grep uuid`,
  "The specified Juju model was not found", `--query-var model_uuid=<uuid>`).
  Diagnostics rather than behaviour, but provider knowledge nonetheless.
- **`cmd/atelier/import.go`** — provider detection via
  `strings.Contains(provider, "juju")`, wiring the steps, checks and
  `JujuBuildImportID` into the importer `Options`.

### What remains generic

- **Discovery:** `terraform query` list-resource enumeration is schema-driven.
- **Matching, phases 1 and 2:** a provider-agnostic strategy.
  1. **Identity match** (preferred): when the planned resource has a
     provider-declared identity (TF 1.14+), find live objects whose identity
     matches on all shared keys.
  2. **Name-based fallback:** match by display name against the Terraform
     resource label or the planned attribute `name`.

  **Phase 3 is Juju-specific** and listed above as provider surface. It covers
  integrations, by comparing sorted `application:endpoint` pairs from the planned
  `application` blocks against the five-part live identity; and offers, in two
  sub-phases — first an exact match of the planned offer name against the last
  dot-separated segment of the live offer URL, then, only if that is not
  conclusive, a substring containment test of `application_name` within the URL.
  That containment test is the loosest rule in the matcher and the most likely
  source of a wrong match; it exists because an offer's live identity carries no
  application name.
- **Null/empty normalisation:** `NullEmptyNormalization` reconciles a state
  value that is null where the configuration wants an empty collection, or vice
  versa. Despite its origins as `JujuNullNormalization`, it contains no Juju
  knowledge: null-versus-empty is a provider-SDK representational quirk, and the
  step reads what the configuration wants out of the post-import plan rather than
  from any provider-specific source.
- **Import execution:** `terraform import <address> <id>`.
- **`imports.tf` generation** and the `--dry-run` preview.
- **Artifact lifecycle** and retention, which keys off the run's outcome and
  never off which resource types failed.
- **`tfquery.hcl` generation:** schema-driven list blocks.
- **The pipeline loops** for preflight steps, plan checks and post-import steps:
  `Generate` just calls whatever the caller provides, in order, and treats a plan
  check's error as fatal.
- **`BuildImportIDs`:** the filter separating "already in state" (skip silently)
  from "is a create but has no ID" (report).

### Adding a new provider

1. Implement a `PreflightStep` if any value must reach the plan before it runs.
2. Implement `PostImportStep`s for provider-specific normalisation, if needed.
3. Implement an `ImportIDFunc` — often just "use the identity", with fallbacks
   for types whose identity is not the import ID.
4. Wire them in `cmd/atelier/import.go` under the provider-detection block.

No architectural changes to the importer are needed.

## Consequences

- **The audit invariant softens for import.** The headline claim shifts from
  "zero provider code in the binary" to "provider code exists only in dedicated
  files (`juju_steps.go`, `juju.go`), scoped to import." This is a bounded,
  documented deviation.
- **Provider knowledge shrinks where identity is available.** Identity-based IDs
  removed the `model_uuid` dependency for applications, integrations, offers and
  models. The residue is one exception (`juju_secret`) plus the variable-name
  conventions.
- **Two phases, two interfaces.** The split makes the ordering constraint
  explicit in the type system: a step that must influence the plan cannot be
  registered as a post-import step.
- **Matched-but-unresolved resources are reported, not dropped.** When
  `BuildImportID` returns empty, the resource appears in a dedicated
  *matched but NOT imported* section, warning that a later apply would try to
  create it. This supersedes revision 1's claim that such resources are
  "reported as unmatched" — they were in fact reported nowhere, which is how the
  seven-application defect stayed invisible.
- **Convention matching is documented as fallible, with three independent
  safety nets** (identity-based IDs, a loud miss, and the `--dry-run` count)
  rather than presented as inference.
- **Destructive identity mismatches are refused.** The one failure mode in this
  area that loses data now fails closed.
- **The Juju scope is documented, not enforced in the UI.** These ADRs and the
  Juju-specific error hints make the scope clear, but the command's help text
  presents `juju` only as an example provider, and a non-Juju provider produces no
  scope-specific message: `BuildImportID` is simply nil, so every create is
  reported through the generic "matched but NOT imported" path. Making the
  limitation explicit at the CLI is outstanding work.
