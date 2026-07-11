# ADR-0016: Scope boundaries — no orchestration overlap with Terragrunt

## Status

Accepted

## Context

With multi-module support (ADR-0015), Atelier's surface expands toward
territory occupied by Terragrunt and Terraform Stacks. Without explicit
boundaries, feature requests will pull Atelier into orchestration,
environment fan-out, and remote state management — domains where mature tools
already dominate and where Atelier has no competitive advantage.

Atelier's core value is **interactive discovery and type-aware configuration
of Terraform module APIs**. This value is orthogonal to orchestration.

### Tools in adjacent space

| Tool | Domain | Cloud account required |
|------|--------|-----------------------|
| Terragrunt | DRY multi-env orchestration, dependency DAGs across roots | No (local binary) |
| Terraform Stacks | Component-based multi-deployment orchestration | Yes (HCP Terraform only) |
| Atelier | Interactive module API discovery and configuration | No (local binary) |

Atelier's users have a Juju cloud and want to deploy/test locally. They
should never need a cloud account or platform lock-in.

## Decision

Atelier will NOT implement:

1. **Multi-environment fan-out.** No directory hierarchy for dev/staging/prod.
   One wrapper = one environment. Users who need fan-out export the wrapper
   into a Terragrunt directory structure or use Terraform workspaces.

2. **Cross-root dependency orchestration.** No `dependency {}` blocks, no
   DAG execution across separate state files, no `run_all`. Atelier operates
   on a single Terraform root (the wrapper).

3. **DRY config inheritance.** No `include` blocks, no parent/child config
   merging. The wrapper is self-contained.

4. **Remote state management.** No auto-configuring S3/GCS/Azure backends.
   Backend configuration is the user's responsibility (hand-edit or
   `terraform init -backend-config=...`).

5. **Deployment rollout orchestration.** No approval gates, phased rollouts,
   or deployment groups. `terraform apply` is the deployment mechanism.

6. **Platform lock-in.** No features that require HCP Terraform, Spacelift,
   Env0, or any managed platform account.

Atelier WILL stay focused on:

1. **Module API discovery** — clone, parse variables, present them
   interactively.
2. **Type-aware editing** — per-type TUI widgets with validation.
3. **Sparse-write** — only emit non-default values (ADR-0007).
4. **Ref lifecycle** — switch refs with value carry-forward and orphan
   detection.
5. **Multi-module composition within a single root** — add/remove modules in
   one wrapper, configure them interactively (ADR-0015).
6. **Inter-module wiring** — suggest output→input connections between modules
   in the same wrapper (see ADR-0017).

## Consequences

- Feature requests for multi-env support get a clear "out of scope" answer
  with a documented alternative (export wrapper, use Terragrunt).
- Atelier remains complementary to Terragrunt: users can use Atelier to
  author a wrapper, then drop it into a Terragrunt hierarchy.
- The single-state constraint keeps the codebase simple and the UX
  predictable.
- Users who outgrow Atelier's single-wrapper model have a natural graduation
  path rather than a half-baked built-in alternative.
