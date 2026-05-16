# ADR-0009: Secrets handling in v1

## Status

Accepted

## Context

Provider configuration commonly includes sensitive attributes — `password`,
`secret_key`, `token`, `ca_certificate` private bytes, etc. The provider's
configuration schema flags these as `sensitive: true`. Variable declarations
may also be marked `sensitive = true` in the module.

Atelier writes the wrapper as files on disk in the user's working directory.
Anything written into `providers.tf` or `main.tf` is, by default, a
candidate for committing to git. Inlining a secret value into a tracked
configuration file is a known foot-gun.

Three options for handling sensitive values:

- **(α) Variable indirection + gitignored tfvars.** Atelier generates
  `provider "X" { password = var.x_password }`, declares `variable "x_password"
  { sensitive = true }` in `variables.tf`, and writes the value to
  `secrets.auto.tfvars` which is gitignored. Atelier reads the value back
  from `secrets.auto.tfvars` on next session.
- **(β) Environment variables.** Many providers support env-var forms
  (`JUJU_PASSWORD`, `AWS_SECRET_ACCESS_KEY`, etc.). Wrapper omits the value
  entirely; the TUI just notes "set via env var X". Cleanest for security
  (no on-disk persistence), but provider-dependent and unsupportable for
  every attribute on every provider.
- **(γ) Hybrid per-field choice.** TUI offers, per sensitive field, "store
  in `secrets.auto.tfvars` (gitignored)" vs. "set via env var `TF_VAR_x_password`".

The user agreed during design that **v1 is targeted at a development trust
model with no sensitive secrets in scope.** This ADR documents the v1
posture explicitly so it is not forgotten when the project graduates to
production use cases.

## Decision

v1 implements **(α): variable indirection with gitignored
`secrets.auto.tfvars`**, with the following explicit limitations.

### v1 limitations (read this carefully)

1. **Development-environment trust model.** v1 assumes the user is operating
   in a single-user development environment on a machine they own and trust.
   The threat model does *not* include malicious local users, compromised
   developer machines, or accidental disk imaging that exposes home
   directories.
2. **Disk-persistence is the security boundary.** The `secrets.auto.tfvars`
   file lives in the wrapper directory in plaintext (HCL). Its protection
   depends on the user's filesystem permissions, full-disk encryption
   posture, and the wrapper directory not being accidentally committed.
   Atelier provides the gitignore entry; it does not provide encryption,
   key wrapping, or access control beyond OS file permissions.
3. **No env-var alternative in v1.** Option (β) is deferred. Users who want
   env-var-based secret injection can hand-edit `providers.tf` and not use
   the TUI's secret editor for those fields.
4. **No external secret managers in v1.** Vault, AWS Secrets Manager, cloud
   KMS integration, age/sops encrypted files — all out of scope. Atelier
   does not provide an abstraction over secret backends.
5. **No per-field source toggle.** Option (γ) is deferred to v2 (see
   [ROADMAP](../ROADMAP.md)).
6. **Sensitive variable read-back may be implicit.** Atelier reads
   `secrets.auto.tfvars` on session open and shows the current value (masked,
   with reveal toggle). This means the file's contents are surfaced inside
   the TUI; any screen-sharing or recording of an Atelier session has the
   same secret-handling considerations as any tool that displays a secret
   value.
7. **Git accidents are not fully prevented.** `secrets.auto.tfvars` is in
   the auto-generated `.gitignore`, but a user who runs `git add -f`, or
   who reinitialises git, or whose `.gitignore` is altered, will not be
   protected. Atelier does not enforce a git-hook layer.

### Atelier's actual responsibilities in v1

- Generate the variable indirection automatically when a sensitive attribute
  needs configuration. Wrapper looks like:

  ```hcl
  # providers.tf
  provider "juju" {
    controller_addresses = var.juju_controller_addresses
    username             = var.juju_username
    password             = var.juju_password
    ca_certificate       = var.juju_ca_certificate
  }

  # variables.tf
  variable "juju_password" {
    type      = string
    sensitive = true
  }

  # secrets.auto.tfvars  (gitignored)
  juju_password = "..."
  ```

- Include `secrets.auto.tfvars` in the auto-generated `.gitignore`.
- Mask sensitive fields in the TUI by default; provide a temporary reveal
  toggle.
- Round-trip sensitive values between the TUI and `secrets.auto.tfvars`.

## Alternatives considered

See "Context" above. (β) was rejected for v1 because:

- Atelier would need a per-provider mapping of "which env vars set which
  attributes," which is essentially the bundled-schemas problem ([ADR-0008](0008-provider-schema-discovery.md)
  alternative b) by another name.
- Some providers do not have env-var support for all sensitive attributes.
- The TUI loses the "yes, this is configured" affordance between sessions
  (no value to read back).

(γ) hybrid is the right v2 target — let the user opt-in per field to env-var
mode — but it does not exist in v1.

## Consequences

- **The v1 README explicitly warns** that Atelier's secret handling is
  appropriate for development environments and that production deployments
  should use one of: (a) hand-written `providers.tf` with env-var references,
  (b) an external secret backend the user wires up themselves, (c) wait for
  v2.
- v2 work includes (γ) hybrid per-field choice. The wrapper format need not
  change to accommodate it; the variable indirection pattern is the same,
  only the *source* of the value differs.
- v3 (or whenever) may add explicit integrations with secret backends. That
  is a significantly larger surface (auth, refresh, audit) and requires its
  own ADR.
