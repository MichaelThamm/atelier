# Atelier

![Atelier](docs/images/atelier.png)

A terminal UI for configuring Terraform modules.

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

## Documentation

| Document | Description |
|---|---|
| [docs/SPEC.md](docs/SPEC.md) | v1 specification |
| [docs/ROADMAP.md](docs/ROADMAP.md) | v1 scope and deferred items |
| [docs/adr/](docs/adr/) | Architecture Decision Records |
| [docs/examples/](docs/examples/) | Sample manifests |