# ADR-0034: `atelier module apply` one-liner

## Status

Accepted

## Context

Atelier's pitch is "configure a module visually, then iterate against plan."
That loop assumes the user already has a wrapper directory. The first step is
still manual and shell-shaped:

```
mkdir cos-lite && cd cos-lite
atelier module add https://github.com/canonical/observability-stack.git \
  --module terraform/cos-lite
terraform init
terraform apply
```

`module add` opens the TUI (or writes the wrapper and exits under a
non-interactive stdin), and the user then leaves Atelier to run `terraform
init && terraform apply` themselves. For the common case — "I just want to
apply this module" — the mkdir, the cd, the init, and the apply are ceremony
around a single intent.

There is also a hard boundary to respect. [ADR-0002](0002-author-and-plan-scope.md)
decided that Atelier implements author + plan + apply *inside the TUI*, with
plan manual (`P`) and apply (`A`) using a cached, reviewed plan file, and
[SPEC §6](../SPEC.md) states there is deliberately **no `atelier apply`**
command. Atelier is not a deployment platform and must not become one
([ADR-0016](0016-scope-boundaries-no-orchestration.md), whose decision is
unchanged: no rollout orchestration, no multi-environment fan-out, no remote
state).

## Decision

Add `atelier module apply <git-url>`, a convenience command that performs the
scaffold-then-apply sequence as one invocation:

1. Clone the module and discover candidates, reusing `module add`'s flow and
   flags (`--module`, `--ref`, `--var-file`, `--var`, `--as`).
2. Create a target directory named after the module candidate (`--as`/`--dir`
   override), staging the clone next to it so the final placement is a rename.
3. Write the wrapper.
4. Run `terraform init`, then `terraform apply` attached to the terminal.

**The apply is Terraform's interactive apply when a terminal is present, and an
auto-approved apply when it is not.** At a terminal the user confirms the plan
at Terraform's own `Do you want to perform these actions?` prompt; there is no
`--yes` flag for this command. When stdin is not a terminal — a pipe, CI, or the
conventional `atelier module apply … < /dev/null` — there is nothing to answer
the prompt, so the apply runs as `terraform apply -auto-approve -input=false`.
The scaffolding is non-interactive in both cases; the redirect only selects the
apply mode.

This is deliberately a *scaffolding* command, not the `atelier apply` of
ADR-0002's boundary. It operates only on a fresh, single module in a new
directory. It does not detect drift, does not re-apply an existing wrapper,
does not manage multiple environments, and keeps no plan cache. Applying an
existing wrapper remains `terraform apply` in that directory or `A` in the TUI
plan view.

## Alternatives considered

### Auto-approve unconditionally (`terraform apply -auto-approve`)

Truly one command with no branch, and the literal reading of "just apply".
Rejected because it removes the only review point when a terminal *is* present,
contradicting ADR-0002's "apply exactly what you reviewed". Auto-approval is
reserved for the case where there is no terminal to review at, where the choice
is between that and refusing to run.

### A top-level `atelier apply <url>`

Simpler to type, but it collides with the boundary language in ADR-0002 and
SPEC §6, which promise no `atelier apply`. Nesting it under the existing
`module` subcommand keeps the surface honest: this is about getting a module's
wrapper scaffolded and its first apply kicked off, not about owning the apply
lifecycle.

### `module add --apply`

Fewer names, but it overloads `add` with a second, very different outcome (an
interactive apply) behind a flag, and `add` has no directory argument to
scaffold into. A distinct subcommand says what it does.

### Emit a shell script or `Makefile` snippet

Pushes the sequence back to the user and adds an artifact to maintain. The
mkdir/cd is one line of code here versus a new file shape everywhere.

## Consequences

- The first-deployment path is one command; the wrapper it produces is an
  ordinary Atelier wrapper, so nothing downstream changes.
- The directory-naming rule is shared with the module block label
  (`bootstrap.ModuleBlockName` / `ModuleDirName`), so a module is named
  consistently whether you `add` or `apply` it.
- The interactive apply needed a path that bypasses `hashicorp/terraform-exec`,
  which always passes `-auto-approve -input=false`;
  `tfexec.Terraform.ApplyDirect` runs the binary directly with the terminal
  attached, and switches to an auto-approved, stdin-detached run when stdin is
  not a terminal. A unit test pins the argv for both modes.
- The scope boundary of ADR-0002 is tightened rather than broken: SPEC §6 now
  distinguishes `module apply` (scaffold a fresh module and let Terraform ask)
  from the still-absent `atelier apply` (re-apply an existing wrapper).
- A required variable with no value stops before apply, so the failure is a
  clear "pass `--var NAME=VALUE`" message rather than a Terraform argument
  error.
- The command still never needs `< /dev/null` to avoid a TUI (it never opens
  one); the redirect only selects auto-approval, so `atelier module apply … <
  /dev/null` runs unattended in scripts and CI.
