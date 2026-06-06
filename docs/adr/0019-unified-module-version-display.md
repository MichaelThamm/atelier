# ADR-0019: Unified module version display

## Status

Proposed — amends [ADR-0006](0006-two-pane-ui-layout.md) and
[ADR-0015](0015-multi-module-grouping.md); relates to
[ADR-0014](0014-unified-layout-budget.md) and
[ADR-0017](0017-inter-module-wiring.md).

## Context

A module's git pin (its `?ref=…`) is surfaced in two places, rendered with
three different vocabularies:

1. **Top banner** (`renderHeader` → `moduleBanner`, active module only):
   `Module: traefik ref rev301 (abc1234)` — name, the word `ref`, the ref, and
   the short resolved **SHA** in parentheses. For an unpinned module it
   collapses to `Module: cos (34a6ad6)` — no ref, but still a SHA.
2. **Left-pane section headers** (`renderLeftPane`, multi-module only):
   `── traefik @rev301 ─────────` — name and `@ref`, **no SHA**, padded to the
   pane width with decorative trailing box-drawing dashes.

This produces three inconsistent renderings of the same concept (`@rev301`,
`ref rev301 (sha)`, and a bare `(sha)`), and three concrete problems:

- **The decorative dashes fight the ref for space.** In the 30-column left
  pane the trailing-dash fill and the ref compete; a long ref is truncated to
  `@re…`, discarding the one value that changes when the user presses `R`.
  (This was also the source of a header line-wrap bug: width math used byte
  length, so the multi-byte ellipsis pushed the line one column past the
  border.)
- **The SHA is noise here.** It is a resolved implementation detail of the
  clone, not a user-authored pin. Showing it — especially for an *unpinned*
  module — implies a pin that does not exist.
- **No honest token exists for an unpinned module.** A bare
  `git::…//terraform/cos` URL has no git pin. We cannot synthesize
  `main`/`master`: the default branch is repo-specific and unknown, so
  printing one would be a lie. A charm `risk`/channel (e.g. `risk = "stable"`)
  is a module-specific input variable, not a source pin, and must not be
  conflated with a git ref.

## Decision

### One concept: the version token

A module's **version token** is its literal git ref and nothing else:

- If the module source carries a `?ref=<ref>`, the token is `@<ref>`.
- Otherwise the token is **empty** — render the bare name. Never a SHA, never
  a synthesized default branch, never a `risk`/channel value.

A single helper produces it, used by every surface:

```go
// moduleLabel renders a module's display name with its git-ref pin, if any.
// Unpinned modules render as the bare name (no SHA, no synthesized branch).
func moduleLabel(name, ref string) string {
    if ref == "" {
        return name
    }
    return name + "@" + ref
}
```

### Surfaces

**Left-pane section headers** (`renderLeftPane`):

```
── traefik@rev301
── ingress_configurator@rev81
── cos
```

- Leading `── ` is retained as the section-grouping cue (per ADR-0015).
- The label is `moduleLabel(name, ref)`.
- **No trailing decorative dashes.** The bordered panel already pads each line
  to the pane width, so the divider role is preserved without a dash fill, and
  the decoration-vs-content contention is removed permanently.
- If the label exceeds the content budget, **truncate the name, preserve the
  `@ref` suffix whole** — the ref is the actionable value. The ref is only
  ever truncated in the pathological case where `@ref` alone overflows.

**Top banner** (`moduleBanner`):

```
Module: traefik@rev301
Module: cos
```

- Mirrors the header exactly: `Module: ` + `moduleLabel(name, ref)`.
- The word `ref` and the `(sha)` suffix are **removed**. The banner and the
  left-pane headers now tell the same story.

### Out of scope (unchanged)

- **Ref-switch modal** (`renderRefModal`): a focused action dialog where the
  resolved commit is useful context. It keeps showing `Current: <ref> (sha)`
  and `Source:`. Do not touch.
- **Transient "Switched … to ref: <ref> (<sha>)" toast** (`applyRefSwitch`):
  the SHA is helpful confirmation feedback. Keep it; `shortSHA` therefore
  remains in use.

## Implementation plan

Target package: `internal/tui`. No new dependencies; `ansi.Truncate` and
`lipgloss.Width` are already imported in `view.go`.

1. **Add the helper.** In `internal/tui/model.go` (near `shortSHA`,
   ~line 1342), add `moduleLabel(name, ref string) string` as above.

