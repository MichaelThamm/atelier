# ADR-0017: Inter-module wiring in the TUI

## Status

Accepted — inspired by Terraform Stacks' `component.<name>.<output>` references.

## Context

Terraform Stacks introduced a component reference syntax
(`component.cos_lite.model_name`) that lets components wire outputs to inputs
declaratively. This is a compelling UX pattern for multi-module composition,
but Stacks requires HCP Terraform — a non-starter for Atelier's local-first
users.

When a wrapper contains multiple modules (ADR-0015), users frequently need to
pass one module's output as another module's input. Today this requires the
user to:

1. Know the upstream module declares a matching output.
2. Type `module.cos_lite.some_output` as the value — a raw HCL expression
   inside a string editor with no validation or discovery.

This is error-prone and defeats Atelier's goal of making module APIs
discoverable.

## Decision

### Wire suggestions

When the user focuses a variable in module B and module A (or any other module
in the wrapper) declares an output whose **type is compatible** with the
variable's declared type, the TUI offers a wire suggestion:

```
model_name (string, required)
  ╰─ Wire to: module.cos_lite.model_name (string)
```

Wire suggestions appear as a selectable hint below the variable name in the
right pane editor. Selecting the suggestion writes a module reference
expression into the wrapper.

### Compatibility rules

A wire is suggested when:

- The output's type is assignable to the variable's type (e.g., `string` →
  `string`, `list(string)` → `list(string)`).
- Name similarity is used for ranking but not gating — all type-compatible
  outputs from other modules in the wrapper are offered, sorted by name
  similarity (Levenshtein or prefix match) then alphabetically.

### What gets written

Selecting a wire suggestion writes the expression as a literal HCL reference
in `main.tf`:

```hcl
module "alerting" {
  source     = "..."
  model_name = module.cos_lite.model_name
}
```

This is standard Terraform — no Atelier-specific syntax. The wrapper remains
independently runnable.

### Wire indicator in the left pane

Variables wired to module references show a distinct marker in the left pane:

- `[→]` — wired to another module's output (not user-edited, not at-default)

### Unwiring

The user can overwrite a wired value by editing the variable normally. This
replaces the module reference with a literal value. `Ctrl+R` (reset to
default) removes the wire and restores the default.

### Scope

- v1.x: wire suggestions for scalar types (`string`, `number`, `bool`) only.
- Future: collection and object type wiring.

## Alternatives considered

### Explicit wiring modal

A dedicated "wiring view" showing all possible connections as a graph.
Rejected as over-engineered for v1; the inline suggestion approach is
lighter and more discoverable during normal editing flow.

### Automatic wiring by name match

Auto-wire variables with identical names across modules without user
confirmation. Rejected — too magical; users should explicitly opt into
cross-module dependencies.

## Consequences

- Brings the best UX idea from Terraform Stacks (declarative component
  wiring) to local-first users without platform lock-in.
- Reinforces Atelier's value as a discovery tool — users learn what outputs
  are available without reading docs.
- Keeps the wrapper as standard Terraform — no custom syntax or runtime.
- The feature is additive; wrappers without wiring work exactly as before.
