# ADR-0038: Flat top-level CLI surface (`atelier add|rm|ls|apply`)

## Status

Accepted — supersedes [ADR-0018](0018-additive-module-command.md).

## Context

ADR-0018 introduced the `atelier module add|rm|list` namespace. The rationale
was grouping and extensibility: a subcommand tree reads as one coherent
lifecycle and leaves room for more `module` verbs.

In practice the surface stabilized, and the namespace costs more than it earns.
`add`, `rm`, and `ls` are the most-used commands, and every one of them pays a
mandatory `module` word that carries no information. `module apply` grew into
the most common first-run command (ADR-0034), and `atelier apply` already
existed as an undocumented alias precisely because the short spelling is what
people type. There is also nothing else to group: `module` has exactly four
verbs, all of which are the whole lifecycle.

## Decision

Flatten the module lifecycle to top-level commands:

```
atelier add <git-url|gallery-name> [flags]
atelier rm <name> [--force]
atelier ls
atelier apply <git-url|gallery-name> [flags]
```

- The `module` namespace is **removed**, with no deprecated alias. A bare
  `atelier module …` is an unknown command; the error names the replacements.
- `list` and `ls` are both accepted for `ls`; `remove` and `rm` for `rm`.
- `atelier apply` is now the **documented canonical** spelling. It was an
  undocumented alias for `module apply` (ADR-0034); making it canonical does
  not change the operation — scaffold a module and hand Terraform the console —
  so ADR-0002's boundary (Atelier does not own the apply lifecycle) still
  holds. What changes is that the name is no longer hidden.

## Alternatives considered

- **Keep the `module` namespace.** Rejected: the grouping no longer buys
  anything, and the extra word is pure friction on the most common commands.
- **Keep `module …` as a deprecated alias.** Rejected: an alias is a second
  surface to document and test forever, for a namespace we are confident is
  wrong. A clear error that names the new spelling is enough.
- **`atelier init` for the first wrapper.** Rejected in ADR-0018 and still:
  `init` implies one-time setup and does not communicate the additive case.
- **A separate `atelier list` for wrappers vs modules.** Rejected: `ls` lists
  modules in the current wrapper; `atelier wrappers` lists wrappers under a
  directory (ADR-0036). The two nouns stay distinct.

## Consequences

- A **breaking change** for any script invoking `atelier module …`; the fix is
  mechanical (`module add` → `add`, and so on).
- ADR-0018 is superseded; its additive-command design is otherwise unchanged.
- The documented surface and `--help` are shorter, and `atelier apply` matches
  what users already typed.
- SPEC §6, the README, the help text, and the unit and integration tests move
  to the flat spellings.