2. **Banner** — `moduleBanner()` (`internal/tui/model.go`, ~line 1358):
   replace the `parts` assembly with:
   ```go
   func (m *Model) moduleBanner() string {
       name, _, ref, _ := m.activeRefInfo()
       if name == "" {
           return ""
       }
       return "Module: " + moduleLabel(name, ref)
   }
   ```
   (Drops the `ref %s` and `(%s)` SHA parts. The `sha` return is now unused
   here — keep `activeRefInfo`'s signature; it is still used by the modal.)

3. **Section header** — `renderLeftPane()` (`internal/tui/view.go`, the
   `if r.IsHeader {` block, ~lines 88–106): replace the name/`@ref`/dash logic
   with:
   ```go
   name := r.VarName
   ref := ""
   if r.ModuleIdx < len(m.Modules) {
       ref = m.Modules[r.ModuleIdx].Ref
   }
   const headerPrefix = 3 // "── "
   budget := maxVisualWidth - headerPrefix
   var label string
   if ref != "" {
       suffix := "@" + ref
       nameBudget := budget - lipgloss.Width(suffix)
       if nameBudget < 1 {
           // Pathological: ref alone overflows; truncate the whole label.
           label = ansi.Truncate(name+suffix, budget, "…")
       } else {
           label = ansi.Truncate(name, nameBudget, "…") + suffix
       }
   } else {
       label = ansi.Truncate(name, budget, "…")
   }
   line := styleSectionHeader.Render("── " + label)
   fmt.Fprintln(&b, line)
   continue
   ```
   Remove the now-dead `pad`/`strings.Repeat("─", …)` code from this block.

4. **Tests** (`internal/tui/`):
   - Update `TestMultiModule_renderLeftPane_showsHeaders` if it asserts on the
     old `@ref`-with-dashes form (the `"── mimir"` / `"── seaweedfs"`
     substring checks still hold).
   - Keep `TestMultiModule_renderLeftPane_headerNeverOverflows` (the width-≤30
     guard) — it must still pass under name-truncation.
   - Add `TestModuleLabel`: `("traefik","rev301") → "traefik@rev301"`;
     `("cos","") → "cos"`.
   - Add a banner test asserting `moduleBanner()` returns `Module: traefik@rev301`
     for a pinned active module and `Module: cos` for an unpinned one, with no
     `(` SHA fragment.
   - Add a header test asserting that a long ref is preserved while the name is
     truncated (e.g. `ingress_configurator@rev123456789` keeps the full
     `@rev123456789`).

5. **Verify** `shortSHA` is still referenced (modal + toast) and left intact.

6. Run `go build ./... && go vet ./... && go test ./...`, then rebuild/install
   (`go build -o atelier ./cmd/atelier && go install ./cmd/atelier`).

## Alternatives considered

- **Show the resolved SHA as the version token.** Rejected: the SHA is a clone
  detail, not a user-authored pin; showing it (especially for unpinned
  modules) implies a pin that does not exist.
- **Synthesize `main`/`master` for unpinned modules.** Rejected: the default
  branch is repo-specific and unknown to Atelier; printing one is incorrect.
- **Surface `risk`/channel in the version slot for channel-sourced modules**
  (e.g. `cos · stable`). Rejected: `risk` is a module input variable, not a
  source pin; conflating the two muddies the slot's meaning.
- **Keep the full-width trailing-dash divider with width-aware math.** Viable
  (and would also fix the wrap bug), but retains the decoration-vs-ref tension
  in a 30-column pane and a second, divergent layout idiom. Dropping the fill
  is simpler and lets `lipgloss` own line padding.
- **Two vocabularies** (compact `@ref` in headers, verbose `ref … (sha)` in the
  banner). Rejected: the inconsistency is exactly the reported problem.

## Consequences

- Banner and section headers share one format and one helper; changing the
  token format later is a single-function edit.
- Unpinned modules render as a bare name in both surfaces. This is an
  intentional, honest asymmetry (pinned vs unpinned), not a missing-data bug.
  Users distinguish "no pin" from "load failure" by the absence of any
  surfaced error, not by a placeholder.
- The left-pane header no longer spans the pane with dashes; the leading `── `
  plus `lipgloss` padding carries the grouping cue. This is a deliberate visual
  change to ADR-0015's header rendering.
- The SHA remains available where it is genuinely useful (ref-switch modal and
  the post-switch confirmation toast), so no information is lost from the
  workflow.
