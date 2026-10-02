# ADR-0039: Composed gallery presets and a required-input coverage lint

## Status

Accepted — supersedes [ADR-0035](0035-bundled-module-gallery.md). The embedded
manifest, CLI-as-human-interface, and `requires`-is-metadata decisions stand;
what changes is the preset model and how coverage is enforced.

## Context

ADR-0035 let a gallery entry carry **at most one** preset, and framed its job as
supplying the module's required inputs so the one-liner would work. Coupling the
preset to required inputs produced three problems.

1. **One preset was too few.** A product like COS has orthogonal bundles
   (`single-unit`, `no-ingress`, `s3-seaweedfs`) that compose into one scenario.
   Forcing one per entry meant the gallery could expose only a slice of them.
2. **Required-input filler had to fake a value.** The only entry with a preset,
   `haproxy-product`, needed `protected_hostnames_configuration` — a required
   `list(object)`. The preset seeded `hostname = "example.com"`: a placeholder
   that looks configured, which is exactly what ADR-0007's sparse rule exists to
   avoid.
3. **Nothing verified coverage.** ADR-0035 assumed `terraform validate` would
   fail when a module call omitted a required argument. It does not. So
   `gallery-check` passed an entry that could never plan, and drift was invisible
   until a user ran it.

The `requires` list was already the right home for deployment-specific inputs;
it just had no enforcement behind it.

## Decision

### An entry composes presets

`presets` is a list. An entry applies **all** of them, in order, as its curated
default scenario; later values win, and a user's own `--var-file` is layered
after them so it still overrides. Presets are scenario bundles, not
required-input fillers.

Generic upstream presets move into Atelier rather than being duplicated by every
product repo: `cos-single-unit` and `cos-no-ingress` are Atelier-hosted copies of
`terraform/cos/presets/*` in `canonical/observability-stack`.

### `requires` may carry a value

A `requires` entry is either a bare `name` — rendered as `--var name=<name>`, a
placeholder the user fills in — or `name=value`, which renders a **working
default** and is the value `gallery-check` passes.

The value form exists because no generic placeholder satisfies a variable's own
`validation` rule. `charmarr`'s `storage_backend` must be one of
`storage-class`, `native-nfs`, or `hostpath`; the check's `"placeholder"` (or a
type-aware `""`) fails validation. `haproxy-product`'s list is passed as `[]`.
Atelier does not invent these values: the entry states what satisfies the input.

### `atelier gallery lint` enforces coverage

`atelier gallery lint` clones each entry's pinned module, reads its schema, and
asserts

```
required(module) ⊆ keys(entry presets) ∪ requires names
```

reporting required inputs nobody supplies and `requires` entries the module no
longer declares, exiting non-zero on either. It is the drift guard: a module
that gains a required input fails here, with a name, rather than at apply time.

`just gallery-check` keeps its job — scaffold each entry and run
`terraform init -backend=false && terraform validate` — and now supplies the
`name=value` entries verbatim.

## Alternatives considered

- **More entries instead of composed presets** (one per scenario). Rejected:
  the entry count is combinatorial, and the bundles are independent, not
  alternative.
- **Keep a preset per entry that supplies required inputs.** Rejected: this is
  the status quo; it reintroduces fake values that look configured.
- **Rely on `terraform validate` to enforce coverage.** Rejected: verified not
  to fail on a module call missing a required argument.
- **Generate the check's values from the declared type in Go.** Rejected as the
  whole answer: a type-aware zero value still fails a `validation` rule. It is
  useful only for inputs that no rule constrains, and the entry already has to
  state a value for those that are.
- **Derive `requires` automatically from the schema.** Rejected: Atelier would
  be guessing which inputs are deployment-specific and which a preset can hold.

## Consequences

- The check stops passing entries that cannot apply; `gallery lint` names the
  offending input.
- `haproxy-product` loses its fake `example.com` and asks for real hostnames.
- `charmarr` and `charmarr-plus` join the gallery.
- Two COS presets are now hosted by Atelier. The product repo may keep its
  copies; Atelier's are independent, and a local or repo bundle of the same name
  still takes precedence.