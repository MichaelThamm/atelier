# ADR-0005: Implementation language: Go

## Status

Accepted

## Context

Atelier is a terminal UI that reads and writes HCL, invokes `terraform` as a
subprocess and parses its JSON output, and distributes as a single binary
(potentially packaged as a snap). The language choice affects each of these.

Candidates considered: Go, Rust, Python, TypeScript/Node.

The most consequential dimension is **HCL handling**. Atelier must
read-modify-write the wrapper's `main.tf` while preserving user-added
comments and formatting. This requires an AST-preserving HCL parser and
emitter — not just a "parse to JSON, lose everything" parser. Without
AST-preserving round-trip, hand-edits between Atelier sessions get clobbered,
which breaks [ADR-0004](0004-wrapper-layout.md)'s round-tripping
property.

## Decision

**Go.**

Primary justification: [`github.com/hashicorp/hcl/v2`](https://github.com/hashicorp/hcl)
is the official HCL library used by Terraform itself. It provides
AST-preserving read-modify-write, full HCL2 semantics, and is maintained on
the same release cadence as Terraform. There is no equivalent in any other
language.

Supporting justifications:

- [`github.com/hashicorp/terraform-exec`](https://github.com/hashicorp/terraform-exec)
  is the official Go library for invoking `terraform` and parsing its JSON
  output. Used in production by tools like Atlantis. Saves us re-parsing
  text or JSON ourselves.
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) + [`bubbles`](https://github.com/charmbracelet/bubbles)
  + [`lipgloss`](https://github.com/charmbracelet/lipgloss) is the most
  mature TUI ecosystem on any platform. Charm's broader ecosystem (gum, glow,
  soft-serve, vhs) also provides reference design language we can align with.
- Static-binary distribution is trivial: one executable, no runtime
  dependencies. Snap packaging is straightforward (small, single-file).
- Same language ecosystem as Terraform itself. No impedance mismatch in
  data structures, error semantics, or convention.

## Alternatives considered

### Rust

[Ratatui](https://github.com/ratatui-org/ratatui) is excellent. [`hcl-rs`](https://github.com/martinohmann/hcl-rs)
exists but is community-maintained and its AST-preserving round-trip is less
battle-tested. There is no equivalent to `terraform-exec` — we'd shell out
and re-parse output ourselves. Static binary works; snap packaging works.
Slower dev velocity than Go for a tool of this scope; the HCL gap is the
deciding factor.

### Python

[Textual](https://github.com/Textualize/textual) is genuinely good.
[`python-hcl2`](https://pypi.org/project/python-hcl2/) parses HCL but is
**lossy**: it discards comments and formatting on parse. That breaks the
hand-edit round-trip property. Distribution under snap is heavyweight
(embedded interpreter, dependency snapshot).

### TypeScript / Node (Ink)

[Ink](https://github.com/vadimdemedes/ink) works. The HCL story is the same
as Python (`@cdktf/hcl2json` is lossy). Distribution similarly heavy.

## Consequences

- Atelier uses Go modules. Minimum Go version: 1.22 (chosen for
  `slices`/`maps` stdlib packages and generic improvements; revisit if a
  newer version becomes necessary).
- HCL read-modify-write goes through `hcl/v2`'s syntax tree (`hclwrite`
  package). Atelier's writer modifies specific attributes within the
  `module {}` block, preserving everything else verbatim.
- Terraform invocation goes through `terraform-exec`. JSON parsing for plan
  output uses the official `terraform-json` package.
- TUI is built on Bubble Tea; styled with Lip Gloss; common widgets from
  Bubbles where they fit, custom widgets for type-specific editors (object
  sub-forms, map editors, etc.).
- Distribution: GoReleaser produces `linux/amd64` and `linux/arm64` binaries
  and a snap. `go install github.com/MichaelThamm/atelier@latest` works for
  development users.
- Logging and telemetry: Go's `slog` for structured logs (debug mode behind a
  flag). No telemetry in v1.
