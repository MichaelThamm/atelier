# ADR-0001: Wrapper module as durable artifact

## Status

Accepted

## Context

Atelier reads and writes Terraform configuration on behalf of the user, and
the question is: *what is the durable output?* Four plausible shapes were
considered:

1. A `terraform.tfvars` file living inside the target module's directory.
2. A wrapper Terraform project (a directory with a `module {}` block calling
   the target module).
3. Direct edits to the target module's `.tf` files.
4. An in-memory configuration piped into `terraform plan -var=…` with no
   persistent artifact.

Options (3) and (4) were dismissed as obviously wrong: (3) pollutes a
repository the user does not own; (4) loses all reproducibility and CI
integration.

The choice between (1) and (2) is a real tradeoff. (1) is more machine-
parseable and amenable to existing tooling (TFC, Atlantis, etc.). (2) is more
human-readable, supports multi-instance deployments without workspace
gymnastics, and is more amenable to documentation and infallible-docs use
cases.

## Decision

Atelier produces a **wrapper Terraform project** — a directory containing a
`module {}` block that calls the target module via its git source, plus
supporting files (`versions.tf`, `providers.tf`, `.gitignore`, `README.md`).

The wrapper lives at the user's current working directory (Shape A; see
[ADR-0004](0004-wrapper-layout-shape-a.md)) and is independently runnable:
`terraform init && terraform plan && terraform apply` works without Atelier
installed.

## Alternatives considered

### tfvars file (option 1)

- **Pros:** trivial round-trip parsing; well-defined format; one fewer layer
  of module nesting; existing tooling already understands tfvars.
- **Cons:** writing to the target module's directory pollutes a repo the user
  doesn't own; multi-instance deployments require workspaces or multiple
  tfvars files in the same directory, which is awkward; less grokable for
  documentation purposes; the wrapper is not version-controllable as a unit.

### Wrapper module (chosen)

- **Pros:** human-readable; multi-instance is naturally one directory per
  instance; version-controllable, shareable, CI-runnable; documentation and
  examples write themselves.
- **Cons:** provider configuration must be authored fresh (vs. inheriting
  from a hypothetical "open the target module" mode); read-existing-state
  requires parsing arbitrary `module {}` block arguments (vs. parsing a
  straightforward tfvars file).

## Consequences

- The wrapper is the artifact that survives across Atelier sessions, gets
  committed to git, and runs in CI.
- Atelier must write HCL that preserves user-added comments and formatting on
  re-save. This drives the implementation-language choice ([ADR-0005](0005-implementation-language-go.md)).
- Atelier must author provider configuration blocks (the target module
  doesn't supply them). This drives [ADR-0008](0008-provider-schema-discovery.md).
- The wrapper's `source = "..."` uses Terraform's native git-source syntax
  (`git::https://...?ref=...`), allowing Terraform itself to fetch the module
  at `init` time. Atelier's local clone (see [ADR-0003](0003-gitops-loading.md))
  is purely for introspection and does not participate in the apply path.
