# Atelier developer tasks. Run `just --list` (or `just`) to see recipes.

set shell := ["bash", "-uc"]

# Path to the dev-built binary; gitignored via the repo-root /atelier rule.
atelier_bin := justfile_directory() + "/atelier"

# Shared integration-test invocation.
pytest := "uv run --project tests/integration --frozen pytest"
pytest_flags := "-vv -ra --capture=no --exitfirst"

# GitHub Pages site build (MkDocs Material), via uv like the integration tests.
site := "uv run --project website --frozen"

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

# Build the GitHub Pages site into website/site/, generating the gallery page.
site-build:
    go run ./tools/gallerysite -o website/docs/gallery.md
    {{site}} mkdocs build --strict -f website/mkdocs.yml

# Serve the GitHub Pages site locally with live reload.
site-serve:
    go run ./tools/gallerysite -o website/docs/gallery.md
    {{site}} mkdocs serve -f website/mkdocs.yml

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
    presets="{{justfile_directory()}}/internal/gallery/presets"
    scan="$("$atelier" gallery list --commands)"
    fail=0
    while IFS= read -r line; do
      [ -n "$line" ] || continue
      # The scaffold command is `atelier module add <name> …`; the entry name is
      # the third token after stripping the leading `atelier`.
      name="$(awk '{print $3}' <<< "${line#atelier }")"
      scratch="$(mktemp -d)"
      if ! (
        cd "$scratch"
        # Publish the gallery's presets as a walk-up atelier.presets/ so the
        # entry's --var-file resolves the way it does for a user who copied it,
        # then run the entry's own scaffold command with the binary under test,
        # stdin closed so the TUI never starts. `read -a` splits the generated
        # line on whitespace without globbing or evaluating shell syntax.
        mkdir -p atelier.presets
        cp "$presets"/*.tfvars atelier.presets/
        read -r -a add_args <<< "${line#atelier }"
        # Entries whose module declares deployment-specific inputs without a
        # default (a Juju model UUID, S3 credentials) cannot validate against
        # the preset alone. Supply a placeholder for exactly those, read from
        # the entry's own `requires` list; the preset stays free of fake values,
        # and a real user supplies the real ones. `presets lint` already checked
        # the preset's keys.
        vars=()
        while IFS= read -r req; do
          [ -n "$req" ] || continue
          case "$req" in
            *uuid*) vars+=(--var "$req=00000000-0000-0000-0000-000000000000") ;;
            *) vars+=(--var "$req=placeholder") ;;
          esac
        done < <("$atelier" gallery requires "$name")
        echo "==> atelier ${add_args[*]} ${vars[*]}"
        "$atelier" "${add_args[@]}" "${vars[@]}" < /dev/null
        terraform init -backend=false -no-color >/dev/null
        terraform validate -no-color
      ); then
        fail=1
      fi
      rm -rf "$scratch"
    done <<< "$scan"
    exit "$fail"

# Both cloud tiers (local convenience; CI runs them as separate jobs).
test-cloud: test-prometheus test-import

# Tidy module dependencies.
tidy:
    go mod tidy

# Remove the dev-built binary.
clean:
    rm -f {{atelier_bin}}
