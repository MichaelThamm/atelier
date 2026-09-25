<!--
PR titles follow conventional commits: feat:, fix:, docs:, refactor:, chore:, test:.
Keep one logical change per PR. The repo contract is in ../AGENTS.md.
-->

## Summary

<!-- What changed and why. Link the issue ("Closes #123") if there is one. -->

## Decision and spec

- **ADR:** <!-- Link the new/updated ADR, or "none needed" with a one-line reason.
  A new decision needs a new ADR. Accepted ADRs are immutable — supersede, don't edit. -->
- **SPEC:** <!-- Section(s) touched in docs/SPEC.md, or "none". -->

## Scope check

- [ ] Fits Atelier's boundaries — configuring a module's variables, not orchestration ([ADR-0016](../docs/adr/0016-scope-boundaries-no-orchestration.md)).
- [ ] No new configuration language and no web UI ([ROADMAP](../docs/ROADMAP.md), "Out of scope").
- [ ] The wrapper stays independently runnable without Atelier installed.
- [ ] Atelier files are never read from the upstream module repo.

## Tests

- [ ] `just check` passes locally.
- [ ] New or changed behavior has a test that fails without the change.
- [ ] Integration tiers affected: <!-- none / wrapper / prometheus / import -->

## Docs

- [ ] `docs/SPEC.md` updated for user-visible surface changes (or N/A).
- [ ] `?` help modal updated if keybindings changed, and README/SPEC updated for
  command or surface changes (or N/A).
- [ ] A row was added to the [ADR index](../docs/adr/README.md) for any new ADR.

## Risk and rollback

<!-- What could break, and how to reverse it. Call out state, secrets, or a
change to the on-disk wrapper format. -->

## Reviewer notes

<!-- Where you were unsure, or what you'd like the reviewer to focus on.
For agent-authored changes, point at the parts most worth verifying against the code. -->
