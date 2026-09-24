# Atelier integration tests

These tests exercise Atelier end-to-end. They are split into two tiers:

- **`wrapper/`** — feature tests against a real upstream module
  (`canonical/prometheus-k8s-operator`). They assert what `atelier module add`
  writes (`--module`, `--ref`, `--preset`, `--as`, `module list`/`rm`, duplicate
  refusal, non-interactive exit) and run `terraform init`/`validate` to prove
  the wrapper is deployable. **No Juju model is needed.**
- **`prometheus/`** — a deployment smoke test: create a model (Jubilant), let
  Atelier author the wrapper from a preset, then `terraform init` + `apply` and
  wait for the application to become active. Marked `cloud`.

They are **not** run by `go test`. Locally:

```shell
# Build the binary under test and point the tests at it.
go build -o /tmp/atelier ./cmd/atelier
export ATELIER_BIN=/tmp/atelier

# Feature tests only — no Juju model required.
uv run --project tests/integration pytest tests/integration -m "not cloud" -vv --capture=no

# Everything, including the deployment, against your current Juju controller.
uv run --project tests/integration pytest tests/integration -vv --capture=no
```

Useful flags and environment variables:

- `--keep-models` (or `KEEP_MODELS=1`) — keep the temporary model for inspection.
- `INTEGRATION_TIMEOUT` — override the settle timeout in seconds.
- `PROMETHEUS_CHANNEL` — charm channel for the deployment test (default `dev/edge`).
- `PROMETHEUS_UNITS` — unit count for the deployment test (default `1`).

## Why prometheus-k8s-operator

It is a **real** module that keeps CI fast and simple: a single Terraform root
under `terraform/`, one application, and no relations or object storage. That
makes it ideal for documenting the CLI surface without the settle time and
external dependencies (S3, many charms) of a full product module.

Candidate-discovery semantics — multiple Terraform roots, directory exclusions,
child-module detection — are covered by Go unit tests in `internal/candidate`
and `internal/bootstrap`, so the integration suite here focuses on the
end-to-end CLI behaviour.

## How the wrapper is tested

`TfDirManager` in [`helpers.py`](helpers.py) does not copy a static `.tf` file.
Instead it *latches onto the wrapper Atelier writes*: the test allocates a
directory, runs `atelier module add … --preset … --yes` in it (with
`stdin=/dev/null`, so Atelier skips the TUI), then runs `terraform init` and
`terraform apply` in that same directory.

See [`.github/workflows/integration.yml`](../../.github/workflows/integration.yml)
for the CI setup. The two tiers run as **two parallel jobs**: `wrapper`
(no Juju/K8s — only Go, Terraform, git and network) and `prometheus`
(Juju/Canonical K8s prepared by Concierge). Because the wrapper job needs no
Concierge, it starts immediately and reports in about a minute, while the
deployment job prepares the controller alongside it.
