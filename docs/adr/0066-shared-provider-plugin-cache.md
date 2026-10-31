# ADR-0066: Share one Terraform provider plugin cache

## Status

Accepted.

## Context

Every `atelier apply` into a **new** target directory starts from a cold
`.terraform/`, so Terraform re-downloads the provider and every module for that
root. The shallow-clone default ([ADR-0065](0065-shallow-git-clones-by-default.md))
removed the fat git history, but a fresh `cos-lite` wrapper still spends most of
its `terraform init` on the juju provider: measured on a throttled link, a fresh
init is ~37 s, of which ~12 s is the ~70 MB provider download and the rest is
modules plus registry round-trips; a warm re-init in the same wrapper is ~1.5 s.

Running several `atelier apply` commands — a different root each time — re-pays
that provider download every time. It is the single largest avoidable transfer,
and the fix is one environment variable: `TF_PLUGIN_CACHE_DIR`, which makes
Terraform install a provider into a shared directory once and symlink it into
each root's `.terraform/providers`.

## Decision

**Atelier points Terraform at a shared, per-user provider plugin cache.**

- `EnablePluginCache` sets `TF_PLUGIN_CACHE_DIR` to
  `<user cache>/atelier/terraform/plugins` (via `os.UserCacheDir`), creating the
  directory first. It runs once, from `main`.
- It never overrides a value the user already set, and it is best-effort: if the
  cache directory cannot be created, it is skipped, because a missing cache must
  never stop Terraform from running.
- It sets the **process** environment rather than a per-command one, because
  `terraform-exec`'s `SetEnv` replaces the whole environment and refuses the
  `TF_VAR_*` / `TF_CLI_ARGS_*` entries a user may legitimately have set.
  `terraform-exec` reads `os.Environ` for every command, so a process variable
  reaches Terraform without dropping anything.

This is the smallest, highest-value piece of a larger caching story. Reusing
module downloads across wrappers, and carrying `.terraform.lock.hcl` to skip the
registry round-trips, are separate decisions with their own correctness weight
(the module cache in particular cannot be keyed by source string alone, because a
branch ref can move) and are deliberately not made here.

## Alternatives considered

- **Leave it to the user to set `TF_PLUGIN_CACHE_DIR`.** Works today with no
  code, but leaves the default slow and puts the burden on every user. Worth
  documenting; not a substitute for a sane default.
- **A provider filesystem mirror via `TF_CLI_CONFIG_FILE` and
  `provider_installation`.** Equivalent effect with more configuration, and it
  can redirect to a read-only mirror Atelier cannot populate. The plugin cache
  is the smaller change.
- **A per-command env via `terraform-exec`'s `SetEnv`.** Rejected: it replaces
  the environment wholesale and rejects `TF_VAR_*` / `TF_CLI_ARGS_*`, so it would
  silently drop variables a user set for Terraform.
- **Cache module downloads here too.** Deferred: reusing `.terraform/modules`
  is keyed by a source string that does not record the resolved commit, so a
  moved branch ref would serve a stale checkout. It needs SHA-keying and is a
  separate change.

## Consequences

- A provider is downloaded once per machine instead of once per wrapper; the
  first `terraform init` that needs it populates the cache and later ones
  symlink from it. On a fresh wrapper this removes the largest single transfer.
- The cache lives under the user cache directory and grows with the providers
  used; a user who wants a different location (or none) sets
  `TF_PLUGIN_CACHE_DIR` themselves, which Atelier leaves untouched.
- Concurrent inits are safe: Terraform locks the cache while populating it.
- The wrapper is unchanged and stays independently runnable
  ([ADR-0001](0001-wrapper-as-durable-artifact.md)); this only affects how the
  Terraform Atelier drives fetches providers.
- Module downloads and provider registry round-trips remain and are the next
  levers; this ADR does not claim to make `terraform init` free, only to stop
  re-downloading the provider.
