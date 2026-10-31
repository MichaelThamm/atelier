# ADR-0065: Shallow git module clones by default

## Status

Accepted.

## Context

Atelier writes a git module's `source` into the wrapper with no shallow-clone
`depth`, so `terraform init` fetches the module's **full** history. That is
inconsistent with everything around it:

- Atelier's own schema-reading clone is already shallow (`git clone --depth 1`).
- Upstream modules routinely declare `?depth=1` on their own nested sources, so
  Terraform fetches those shallow while the one clone Atelier authors is full.

The cost is real for repositories with deep history. Measured on the command
line, a full clone of `canonical/github-runner-operator` takes minutes and pulls
~62 MiB where a shallow clone is under a second and ~0.4 MiB; the gallery's
`haproxy-product`, `loki-operators`, and `charmarr` roots are in the same class
(tens of MiB of history). Small repos (`observability-stack`, ~5.7 MiB) gain
little, so the benefit is concentrated in the fatter roots — which is exactly
where it is worth having.

One constraint shapes the whole design. Terraform accepts the `depth` query
parameter **only with a branch or tag ref**; a commit SHA is rejected outright:

```
error downloading '…?depth=1&ref=<sha>':
  fatal: Remote branch <sha> not found in upstream origin
   (note that setting 'depth' requires 'ref' to be a branch or tag name)
```

Every bundled gallery entry pins a commit SHA, so an unconditional default would
break the most common flow (`atelier apply cos-lite`).

## Decision

**A git module source defaults to a shallow clone, written into the wrapper as
`?depth=1`, wherever Terraform permits it.**

- A branch, tag, or empty ref gets `depth=1` by default. A commit-SHA ref gets
  no `depth`, because Terraform rejects it — the effective depth is forced to 0
  whenever the ref is a full SHA (`modulesource.EffectiveDepth`).
- `--depth N` sets an explicit depth; `--depth 0` and `--no-depth` force a full
  clone. An explicit `--depth` on a SHA ref prints a note and is ignored.
- A local `source = "./…"` path is never given a depth: it has no remote to
  shallow.

`internal/modulesource` becomes query-parameter aware so the depth round-trips:
`Decompose` stops the ref at the query separator and returns the remote without
any query, `ModulePath` cuts at the first `?`, and `Compose` writes `ref` then
`depth`. `Depth`/`DepthOf` read it back. Module identity compares the ref
(unchanged by depth), so `?ref=main` and `?ref=main&depth=1` are the same module
for convergence purposes (ADR-0050).

`LoadExisting` and the degraded load path take the primary block's `source`
verbatim from `main.tf`, so a `depth` Atelier wrote — or a hand edit — survives
a later save. A TUI ref switch carries the current block's depth forward.

The default lives at the CLI boundary: `add`, `apply`, and `import` pass
`Depth: 1` unless a flag overrides it. `bootstrap` treats a zero depth as "no
depth", so library callers that do not opt in (rehydration, secondary loads)
are unaffected.

## Alternatives considered

- **Blanket `depth=1`, including SHA refs.** Breaks every gallery pin and every
  `--ref <sha>`, because Terraform will not accept it. Rejected.
- **Flag-only, no default.** Leaves the speedup opt-in, so the common branch
  flow does not get it without the user knowing the flag exists. The whole point
  is that the fast case is the safe case. Rejected.
- **Overload `--ref` with query syntax** (`--ref 'main&depth=1'`). The composed
  string is valid Terraform, but Atelier resolves and clones the ref first:
  `gitops.ResolveRef` looks for a branch literally named `main&depth=1` and
  fails. The `&` would also have to be shell-quoted. Rejected.
- **Reconstruct depth from `.atelier/session.json`.** The session does not track
  it, and reading the source verbatim from `main.tf` is simpler and also
  preserves hand edits. Rejected.
- **Resolve a pinned SHA to a tag when one exists**, to recover the gallery
  flow's speedup. Useful, but a separate mechanism with its own network cost and
  failure modes; left as a follow-up rather than folded in here.

## Consequences

- `terraform init` on a branch- or HEAD-based git source fetches only the tip.
  On a fat root this turns a multi-minute clone into about a second.
- Gallery pins and `--ref <sha>` sources are byte-identical to before — they
  already could not take a depth — so the default flow is unchanged.
- A wrapper whose source has no `depth` and a branch ref gains `?depth=1` the
  next time `add`/`apply`/`import` composes it, which also reads as a changed
  declaration and triggers `init -upgrade` once (ADR-0062).
- `--depth 0` / `--no-depth` is the escape hatch if a repository ever needs full
  history (for example a submodule that does not resolve against a shallow
  superproject).
