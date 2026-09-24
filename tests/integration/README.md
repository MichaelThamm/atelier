# Atelier integration tests

These tests exercise Atelier end-to-end against a live Juju controller and
Terraform: Atelier bootstraps a wrapper from a real upstream module, applies a
preset non-interactively, and Terraform deploys it.

They are **not** run by `go test`. Locally:

```shell
# Build the binary under test and point the tests at it.
go build -o /tmp/atelier ./cmd/atelier
export ATELIER_BIN=/tmp/atelier

# S3 coordinates for the loki workload (microceph RGW in CI).
export S3_ENDPOINT=http://10.0.0.1:8080
export S3_ACCESS_KEY=access-key
export S3_SECRET_KEY=secret-key

# Run against your current Juju controller.
uv run --project tests/integration pytest tests/integration/loki -m cloud -vv --capture=no
```

Useful flags and environment variables:

- `--keep-models` (or `KEEP_MODELS=1`) — keep the temporary model for inspection.
- `INTEGRATION_TIMEOUT` — override the settle timeout in seconds.

## How the wrapper is tested

`TfDirManager` in [`helpers.py`](helpers.py) does not copy a static `.tf` file.
Instead it *latches onto the wrapper Atelier writes*: the test allocates a
directory, runs `atelier module add … --preset … --yes` in it (with
`stdin=/dev/null`, so Atelier skips the TUI), then runs `terraform init` and
`terraform apply` in that same directory.

See [`.github/workflows/_integration.yml`](../../.github/workflows/_integration.yml)
for the CI setup (Juju/MicroK8s via Concierge, microceph for S3).
