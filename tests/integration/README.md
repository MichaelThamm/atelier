# Integration tests

End-to-end tests for Atelier against real modules and a real Juju controller.
They are grouped by what they need:

| Directory | Needs | What it proves |
| --- | --- | --- |
| [`wrapper/`](wrapper/) | Terraform | The `atelier module add` CLI surface: `--module`, `--ref`, `--var-file`, `--as`, listing/removal, duplicate refusal, and non-interactive exit. No Juju model needed. |
| [`prometheus/`](prometheus/) | Juju + Canonical K8s | Atelier + Terraform deploy `prometheus-k8s-operator` and the model settles active and idle. |
| [`import/`](import/) | Juju + Canonical K8s | Deploy COS-Lite, delete the Terraform state, and `atelier import` rebuilds it with no drift on the resources that have a live counterpart. |

The `prometheus/` and `import/` tiers are marked `cloud` and take tens of
minutes; everything else is the fast tier and runs in a minute or two.

## Running locally

Prerequisites: [uv](https://docs.astral.sh/uv/), Terraform ≥ 1.14, and — for the
`cloud` tier — a Juju controller on a Kubernetes cloud. The
[`justfile`](../../justfile) wraps both tiers:

```bash
just test-integration   # fast tier — no Juju model
just test-cloud         # cloud tier — needs Juju + Canonical K8s
```

Without `just`, build Atelier and point the suite at the binary:

```bash
go build -o /tmp/atelier ./cmd/atelier

# Fast tier (no Juju model):
ATELIER_BIN=/tmp/atelier \
  uv run --project tests/integration --frozen pytest tests/integration

# Cloud tier (opt in explicitly):
ATELIER_BIN=/tmp/atelier \
  uv run --project tests/integration --frozen pytest tests/integration -m cloud
```

`cloud` tests are excluded by default: [`pyproject.toml`](pyproject.toml) sets
`-m "not cloud"` in `addopts`, so an unscoped `pytest` run never creates a Juju
model by accident. Passing `-m cloud` overrides it.

Set `KEEP_MODELS=true` (or pass `--keep-models`) to keep the temporary Juju
models when a test fails, so you can inspect them.

CI runs these in [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) —
after the `build · vet · test` job passes — against Juju + Canonical K8s
prepared by [Concierge](https://github.com/canonical/concierge). Pull requests
that change only documentation skip the integration tiers; pushes to `main` and
manual runs always execute them.
