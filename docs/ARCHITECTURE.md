# Architecture

Atelier is a provider-agnostic terminal UI for configuring Terraform modules. It
reads a module's declared API, presents it as an editable surface, writes a
small runnable wrapper, and drives `terraform plan` / `apply`.

This page is the **current** shape. The [ADRs](adr/) are the historical record of
each decision and its rationale; [SPEC.md](SPEC.md) is the behavioural source of
truth. Read this first, then SPEC, then the ADR a claim cites.

## The artifact

- The **wrapper** is the durable output: a normal Terraform directory Atelier
  writes into a target, runnable without Atelier installed
  ([ADR-0001](adr/0001-wrapper-as-durable-artifact.md),
  [ADR-0004](adr/0004-wrapper-layout.md)).
- Writes are **sparse-plus-required** — required variables always, optionals only
  when they differ from the declared default — and preserve hand edits and
  comments ([ADR-0007](adr/0007-sparse-wrapper-write-rule.md)).
- `.atelier/` is regenerable internal state (clone cache, session, logs); the
  wrapper never depends on it ([ADR-0003](adr/0003-gitops-loading.md)).

## The module contract

- Atelier reads a module's **whole declared contract**: variables and outputs
  ([ADR-0060](adr/0060-module-contract-outputs.md)).
- It generates an `outputs.tf` re-exporting each module's outputs as root
  outputs, named `<module>_<output>`, so `terraform output` works in the wrapper
  ([ADR-0060](adr/0060-module-contract-outputs.md)).

## Composition and interfaces

- A wrapper may hold several modules. `--dir` names the wrapper to compose into;
  a target holding a `main.tf` is always the additive case
  ([ADR-0015](adr/0015-multi-module-grouping.md),
  [ADR-0044](adr/0044-dir-names-the-wrapper.md)).
- A **producer** publishes a typed interface catalogue — an output keyed by the
  interface each offer satisfies (the names its charms declare). A **consumer**
  accepts a matching input. Atelier matches whole objects by name and type
  ([ADR-0062](adr/0062-typed-module-interfaces.md)).
- For an **object** input Atelier wires the whole object; for a **`map(string)`**
  input it offers a subset. A wired value is unknown until the producer applies,
  so a consumer gates integrations on `keys(...)`, never on the value.
- An Atelier-written wire is **not final**: an existing wire is reported with its
  current selection and can be adjusted and overwritten; `Ctrl+R` unwires an
  input.
- The wrapper is a **root graph**: nodes are `module` blocks, edges are
  argument-level `module.<name>.<output>` references
  ([ADR-0061](adr/0061-root-graph.md)).

## Plan and apply

- The TUI drives `terraform plan` and, from the plan view, `terraform apply`,
  reaching Terraform through narrow interfaces, not directly
  ([ADR-0002](adr/0002-author-and-plan-scope.md),
  [ADR-0011](adr/0011-plan-output-tree.md)).
- Applying hands the terminal to Terraform rather than re-rendering its output
  ([ADR-0052](adr/0052-hand-the-terminal-to-terraform.md)).

## Curation

- The bundled **gallery** is a curated set of quick starts, gated by
  `gallery lint` / `gallery check`
  ([ADR-0039](adr/0039-composed-gallery-presets.md)).
- **Presets** are Terraform-native `.tfvars` bundles; Atelier never reads a
  bespoke manifest from upstream
  ([ADR-0031](adr/0031-presets-as-tfvars-bundles.md)).

## Scope boundaries

Atelier configures **one Terraform root**. It is not an orchestration,
multi-environment, or remote-state tool, and it carries **no provider
vocabulary** — matching is name and type over the keys a producer publishes
([ADR-0016](adr/0016-scope-boundaries-no-orchestration.md)).

## Layering

Dependencies point downward only:

```
leaves (gitops, session, state, tfexec, tftypes, tfvars, modulesource, candidate)
   → domain (wrapper, rootgraph)
   → orchestration (bootstrap)
   → adapters (cmd/atelier, internal/tui, internal/importer)
```

Reuse before build (see the table in [AGENTS.md](../AGENTS.md)); `tools/codecheck`
fails on an `internal/` package unreachable from `cmd/`, and
`tools/deadcodecheck` on a function reachable from neither the binary nor a test.
