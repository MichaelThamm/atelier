# Integration tests

End-to-end tests for Atelier against real modules and a real Juju controller.
They are grouped by what they need:

| Directory | Needs | What it proves |
| --- | --- | --- |
| [`wrapper/`](wrapper/) | Terraform | The `atelier module add` CLI surface: `--module`, `--ref`, `--preset`, `--as`, listing/removal, duplicate refusal, and non-interactive exit. No Juju model needed. |
| [`prometheus/`](prometheus/) | Juju + Canonical K8s | Atelier + Terraform deploy `prometheus-k8s-operator` and the model settles active and idle. |
| [`import/`](import/) | Juju + Canonical K8s | Deploy COS-Lite, delete the Terraform state, and `atelier import` rebuilds it with no drift on the resources that have a live counterpart. |

The `prometheus/` and `import/` tiers are marked `cloud` and take tens of
minutes; the `wrapper/` tier is quick.

## Running locally

Prerequisites: [uv](https://docs.astral.sh/uv/), Terraform ≥ 1.14, and — for the
`cloud` tier — a Juju controller on a Kubernetes cloud. Build Atelier and point
the suite at the binary:

```bash
go build -o /tmp/atelier ./cmd/atelier

# Fast tier (no Juju model):
ATELIER_BIN=/tmp/atelier \
  uv run --project tests/integration --frozen pytest tests/integration/wrapper

# Cloud tier:
ATELIER_BIN=/tmp/atelier \
  uv run --project tests/integration --frozen pytest tests/integration/prometheus -m cloud
ATELIER_BIN=/tmp/atelier \
  uv run --project tests/integration --frozen pytest tests/integration/import -m cloud
```

Set `KEEP_MODELS=true` (or pass `--keep-models`) to keep the temporary Juju
models when a test fails, so you can inspect them.

CI runs these in [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) —
after the `build · vet · test` job passes — against Juju + Canonical K8s
prepared by [Concierge](https://github.com/canonical/concierge).
