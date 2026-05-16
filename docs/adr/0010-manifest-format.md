# ADR-0010: Manifest format: `atelier.yaml`

## Status

Accepted

## Context

Atelier needs *some* maintainer-curation surface — a way for the author of a
module repository to give Atelier hints that make the UX better than what
pure heuristics can achieve. Specifically:

- Friendly names and descriptions for module candidates (better than the raw
  directory path).
- Variable grouping in the TUI's left pane (better than flat declaration
  order).

The two-pane layout ([ADR-0006](0006-two-pane-ui-layout.md)) supports
grouping, and the candidate-discovery design ([ADR-0003](0003-gitops-loading.md))
supports manifest override of the heuristic discovery. Both want a file
format.

The risk: a manifest format can grow without bound. There were ideas during
the initial grilling about declaring "features" (named presets, scenario
toggles, test-derived configurations) in the manifest. Those were
deliberately parked (see [ROADMAP](../ROADMAP.md)) because they were
under-specified. The v1 manifest should *not* include anything we don't
fully understand.

## Decision

The manifest file is **`atelier.yaml`** at the root of the module
repository. The v1 schema is intentionally minimal:

```yaml
modules:
  - path: terraform/cos-lite
    name: "COS Lite"
    description: |
      Production-ready Charmed Observability Stack.
    groups:
      - name: "TLS"
        variables: [internal_tls, external_certificates_offer_url, external_ca_cert_offer_url]
      - name: "Ingress"
        variables: [ingress]
      - name: "Applications"
        variables: [alertmanager, catalogue, grafana, loki, prometheus, ssc, traefik]

  - path: terraform/cos
    name: "COS"
    description: "Standalone COS deployment."
```

### Field semantics

- `path` (string, required) — path to a module candidate, relative to the
  repository root.
- `name` (string, required) — display name in the candidate picker.
- `description` (string, optional) — multi-line description. Falls back to
  the candidate's `README.md` first paragraph, then to the path.
- `groups` (list, optional) — ordered list of variable groups.
  - `name` (string, required) — group display name.
  - `variables` (list of strings, required) — variable names belonging to
    this group, in display order.

Variables not listed in any group appear in an implicit trailing `Other`
group. If `groups:` is absent entirely, all variables appear flat in
declaration order.

### v1 schema is `modules:` only — no top-level configuration

No `version:`, no `atelier_version:`, no top-level options. v1 keeps the
schema flat to leave room for forward-compatible growth.

## Alternatives considered

### `.atelier.yaml` (hidden file)

Considered. Rejected because the manifest is *content authored for users to
discover*, like `package.json`, `goreleaser.yaml`, `pyproject.toml`. It's
not a hidden tool-config dotfile like `.gitignore` or `.editorconfig`. A
visible filename invites maintainer attention.

### Inline annotations in `variables.tf`

Considered. Pattern: a special comment like `## atelier:group=TLS` on each
variable.

Rejected because:

- HCL comments are not first-class metadata. Parsing them is fragile.
- Cross-cutting decisions (e.g., listing candidates with paths and
  descriptions) don't fit per-variable annotations.
- A separate manifest file is a more honest place for maintainer-curation
  decisions.

### Auto-grouping by file

If a module splits variables across `variables-tls.tf`, `variables-ingress.tf`,
etc., we could infer groups from file names. Useless for modules like COS
Lite that keep everything in one file. Conflicts with maintainer freedom.
Rejected.

### Comment-marker parsing in `variables.tf`

Detect a standardized comment pattern (`## section: TLS`) and treat it as a
group divider for variables that follow. Considered for v2 as a
zero-friction alternative to the manifest, but the standard doesn't exist
yet and we shouldn't invent one without observing what maintainers actually
write.

### "Features" / presets in the manifest

Deliberately parked. The concept was under-specified during initial design
(the user described both "auto-discovered from tests" and "manifest-declared
decorators" without converging on which). v1 ships without features; the
v1 manifest schema does not include them. Adding them later does not break
v1 wrappers.

See [ROADMAP](../ROADMAP.md) for the parked-features discussion.

### Variable annotations (friendly labels, value hints)

Considered for v1; deferred to v2. The current value-add of annotating
variables (overriding the raw variable name with a prettier label, providing
example values) is real but small relative to the schema-evolution risk of
locking in too much.

## Consequences

- Maintainers who care about UX add an `atelier.yaml` to their repository.
  Maintainers who don't are still supported by the heuristic candidate
  discovery and flat variable listing.
- The Atelier binary embeds a JSON Schema for `atelier.yaml` and validates
  the manifest on load, with clear error messages for malformed manifests.
- Versioning the manifest schema: v1 has no explicit version field. If/when
  v2 introduces a breaking change, we add a `version: 2` top-level key and
  treat missing version as v1.
- Variables declared in the manifest's `groups[].variables:` but not
  actually present in the module produce a warning at load time, not an
  error (the manifest may be slightly out of sync with a moving ref).
- Variables present in the module but not listed in any group appear in an
  implicit trailing `Other` group with no maintainer effort required.
