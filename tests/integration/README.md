# Integration tests

End-to-end tests for Atelier against real modules and a real Juju controller.
They are grouped by what they need:

| Directory | Needs | What it proves |
| --- | --- | --- |
| [`wrapper/`](wrapper/) | Terraform | The `atelier add`/`apply` surface and the wrapper lifecycle: module selection and ref pinning, re-pointing a ref, repeat applies that converge, hand-edit preservation, `.gitignore` hygiene, and listing/removal. Real upstream modules and hermetic local ones; no Juju model. |
| [`import/`](import/) | Juju + Canonical K8s | Deploy COS-Lite, delete the Terraform state, and `atelier import` rebuilds it with no drift on the resources that have a live counterpart. |

The `import/` tier is marked `cloud` and takes tens of minutes; everything
else is the fast tier and runs in a minute or two.

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

## Writing a test

[`helpers.py`](helpers.py) has three things. `atelier(...)` runs one command line,
`TfDirManager` asks Terraform what became of the wrapper, and
`wait_for_active_idle_without_error` settles a model.

```python
from helpers import atelier

atelier("apply cos-lite --dir runner --ref track/2", cwd=tmp_path)
```

The command is a string, everything after the word `atelier`, so a test reads as
the command it runs — prefix `atelier` and it is something you can paste into a
shell. Two details it handles, both of which a hand-rolled `subprocess.run` gets
wrong:

- **stdin is `/dev/null`.** That is how Atelier knows there is no terminal:
  `apply` auto-approves instead of opening Terraform's plan prompt, and `add`
  writes the wrapper instead of launching the editor. In your own repo, end the
  command line with `< /dev/null` to get the same behaviour.
- **stdout is captured alone**, so a `--json` payload reads as
  `json.loads(result.stdout)["data"]` with no progress chatter mixed in.

One quoting limit: POSIX splitting strips quotes used as syntax, so a value whose
own text needs double quotes — an HCL object, say — loses them. Put those in a
`.tfvars` bundle and pass it with `--var-file`.

Set `KEEP_MODELS=true` (or pass `--keep-models`) to keep the temporary Juju
models when a test fails, so you can inspect them.

CI runs these in [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) —
after the `build · vet · test` job passes — against Juju + Canonical K8s
prepared by [Concierge](https://github.com/canonical/concierge). Pull requests
that change only documentation skip the integration tiers; pushes to `main` and
manual runs always execute them.
