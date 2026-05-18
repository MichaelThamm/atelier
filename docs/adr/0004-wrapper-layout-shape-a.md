# ADR-0004: Wrapper layout: Shape A

## Status

Accepted

## Context

[ADR-0001](0001-wrapper-as-durable-artifact.md) established that Atelier
produces a wrapper Terraform project. This ADR pins down *where the wrapper
lives on disk and what is inside it*.

Two shapes were on the table:

- **Shape A:** Wrapper files (`main.tf`, `versions.tf`, `providers.tf`,
  `.gitignore`, `README.md`) live in the current working directory. A hidden
  subdirectory `.atelier/` holds Atelier-managed internal state (module
  clone cache, session metadata). Mirrors how `terraform` itself uses
  `.terraform/`.
- **Shape B:** Everything, including the user-facing `.tf` files, lives
  inside `.atelier/`. The user's CWD has only the `.atelier/` directory.

A separate consideration: should the wrapper directory be the user's choice
(CWD-based) or Atelier-managed (`$XDG_DATA_HOME/atelier/projects/<id>/`)?
The latter avoids requiring users to think about file layout but breaks the
"wrapper as portable, version-controllable artifact" property.

## Decision

**Shape A in the user's CWD.**

- The user runs `atelier init <source>` in a directory of their choosing
  (typically empty, or a directory they intend to make into a wrapper).
  Atelier writes wrapper files (`main.tf`, etc.) directly into CWD.
- A hidden `.atelier/` subdirectory inside the wrapper holds clone cache and
  session metadata. Gitignored. Regenerable.

```
<cwd>/
├── main.tf              # user-visible
├── versions.tf          # user-visible
├── providers.tf         # user-visible
├── outputs.tf           # auto-generated, re-exports module outputs
├── README.md            # user-visible
├── .gitignore           # user-visible
└── .atelier/            # internal, gitignored
    ├── clone/
    ├── cache/
    └── session.json
```

## Alternatives considered

### Shape B (everything in `.atelier/`)

Rejected because:

- It inverts the `.terraform/` convention. `.terraform/` holds *opaque
  internal state*; the user-facing Terraform code (`main.tf`, `variables.tf`,
  `terraform.tfvars`) sits in the parent. Shape B puts the user-facing code
  inside the dotfile directory, which makes hand-editing feel wrong and
  `terraform apply` awkward (requires `terraform -chdir=.atelier`).
- The "hidden TF files" framing turned out to be the wrong intuition. What
  the user wants hidden is *the stuff they shouldn't think about* (clone,
  cache, metadata). The wrapper itself is the *output the user is producing*
  — it should be as visible as any normal Terraform project.

### Atelier-managed wrapper directory (XDG / snap data dir)

Considered as `$XDG_DATA_HOME/atelier/projects/<id>/` or
`$SNAP_USER_DATA/projects/<id>/`. Rejected because:

- The wrapper would not be a normal Terraform project the user can `cd` into
  and apply.
- Sharing with a teammate, committing to git, or running in CI would require
  an "export" action that materializes the wrapper to a user-chosen path.
  This is a separate, error-prone code path.
- Snap distribution does not require the wrapper to be in a snap-managed
  dir. The `home` plug gives a snap access to the user's home directory;
  CWD-based operation works under snap.

### `/var/lib/atelier/...`

Considered briefly. Rejected because `/var/lib/` is for system daemon state
(owned by root or a service user), not user-facing tools. Per-user subdirs
under `/var/lib/` are `$HOME` with extra steps and permission complications.
Snap confinement prevents writing to `/var/lib/` anyway.

## Consequences

- The wrapper is a normal Terraform project. `terraform init && terraform
  apply` in CWD works with zero further configuration.
- Git integration is obvious: `git init && git add . && git commit` works,
  with `.atelier/` correctly gitignored.
- Snap distribution uses the `home` interface. No special data-directory
  handling needed.
- Hand-editing `main.tf` between sessions is supported and round-trips
  correctly (see [ADR-0005](0005-implementation-language-go.md) for the HCL
  AST handling).
- The clone cache and session metadata are regenerable. Deleting `.atelier/`
  forces a re-clone on next `atelier` invocation but does not break the
  wrapper.
- When `atelier` is run in a directory with wrapper files but no `.atelier/`
  (e.g., a teammate freshly cloned the wrapper), Atelier auto-rehydrates:
  parses `main.tf` to recover the module source/ref, re-clones into
  `.atelier/clone/`, repopulates `session.json`, and opens the TUI normally.
