# ADR-0013: Snap packaging

## Status

Proposed

## Context

Atelier is currently distributed as a static Go binary via `go install`. For
broader adoption — especially among users who may not have a Go toolchain
installed — a [snap](https://snapcraft.io/) provides an installation path
that is familiar to Ubuntu users and gets automatic updates via the Snap
Store.

The two key design axes are:

1. **Confinement model:** `strict` (sandboxed, needs declared plugs for
   filesystem/network access) vs `classic` (full host access, like a
   traditionally installed binary).
2. **Build platforms:** which architectures to support.

### Confinement tradeoffs

Atelier's runtime needs are:

| Capability | Why | Plug (strict) |
|---|---|---|
| Read/write CWD and subdirs | Wrapper files, `.atelier/` cache | `home` |
| Execute `terraform` or `tofu` | Plan, apply, validate, init, output | `home` (if terraform is a snap or in `$HOME`) |
| Execute `git` | Clone modules, `ls-remote` for ref resolution | — (bundled or from base) |
| Network access | `git clone`, `terraform init` (provider downloads) | `network` |
| Read `$HOME/.terraformrc` | Provider mirror/plugin cache config | `home` |

The `terraform` snap itself uses **classic confinement** because Terraform
needs to read provider plugins from arbitrary paths, write state to arbitrary
backends, and execute arbitrary provider binaries. This is the main tension:

- **Strict confinement for Atelier** is feasible in principle — Atelier's
  own filesystem access is limited to CWD (wrapper) and `$HOME` (for
  `.terraformrc`, SSH keys used by git). The `home` and `network` plugs
  cover this.
- **However**, Atelier shells out to `terraform`. If `terraform` is
  installed as a classic snap, a strictly-confined Atelier snap cannot
  execute it (snap sandboxes do not compose across confinement boundaries).
  If `terraform` is a deb or a standalone binary in `/usr/bin` or
  `/usr/local/bin`, a strict snap can execute it *only* if those paths are
  accessible (they are, via the base snap's root filesystem).
- If `terraform` is installed in `$HOME/bin` or `$HOME/go/bin`, the `home`
  plug provides access.

The practical risk with strict confinement: users who installed `terraform`
via its own classic snap will hit a confusing "terraform: command not found"
error from inside Atelier's sandbox. This is the single most likely
installation combination on Ubuntu.

### Staged approach

Start with **classic confinement** to match the `terraform` snap's model and
avoid the cross-snap execution problem. This lets Atelier work with any
`terraform` installation without friction. Once the user base and failure
modes are understood, evaluate migrating to strict confinement — potentially
by bundling a `terraform` binary inside the Atelier snap or by using
content-sharing interfaces.

### Build platforms

Atelier targets Linux; the primary user base is Ubuntu on `amd64`. An
`arm64` build is straightforward (Go cross-compiles trivially) but adds CI
time and Store maintenance for a platform with very few current users. Start
with `amd64` only; add `arm64` when there is demand.

## Decision

**Ship Atelier as a classic-confinement snap, building for `amd64` only.**

A migration to strict confinement is desirable and should be revisited once:

1. The cross-snap execution story for `terraform` is resolved (e.g.,
   Terraform moves to strict, or Atelier bundles its own `terraform`
   binary).
2. Real user feedback confirms the plug set (`home`, `network`) is
   sufficient for all common workflows.

### Proposed `snapcraft.yaml`

```yaml
name: atelier
base: core24
summary: Terminal UI for configuring Terraform modules
description: |
  Atelier presents a Terraform module's variables as an editable two-pane
  TUI. It produces a wrapper Terraform project — a main.tf calling the
  module via its git source, with only the values the user chose to set.
  Plan and apply run inside the TUI; the wrapper is independently runnable
  without Atelier installed.
adopt-info: atelier
confinement: classic
grade: stable

platforms:
  amd64:
    build-for: [amd64]

apps:
  atelier:
    command: bin/atelier

parts:
  atelier:
    plugin: go
    source: ./
    build-snaps:
      - go/latest/stable
    override-pull: |
      craftctl default
      # Derive version from git tag or commit.
      version="$(git describe --tags --always --dirty 2>/dev/null || echo "dev")"
      craftctl set version="${version}"
    override-build: |
      go build -o "${CRAFT_PART_INSTALL}/bin/atelier" ./cmd/atelier
```

### Key choices in the snapcraft.yaml

- **`base: core24`** — Ubuntu 24.04 base snap. Provides `git` in the base
  filesystem, so Atelier's `git` subcommands work without bundling a
  separate `git` part. `core22` would also work but `core24` aligns with
  the project's target platform.
- **`adopt-info: atelier`** — version is derived from `git describe` during
  the build, so the snap version tracks the release tag automatically.
- **`plugin: go`** — uses the Go snapcraft plugin; the `go` build snap
  provides the compiler. The resulting binary is statically linked (default
  for Go on Linux/amd64 with CGo disabled), so no runtime library
  dependencies beyond the base snap.
- **`confinement: classic`** — full host access. Atelier can find and
  execute `terraform` regardless of how it was installed. No `plugs:`
  declaration needed (classic snaps have unrestricted access).
- **`platforms: amd64` only** — single architecture to start. Adding
  `arm64` later is a one-line change.
- **No daemon, no service** — Atelier is a user-facing CLI/TUI tool, not a
  background service.

### Snap Store channel strategy

- `latest/edge` — every push to `main`.
- `latest/beta` — release candidates.
- `latest/stable` — tagged releases.

## Alternatives considered

### Strict confinement from day one

Would provide better security isolation but creates a hard dependency on
`terraform` being reachable from within the sandbox. The most common Ubuntu
installation (`snap install terraform --classic`) is unreachable from a
strict snap. Workarounds:

- **Bundle `terraform` inside the snap.** Feasible but creates a version
  coupling problem (which Terraform version? how to update independently?)
  and inflates the snap size. Also raises licensing questions (Terraform is
  BSL 1.1 since v1.6).
- **Content interface.** A content-sharing interface between the `terraform`
  snap and the `atelier` snap could expose the `terraform` binary. This
  requires cooperation from the Terraform snap publisher (HashiCorp) and is
  not currently supported.
- **Require deb-installed Terraform.** Strict snap can access
  `/usr/bin/terraform`. Viable but forces users into a specific installation
  method, which conflicts with Atelier's "works with your existing setup"
  design goal.

Starting strict and then discovering these issues in production is worse than
starting classic and tightening later.

### Deb package (`.deb`)

A deb provides the same "no Go toolchain needed" benefit. Rejected as the
*primary* distribution because:

- No automatic updates. Users must re-add the PPA and `apt upgrade`, or
  manually download new `.deb` files.
- PPAs require maintenance (GPG keys, repo hosting, per-series builds).
- Snap Store gives automatic updates, channel management, and a single
  build artifact per architecture.

A deb may be offered alongside the snap for users who prefer it, but it is
not the primary distribution vehicle.

### Flatpak

Not a natural fit for CLI/TUI tools. Flatpak is designed for graphical
desktop applications with portals for filesystem access. A CLI tool running
inside a Flatpak sandbox would need `flatpak run` wrapping, which is
awkward for terminal workflows.

### Container image (OCI)

Viable for CI use cases but poor for interactive TUI use (requires
`docker run -it` with terminal passthrough, volume mounts for CWD, and
network access configuration). The snap is a better fit for the primary
interactive use case; a container image could supplement it for CI.

## Consequences

- Atelier gains an installation path that works without a Go toolchain:
  `snap install atelier --classic`.
- Classic confinement means Atelier has full host access, matching the
  `terraform` snap's model. Users' existing `terraform` installations
  (snap, deb, binary, `asdf`, `mise`, etc.) all work without configuration.
- The snap version tracks git tags via `adopt-info` + `git describe`.
- Only `amd64` is built initially. Adding `arm64` is a one-line platforms
  change when demand warrants it.
- A future migration to strict confinement is possible but requires
  resolving the `terraform` execution story first. This ADR will be
  superseded by a new ADR if/when that migration happens.
- CI needs a snapcraft build step. This can be a GitHub Actions workflow
  using `snapcore/action-build` and `snapcore/action-publish`.
