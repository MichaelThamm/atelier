# ADR-0061: Import derives the Juju model from the target, not the command line

## Status

Accepted.

## Context

`atelier import` discovers live objects with `terraform query`, and the Juju
provider's list resources need a model UUID to know which model to enumerate.
The CLI therefore asks for `--query-var model_uuid=…` on every run.

That value is usually already in the target. A wrapper records it as
`model.uuid` or `model_uuid`, because that is what the module needs to manage
the deployment; after a partial or repeated import, `terraform.tfstate` records
it on every `juju_application` as well. [ADR-0027](0027-atelier-import.md)
already says import must never ask for a value it can derive, and the identity
tier is explicitly exempt from user input. The query runs before the preflight
step that derives identity values from live objects, so that step cannot fill
the gap; the CLI can, before the query file is written.

## Decision

The provider contract gains `QueryDefaults(explicit, wrapperState, dir)`. It
returns query-engine inputs the target already records, and may refuse an
explicit value that contradicts one. `atelier import` merges the result under
the user's `--query-var` values and passes the merged map to discovery.

For Juju, the model UUID is resolved in this order:

1. `--query-var model_uuid=…`, if supplied.
2. The wrapper's `model.uuid`, then `model_uuid`.
3. `juju_application.model_uuid` in existing `terraform.tfstate`.

A wrapper whose model does not appear in state is refused before the query, as
is an explicit UUID that contradicts a recorded one. `model_uuid` forces
replacement on every Juju resource, so adopting one model's resources while
planning or holding state for another would leave the next apply destroying
them. State that spans several models is accepted when it includes the wrapper's
model (a wrapper may compose modules from more than one); with no wrapper model,
an explicit `--query-var` selects which one this run enumerates.

Atelier does not shell out to `juju`, does not read a page-specific environment
variable, and does not infer a UUID from a model name. None of those is an
identity Atelier can verify, and the first is outside the provider-agnostic
boundary ([ADR-0016](0016-scope-boundaries-no-orchestration.md)).

## Alternatives considered

- **Keep requiring `--query-var`.** Rejected: it repeats a value the wrapper or
  state already holds, and it turns "which deployment am I adopting" into an
  internal query-engine detail the user has to look up separately.
- **Shell out to `juju show-model` or `juju models`.** Rejected: it puts a
  provider's CLI and output format in the binary, which ADR-0016 rules out. The
  gallery page already reads the model once into `CURRENT_MODEL`
  ([ADR-0049](0049-juju-page-model-from-environment.md)); that is a documentation
  convention, not something the product should silently depend on.
- **Read `CURRENT_MODEL` from the environment.** Rejected for the same reason:
  a stale export would select a model the user did not intend, and the variable
  exists to serve the generated page rather than the CLI contract.
- **Infer the UUID from `model.name`.** Rejected: a name is not an identity, and
  resolving it needs controller access Atelier does not have.
- **Prompt for the UUID when it is unknown.** Deferred. A fresh wrapper with no
  model and no state still has nothing to derive from; `--query-var` keeps that
  case scriptable, and the existing missing-argument hint already names it.

## Consequences

- `atelier import juju` works without a model flag in a wrapper that already
  names the model, and after a partial import that left state behind.
- A fresh wrapper with no model and no state still needs
  `--query-var model_uuid=…`; nothing else can identify the model to enumerate.
- Some mismatches are now refused earlier. A wrapper targeting one model while
  state holds another is refused before the query instead of producing a plan
  that cannot be reconciled; an explicit value contradicting either is refused
  for the same reason.
- Adding an import provider now includes deciding what query inputs its target
  already records. Returning nil is valid for providers whose list resources
  need none.
