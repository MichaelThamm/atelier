# ADR-0008: Provider schema via `terraform providers schema -json`

## Status

Accepted

## Context

The wrapper must contain `provider "<name>" {}` blocks for any provider the
target module requires. Provider configuration attributes — `controller_addresses`,
`username`, `password`, `ca_certificate` for the juju provider; `region`,
`access_key`, etc. for AWS — are not declared by the module. They are
declared by the *provider itself*, accessible via the provider's
configuration schema.

For Atelier to render an editor for the provider's configuration, it needs
the schema. Three options:

- **(a) Run `terraform providers schema -json`** after `terraform init` to
  obtain the schema directly from the provider plugin. Authoritative for any
  provider; works without Atelier knowing the provider in advance.
- **(b) Bundle known provider schemas** inside the Atelier binary. Zero
  network latency; goes stale on every provider release; only supports
  providers Atelier explicitly knows about.
- **(c) Skip provider-config editing entirely.** User hand-writes
  `providers.tf`. Defeats the "configure visually" pitch for one of the most
  consequential blocks in any wrapper.

## Decision

**Option (a):** run `terraform init` (which downloads the provider plugin)
followed by `terraform providers schema -json` (which queries the plugin for
its schema). Cache the resulting JSON per `<provider-source>@<version>` in
`.atelier/cache/providers/<source>@<version>.json` so subsequent sessions
are instant.

This runs as part of the bootstrap sequence on `atelier init`, before the
user sees the variable editor. Approximate cost on first run: 5–15 seconds
depending on provider size and network. On subsequent sessions: zero, the
cached JSON is read directly.

## Alternatives considered

### Bundled schemas (b)

Goes stale on every provider release. Locks Atelier into the providers it
chooses to bundle, defeating the "works for any Terraform module" goal.
Rejected.

### Hand-written providers.tf (c)

Defeats the value proposition. Provider configuration is one of the most
frequent sources of friction (especially around secret values like
`password` and `secret_key`); the TUI should help with it, not avoid it.
Rejected.

### Defer the init step

We considered deferring `terraform init` to first-plan time. Rejected
because:

- The schema is needed *immediately* to render the provider configuration
  pane. Without it, we can't show any provider attributes.
- `terraform init` also populates `.terraform/modules/` and
  `.terraform.lock.hcl`, both of which the user needs at apply time. Doing it
  early is not wasted work.

## Consequences

- `terraform` must be on `$PATH` before `atelier init` runs. The presence
  check is part of the CLI-level pre-launch validation; failures error out
  cleanly with an actionable message.
- First-run latency on `atelier init` includes the time to fetch the module
  clone (small, shallow) and to `terraform init` the chosen candidate
  (variable; provider-plugin-bound). The TUI does not open until the
  bootstrap is complete; the CLI shows progress.
- The schema cache lives under `.atelier/cache/providers/` and is keyed by
  `<provider-source>@<version>`. Cache invalidation happens on version
  change (which requires `terraform init -upgrade` anyway).
- Sensitive provider attributes (flagged `sensitive: true` in the schema)
  are rendered with masking in the TUI and handled by the secrets-handling
  rules; see [ADR-0009](0009-secrets-handling-v1.md).
- The TUI surfaces the provider as a top-level pseudo-group `Provider:
  <name>` in the left pane. Its fields are edited identically to module
  variables; the wrapper-write rule from [ADR-0007](0007-sparse-wrapper-write-rule.md)
  applies (sparse-plus-required).
