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

# Structural checks: no dead internal packages, no unreachable functions.
code-check:
    go test -count=1 ./tools/codecheck/... ./tools/deadcodecheck/...

# Build the dev binary
build-bin:
    go build -o {{atelier_bin}} ./cmd/atelier

# Install into $GOBIN
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

# Validate the bundled module gallery: run each entry's scaffold command and validate the wrapper (ADR-0035).
gallery-check: build-bin
    #!/usr/bin/env bash
    set -euo pipefail
    atelier="{{atelier_bin}}"
    commands="$("$atelier" gallery list --commands)"
    fail=0
    while IFS= read -r line; do
      [ -n "$line" ] || continue
      echo "==> $line"
      scratch="$(mktemp -d)"
      if ! (
        cd "$scratch"
        # Run the gallery's own scaffold command with the binary under test,
        # stdin closed so the TUI never starts. `read -a` splits the generated
        # line on whitespace without globbing or evaluating shell syntax.
        read -r -a add_args <<< "${line#atelier }"
        "$atelier" "${add_args[@]}" < /dev/null
        terraform init -backend=false -no-color >/dev/null
        terraform validate -no-color
      ); then
        fail=1
      fi
      rm -rf "$scratch"
    done <<< "$commands"
    exit "$fail"

# Both cloud tiers (local convenience; CI runs them as separate jobs).
test-cloud: test-prometheus test-import

# Tidy module dependencies.
tidy:
    go mod tidy

# Remove the dev-built binary.
clean:
    rm -f {{atelier_bin}}
