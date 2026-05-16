# ADR-0003: GitOps loading model

## Status

Accepted

## Context

Atelier must locate a Terraform module to introspect. The original framing
considered three loading patterns:

1. **Path-based:** Atelier is invoked inside the module's local directory;
   wrapper is written nearby. Requires the user to clone, vendor, or know
   where the module lives.
2. **CLI-arg path:** module is specified as `--module ./path/to/module`.
3. **GitOps:** Atelier accepts a git URL, shallow-clones the repo into a
   managed cache, presents the user with module candidates inside the clone,
   and proceeds against the chosen candidate.

Path-based loading constrains discovery: the user must already have a local
copy of the module. GitOps decouples "find a module" from "have a copy
locally" and aligns with how users actually discover Terraform modules
(browsing GitHub orgs).

A repository may contain multiple Terraform root modules in subdirectories.
For example, the `canonical/observability-stack` repo contains
`terraform/cos`, `terraform/cos-lite`, and `terraform/cos-dev`. We refer to
these as **module candidates** to avoid confusion with git submodules (which
are a different concept entirely).

## Decision

v1 supports two loading modes:

- **Git source (primary):** `atelier init <git-url>` shallow-clones the repo
  into `.atelier/clone/` and presents module candidates for selection. Public
  repositories only.
- **Local source (development):** `atelier init --source <path>` operates on
  a local module directory without cloning. Useful for development of the
  module itself, or local iteration.

The wrapper's `module { source = ... }` records the canonical git-source URL
(`git::https://...?ref=<sha-or-ref>`) regardless of which loading mode was
used. This means Terraform itself fetches the module at `terraform init`
time, independent of Atelier. Atelier's local clone is purely an introspection
aid.

Module candidate identification is **heuristic with manifest override**: any
directory containing `.tf` files with `variable` blocks is a candidate,
excluding `tests/`, `examples/`, and directories referenced by another module
as a child `source = "./..."`. If `atelier.yaml` exists at the clone root, its
`modules:` list overrides the heuristic.

## Alternatives considered

### Path-based loading only

Simpler but constraining. Forces every user to clone or vendor the module
manually. Worse first-run UX for the most common case (discovering and
configuring a published module).

### Registry sources (`namespace/name/provider`)

Out of scope for v1. Registry loading requires a different fetch mechanism
(versioned tarballs from the registry API, not git clones) and a different
URL syntax in the wrapper's `source =`. Deferred to v2.

### Authenticated git access

Out of scope for v1. Adds material complexity (`gh auth`, SSH key handling,
`GITHUB_TOKEN` env, askpass helpers) that is not on the critical path.
Public-only v1 ships a usable tool faster.

### Wrapper-references-local-clone-path

Considered: wrapper's `source = "/home/.atelier/clones/abc/..."`. Rejected
because:

- The clone is Atelier-managed and may be garbage-collected.
- The wrapper would only work on the machine that ran Atelier.
- The wrapper would not be CI-runnable without Atelier.

Using `source = "git::...?ref=..."` makes the wrapper independent of
Atelier's local state.

## Consequences

- `git` must be on the user's `$PATH`. Atelier shells out to `git` for clones
  and `ls-remote`; it does not embed a git library in v1.
- The clone cache lives under `.atelier/clone/` inside the wrapper directory.
  It is regenerable and gitignored. Cache invalidation happens on ref change
  (see [ADR-0007](0007-sparse-wrapper-write-rule.md) for the related
  default-change-surfacing behaviour).
- Wrappers are portable: any machine with `terraform` can apply them. CI
  works.
- Refs are user-editable in the TUI via a modal prompt (`R` key). The modal
  shows the module name and source URL for context. Atelier stores the
  literal user input (e.g., `main`, `v1.2.0`, `abc123`) in the wrapper and
  displays the resolved SHA alongside. Switching refs carries over existing
  variable values, re-clones the module, and runs `terraform init -upgrade`;
  orphaned variable names are listed in the status bar. This enables
  cross-ref upgrade comparisons (e.g., planning at `v1.0` then switching to
  `v2.0` to see the infrastructure delta). Pinning to a SHA is done by
  typing the SHA into the ref prompt.
