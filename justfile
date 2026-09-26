# Atelier developer tasks. Run `just --list` (or `just`) to see recipes.

set shell := ["bash", "-uc"]

# Path to the dev-built binary; gitignored via the repo-root /atelier rule.
atelier_bin := justfile_directory() + "/atelier"

# Shared integration-test invocation.
pytest := "uv run --project tests/integration --frozen pytest"
pytest_flags := "-vv -ra --capture=no --exitfirst"

# Default: run the full local gate — format check, build, vet, race tests.
default: check

# Format check + build + vet + unit tests. CI runs this exact recipe.
check: fmt-check build vet test

# Rewrite files with gofmt.
fmt:
    gofmt -w .

# Fail if any file is not gofmt-formatted.
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    unformatted="$(gofmt -l .)"
    if [ -n "${unformatted}" ]; then
      echo "These files are not gofmt-formatted:"
      echo "${unformatted}"
      exit 1
    fi

# Compile every package.
build:
    go build ./...

# Run static checks.
vet:
    go vet ./...

# Run the full unit suite with the race detector.
test:
    go test -race ./...

# Run unit tests for one package, e.g. `just test-pkg ./internal/tui`.
test-pkg pkg:
    go test -race {{pkg}}

# Documentation drift checks: ADR index, ADR references, relative links.
docs-check:
    go test ./tools/docscheck/...

# Build the dev binary used by the integration tiers. Repo-local (./atelier);
# does not touch $GOBIN or your PATH — use `just install` for that.
build-bin:
    go build -o {{atelier_bin}} ./cmd/atelier

# Install into $GOBIN (default ~/go/bin) so `atelier` on PATH is this build.
# Use this when dogfooding; `build-bin` stays repo-local.
install:
    go install ./cmd/atelier

# Fast integration tier: every test not marked `cloud` (no Juju model needed).
test-integration: build-bin
    ATELIER_BIN={{atelier_bin}} {{pytest}} tests/integration -m "not cloud" {{pytest_flags}}

# Cloud tier: prometheus-k8s deploy smoke test. Needs Juju + Canonical K8s.
test-prometheus: build-bin
    ATELIER_BIN={{atelier_bin}} {{pytest}} tests/integration/prometheus -m cloud {{pytest_flags}}

# Cloud tier: COS-Lite import round-trip. Needs Juju + Canonical K8s.
test-import: build-bin
    ATELIER_BIN={{atelier_bin}} {{pytest}} tests/integration/import -m cloud {{pytest_flags}}

# Both cloud tiers (local convenience; CI runs them as separate jobs).
test-cloud: test-prometheus test-import

# Tidy module dependencies.
tidy:
    go mod tidy

# Remove the dev-built binary.
clean:
    rm -f {{atelier_bin}}
