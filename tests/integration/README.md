# Atelier integration tests

These tests exercise Atelier end-to-end against a live Juju controller and
Terraform: Atelier bootstraps a wrapper from a real upstream module, applies a
preset non-interactively, and Terraform deploys it.

They are **not** run by `go test`. Locally:

```shell
# Build the binary under test and point the tests at it.
go build -o /tmp/atelier ./cmd/atelier
export ATELIER_BIN=/tmp/atelier

# Run against your current Juju controller. The tests create their own model
# and deploy seaweedfs-k8s into it for S3, so no S3 configuration is needed.
uv run --project tests/integration pytest tests/integration/loki -m cloud -vv --capture=no
```

Useful flags and environment variables:

- `--keep-models` (or `KEEP_MODELS=1`) — keep the temporary model for inspection.
- `INTEGRATION_TIMEOUT` — override the settle timeout in seconds.
- `SEAWEEDFS_APP` — application name to deploy/reuse for seaweedfs-k8s
  (default `swfs`). The backend is found by charm name, so an existing app
  under any label is reused.
- `SEAWEEDFS_CHANNEL` — charm channel (default `latest/edge`).
- `SEAWEEDFS_S3_PORT` — S3 port (default `8333`).

## S3 test backend

The loki workload needs an S3 endpoint. The `s3_endpoint` fixture deploys
`seaweedfs-k8s` into the test model and derives the endpoint from the model
status — the equivalent of:

```shell
juju status --format=yaml | yq -r '"http://" + .applications.<app>.units."<app>/0".address + ":8333"'
```

but resolved through Jubilant, so the application label and model name do not
matter. seaweedfs-k8s runs without auth here, so the preset uses placeholder
credentials.

## How the wrapper is tested

`TfDirManager` in [`helpers.py`](helpers.py) does not copy a static `.tf` file.
Instead it *latches onto the wrapper Atelier writes*: the test allocates a
directory, runs `atelier module add … --preset … --yes` in it (with
`stdin=/dev/null`, so Atelier skips the TUI), then runs `terraform init` and
`terraform apply` in that same directory.

See [`.github/workflows/integration.yml`](../../.github/workflows/integration.yml)
for the CI setup (Juju/Canonical K8s via Concierge; S3 from the seaweedfs-k8s
application the tests deploy).
