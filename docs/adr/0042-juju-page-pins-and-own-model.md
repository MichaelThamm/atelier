# ADR-0042: Multiple Juju pins, and modules that own their model

## Status

Accepted — extends [ADR-0041](0041-juju-opinionated-gallery-page.md). The
provider-agnostic manifest and the pinned-input gate stand; these pins now render
on the single gallery page ([ADR-0057](0057-single-gallery-page.md)).

## Context

ADR-0041 published a Juju page whose commands are ready to run, and encoded
Juju's model idioms once in `tools/gallerysite/juju.go`. Two assumptions behind
it held for the nine entries the page shipped with and broke as soon as the
gallery grew.

1. **One pin per entry.** `jujuModels` mapped an entry to a single `--var`
   argument. Some modules cannot be pointed at an existing model by that
   argument alone: `charmed-kubeflow-solutions`' products read `model_uuid` only
   when `create_model` is false (`model_uuid = var.create_model ?
   juju_model.kubeflow[0].uuid : var.model_uuid`), and its `iam` product reads a
   second model the same way. Pinning `model_uuid` without the flag yields a
   command that validates and deploys into a model the module created anyway —
   exactly the failure ADR-0041 set out to prevent, wearing a passing command.
2. **Every entry is pin-able.** The tests required each shipped entry to have a
   pin. `indico-operator`'s product module declares `juju_model` with no `count`
   and no input that names an existing model, so there is nothing to pin. Its
   alternatives were to drop a working module from the gallery or to ship a card
   silently missing a variant, which is what the test exists to catch.

The manifest and the CLI are unaffected: this is still one page generator
knowing what Juju means, still with the CLI provider-agnostic.

## Decision

### An entry carries a list of pins

`jujuModels` maps an entry to `[]string` of complete `--var` tokens. A pin sets
one variable; an entry may need several to make the model pin mean anything. The
variant command emits all of them, and `jujuOptionalVars` reports every pin the
manifest does not already carry, so `just gallery-check` scaffolds and asserts
each one.

A pin is a variable and its value, not a position: `pinFor` looks a variable up
by name, so a pin replaces the manifest's placeholder for that variable wherever
the manifest lists it.

### A module that owns its model is declared, not omitted

`jujuOwnModel` names the entries whose module always creates its model, each with
the reason. Such an entry gets no variant; its card says it creates its own
model, and the page banner lists it. `createsOwnModel` and a pin are mutually
exclusive, and a test enforces both directions: every shipped entry is either
pinned or declared, and never both.

### The gate checks that the flag and the pin arrived together

A module that stopped honouring `create_model` would accept the flag, ignore it,
and create its own model — validating cleanly while the pin does nothing. The
gate already requires each pin to reach the wrapper; it now also requires a
wrapper that took `create_model` to have taken `model_uuid`, and shapes each
pin's check value to its variable (a bool for a flag, `{uuid=…}` for an object).

## Alternatives considered

- **Drop `indico` from the gallery.** Rejected: it validates and deploys, and
  excluding a working module because of a site page's completeness rule trades a
  real capability for a tidier grid.
- **Let the pin syntax express a pair, e.g. one token holding two `--var`s.**
  Rejected: a token that is not one variable breaks the name-keyed lookup, the
  gate's per-variable assertion, and the shell-quoting guarantee. A list of
  single-variable pins keeps each one independently checkable.
- **Teach `atelier apply` about `create_model`.** Rejected as scope: the flag is
  one module's idiom, and ADR-0041 already places that knowledge in the site
  generator so the binary stays provider-agnostic.
- **Have the gate assert the flag's effect rather than its presence.** Deferred:
  that needs a plan against a real model, which is a Juju cloud tier rather than
  a scaffold check.

## Consequences

- The Juju page covers every shipped entry, either with a runnable variant or a
  stated reason it cannot have one.
- `just gallery-check` exercises two pins it did not before and fails if either
  stops reaching the wrapper, or if a flag arrives without the pin it enables.
- A module that becomes pin-able later is a one-line move from `jujuOwnModel` to
  `jujuModels`; a test keeps the two sets disjoint either way.
- The gate's pin handling grows a case per pin shape. A new shape is a small
  `case` in the justfile, and a wrong value fails loudly at the entry that
  introduced it.