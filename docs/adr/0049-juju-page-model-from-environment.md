# ADR-0049: The Juju page reads the model from the environment

## Status

Accepted — supersedes [ADR-0041](0041-juju-opinionated-gallery-page.md)'s
per-command model substitution. [ADR-0042](0042-juju-page-pins-and-own-model.md)'s
pins and
[ADR-0043](0043-juju-page-guesses-nothing.md)'s environment-over-guessing rule
stand.

## Context

The Juju page resolved the reader's model inside every command it printed:

```bash
atelier apply airbyte \
  --var model_uuid="$(juju show-model --format json | jq -r '.[]."model-uuid"')"
```

Twenty-one cards carried that substitution — twenty-two occurrences, since
Charmed Kubeflow pins a second model. Every one of those lines is a command the
reader has to decode before running, and the page asks for `juju` and `jq` on
`PATH` where a name the reader exports once would do.

ADR-0043 already settled this for credentials: the S3 trio is read from the
environment rather than resolved per command, because the value is the reader's
and Atelier cannot know it. The model is the same kind of input — which model to
deploy into is the reader's choice, made with `juju switch`.

## Decision

- The banner exports the model once, immediately after the `juju switch` that
  selects it:

  ```bash
  export CURRENT_MODEL="$(juju show-model --format json | jq -r '.[]."model-uuid"')"
  export CURRENT_MODEL_NAME="$(juju show-model --format json | jq -r '.[]."short-name"')"
  ```

- Every pin reads `$CURRENT_MODEL`, or `$CURRENT_MODEL_NAME` for the modules
  that take a model *name*. The three shapes ADR-0041 encoded — a UUID, an
  object whose `uuid` selects a model, a name — are unchanged; only where the
  value comes from moves. `juju show-model` appears on the page exactly twice,
  in the export.
- A pin is still a complete, shell-quoted `--var` token, so a card's command
  pastes as-is once the model is exported.
- The per-card statements about which entries demand a model and which entries
  override their module's own are unchanged. Where the value comes from says
  nothing about whether pinning it changes behavior.

## Alternatives considered

- **Leave the substitution in each command.** Rejected: it repeated one line of
  shell twenty-two times, and every command on the page opened with a pipeline
  the reader must parse before knowing what it deploys.
- **Read the model in Atelier, by shelling out to `juju show-model`.** Rejected:
  it puts a provider's CLI and its output format into the binary, which
  [ADR-0016](0016-scope-boundaries-no-orchestration.md)'s provider-agnostic
  boundary rules out. The page is where the opinion is affordable.
- **Export one name carrying both forms.** Rejected: a UUID and a model name are
  different strings, and one variable holding both would be a value the reader
  has to parse. Two exports, once, is simpler.
- **Guard the variable, `${CURRENT_MODEL:?…}`.** Deferred: it fails loudly rather
  than passing an empty UUID, but the S3 pins already rely on the reader's
  environment, so a guard on the model alone is inconsistent. Reconsider if a
  reader reports deploying with an empty `model_uuid`.
- **Drop the "instead of creating one" variants entirely.** Rejected: for six
  entries the pin overrides what the module would have done, and hiding that is
  the failure ADR-0041 set out to prevent.

## Consequences

- A card is one line per input, and reads the same whether the module wants a
  UUID, an object, or a name.
- A pasted card command depends on the reader having exported the model; an
  unset variable reaches Terraform as an empty string, exactly as an unset
  `S3_ENDPOINT` does today. The banner states the export directly above the
  cards, so switching models means re-running it.
- The page no longer depends on `jq` per command, only per shell.