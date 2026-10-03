# ADR-0037: Generated GitHub Pages gallery

## Status

Accepted — amended by
[ADR-0046](0046-gallery-card-states-inputs-once.md), which drops a card's
restatement of the required inputs its own command already carries.

## Context

[ADR-0035](0035-bundled-module-gallery.md) makes the module gallery
machine-first: an embedded JSON manifest
([`gallery.json`](../../internal/gallery/gallery.json)) whose deploy command is
**derived** from the entry fields, never stored, so it cannot drift. The human
interface is the CLI and the TUI, and committed per-example Markdown was
rejected for exactly the drift it invites.

That leaves the gallery without a browsable, linkable home. A user who has not
installed Atelier cannot see which modules are onboarded, and the README can
only describe the gallery in prose. A static project site — a landing page and
the gallery — makes the onboarding concrete and gives each entry a URL.

The [ROADMAP](../ROADMAP.md) lists "a web UI" as out of scope. This site is
read-only documentation: it configures nothing, runs no Terraform, and holds no
state. It is not an operational interface and does not cross that boundary.

## Decision

Publish a static site to GitHub Pages, built with MkDocs Material and deployed
by a GitHub Actions workflow.

- The gallery page is **generated** from the manifest by
  [`tools/gallerysite`](../../tools/gallerysite/main.go), which reuses
  `gallery.Entry` (`ApplyCommand`, `ShortRef`), so the deploy commands and pinned
  refs on the page are the same derivation `atelier gallery list` prints. The
  page is not committed; the build regenerates it, so it cannot drift from the
  manifest.
- Each entry renders as a Material "grid card": the description, the module
  link, the pinned ref, the preset and required inputs when present, and the
  `atelier apply <name>` one-liner.
- The site source lives in `website/`; the built output (`website/site/`) and the
  generated page (`website/docs/gallery.md`) are gitignored.
- `just site-build` and `just site-serve` are the local entry points; the Pages
  workflow runs `just site-build`.
- A change under `website/` is documentation-only, so it joins the docs set in
  CI's change filter and does not trigger the integration or gallery jobs.

## Alternatives considered

- **Commit the generated page.** Rejected: the drift ADR-0035 rejects, and it
  would need a regeneration step anyway.
- **Re-derive the command in the site's templating layer.** Rejected: a second
  derivation that can diverge from the CLI's, defeating the single-derivation
  rule.
- **A hand-written HTML page.** Rejected: the gallery would be transcribed by
  hand and would rot on the next entry or ref bump.
- **Fetch the manifest from GitHub at page load.** Rejected: it ties the page to
  a network and to a ref rather than to the built site.
- **Jekyll, Hugo, or Docsify.** Rejected: MkDocs Material renders the same
  Markdown with first-class search and card layouts, and is a build-only
  dependency confined to the Pages workflow.

## Consequences

- The site is a new build surface: a pinned `mkdocs-material` dependency and a
  `website/` tree. It is isolated to the Pages workflow; `just check` stays Go
  only.
- The page cannot drift from the manifest, but the generator can still misrender
  an entry; `tools/gallerysite` carries a colocated test.
- Adding a gallery entry updates the site on the next push to `main`; no
  separate documentation edit is needed.
- `ShortRef` moves from the CLI adapter to `internal/gallery` so the CLI and the
  generator share one definition.
