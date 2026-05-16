# Atelier

![Atelier](docs/images/atelier.png)

A terminal UI for configuring Terraform modules.

Atelier treats a module's variables as its API surface. The wrapper it
generates captures only the values the deployer chose to set, so `main.tf`
reads as a concise statement of intent rather than a wall of options.
Defaults handle the rest, and plan diffs show exactly what changes between
versions — making large modules approachable for first-time and experienced
Terraform users alike.

## Quick start

```bash
atelier init https://github.com/canonical/observability-stack.git
atelier        # re-open an existing wrapper
```

## Keyboard shortcuts

| Key | Context | Action |
|-----|---------|--------|
| `Tab` | Anywhere | Switch between left (variable list) and right (editor) pane |
| `↑` / `↓` | Left pane | Navigate variables |
| `Enter` | Left pane | Focus the editor for the selected variable |
| `P` | Left pane | Run `terraform plan` against the wrapper |
| `R` | Left pane | Switch the module ref (branch, tag, or SHA) |
| `E` | Left pane | Show full error detail (when an error is present) |
| `F` | Left pane | Open the preset picker (when presets are available) |
| `^R` | Anywhere | Reset the current variable to its default |
| `Q` | Left pane | Quit and save |

## Presets

Module maintainers can declare **presets** in `atelier.yaml` — named bundles
of variable values that users apply in one action, then customise as needed.

```yaml
modules:
  - path: terraform/cos-lite
    name: "COS Lite"
    presets:
      - name: production
        description: "Stable channel, TLS, HA replicas."
        sets:
          risk: "stable"
          internal_tls: true
          alertmanager:
            units: 3
```

When presets are declared, `[F] preset` appears in the status bar. Press `F`
to open the picker, navigate with `↑`/`↓`, apply with `Enter`, or cancel
with `Esc`.

See [docs/examples/cos-lite.atelier.yaml](docs/examples/cos-lite.atelier.yaml)
for a full example.

## Comparing versions

Press `R` to switch the module ref without leaving the TUI. Atelier
re-clones the module, carries your values forward, runs
`terraform init -upgrade`, and flags any orphaned or newly required
variables.

1. Configure and plan at `v1.0`.
2. Press `R`, type `v2.0`, confirm.
3. Plan again — the diff shows what the version bump changes.

## Documentation

| Document | Description |
| --- | --- |
| [docs/SPEC.md](docs/SPEC.md) | v1 specification |
| [docs/ROADMAP.md](docs/ROADMAP.md) | v1 scope and deferred items |
| [docs/adr/](docs/adr/) | Architecture Decision Records |
| [docs/examples/](docs/examples/) | Sample manifests |