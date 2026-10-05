# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""`atelier import` round-trip: deploy COS-Lite, delete its state, recover it.

This is a user's disaster-recovery flow — `add` to author the wrapper,
`atelier apply` to deploy, then `import` to rebuild state from what is still
running. Everything in between runs through the registered Juju provider:
detection, discovery, matching, and import-ID construction.

Two things keep it from being a happy path. The module is configured from the
upstream ``no-ingress`` preset as well as a local bundle, so a preset resolved
from the clone is exercised too. And the final check is a ``terraform plan``
rather than Atelier's own report: the state must match reality, not merely be
non-empty. Drift in ``terraform_data`` and secrets is expected — see
``PRESERVED_TYPES`` for why it is tolerated.
"""

import json
from pathlib import Path

import jubilant
import pytest

from helpers import run, wait_for_active_idle_without_error, write_tfvars

COS_REPO = "https://github.com/canonical/observability-stack.git"
COS_MODULE = "terraform/cos-lite"
COS_REF = "main"
# The module's own preset, resolved by name from the clone (upstream discovery).
COS_PRESET = "no-ingress"

# The core resources `atelier import` must recover and reproduce exactly: the
# applications, the relations between them, and the offers they expose.
#
# Other types may drift without failing. `terraform_data` has no live object to
# import at all, and `juju_secret` carries an identifier no plan can satisfy from
# imported state — so demanding they match would fail on correct behaviour.
PRESERVED_TYPES = frozenset({"juju_application", "juju_integration", "juju_offer"})

# Types COS-Lite may create that need not round-trip. Naming them means the
# completeness check below fails, rather than silently passing, if COS-Lite ever
# creates a type nobody has decided about.
DRIFT_TYPES = frozenset({"terraform_data", "juju_secret", "juju_access_secret"})


def _hcl_block(text: str, name: str) -> str:
    """Return the `{...}` body of `name = {...}` in `text`, brace-balanced.

    A plain `split("}", 1)` truncates at the first closing brace, which is wrong
    for a block whose values are themselves objects. This walks the braces so
    nested values are included, and skips quoted strings so a `}` inside one
    does not end the block early.
    """
    start = text.index(f"{name} = {{") + len(f"{name} = ")
    depth = 0
    in_str = False
    escaped = False
    for i, ch in enumerate(text[start:], start):
        if in_str:
            if escaped:
                escaped = False
            elif ch == "\\":
                escaped = True
            elif ch == '"':
                in_str = False
            continue
        if ch == '"':
            in_str = True
        elif ch == "{":
            depth += 1
        elif ch == "}":
            depth -= 1
            if depth == 0:
                return text[start + 1 : i]
    raise AssertionError(f"unbalanced braces in {name} block")


@pytest.mark.cloud
def test_import_cos_lite_roundtrip(tf_manager, juju: jubilant.Juju, tmp_path):
    # GIVEN a running Juju model
    model_uuid = juju.show_model(juju.model).model_uuid

    # AND a fresh directory for Atelier to author a wrapper into
    wrapper_dir = tmp_path

    # AND a bundle describing the deployment for that model, kept beside the
    # wrapper directory: the wrapper directory itself stays empty for `--dir`.
    bundle = write_tfvars(
        wrapper_dir.parent,
        "ci",
        {
            "model": {"uuid": model_uuid, "name": juju.model},
            "internal_tls": False,
        },
    )

    # WHEN Atelier bootstraps the module, non-interactively, pinned to --ref
    # and configured from both bundles: the explicit ci values, then the
    # module's own no-ingress preset.
    added = json.loads(run("add", COS_REPO, "--module", COS_MODULE, "--ref", COS_REF,
                           "--dir", ".", "--var-file", str(bundle), "--var-file", COS_PRESET,
                           "--strict", "--yes", "--json", cwd=wrapper_dir).stdout)["data"]

    # THEN the wrapper went where the test prepared it, at the subdirectory the
    # repository puts the module in, and main.tf has the bundle values written
    # through with every ingress component switched off
    assert Path(added["wrapper"]) == wrapper_dir
    assert added["added"]["modulePath"] == COS_MODULE
    assert added["added"]["ref"] == COS_REF
    main_tf = (wrapper_dir / "main.tf").read_text()
    assert f'uuid = "{model_uuid}"' in main_tf
    assert "internal_tls = false" in main_tf
    ingress_block = _hcl_block(main_tf, "ingress")
    assert "= false" in ingress_block
    assert "= true" not in ingress_block

    # AND the module is deployed. `apply` is `add` plus `terraform init` and
    # `apply`, so Terraform needs no separate invocation here.
    run("apply", COS_REPO, "--module", COS_MODULE, "--ref", COS_REF, "--dir", ".",
        "--var-file", str(bundle), "--var-file", COS_PRESET, "--strict", cwd=wrapper_dir)

    # THEN the model settles active and idle
    wait_for_active_idle_without_error(juju)

    # AND the state is deleted, orphaning the live deployment — after first
    # recording what apply created, so the test can flag any COS-Lite resource
    # type it has not classified
    state_file = wrapper_dir / "terraform.tfstate"
    assert state_file.exists(), "apply should have produced terraform.tfstate"
    applied = json.loads(state_file.read_text())
    applied_types = {
        r["type"]
        for r in applied.get("resources", [])
        if r.get("mode", "managed") == "managed" and r.get("instances")
    }
    unclassified = applied_types - PRESERVED_TYPES - DRIFT_TYPES
    assert not unclassified, (
        "COS-Lite created resource type(s) this test does not classify: "
        f"{sorted(unclassified)}. Add each to PRESERVED_TYPES (must round-trip) "
        "or DRIFT_TYPES (may drift)."
    )
    state_file.unlink()
    (wrapper_dir / "terraform.tfstate.backup").unlink(missing_ok=True)

    # WHEN Atelier imports the live deployment back into a fresh state, with
    # the same --ref/--var-file bundles and the model UUID as a query variable
    result = json.loads(run("import", "juju", "--source", COS_REPO, "--module", COS_MODULE,
                            "--ref", COS_REF, "--dir", ".", "--var-file", str(bundle),
                            "--var-file", COS_PRESET,
                            "--query-var", f"model_uuid={model_uuid}",
                            "--yes", "--json", cwd=wrapper_dir).stdout)["data"]

    # THEN live objects were matched to module addresses and imported
    assert result["matched"], "nothing matched the module's resources"
    assert result["imported"], f"nothing was imported: {result['unresolved']}"
    assert state_file.exists(), "import should have repopulated terraform.tfstate"

    # AND the core resource types really were recovered (guards against a
    # vacuous pass where nothing matched)
    imported = json.loads(state_file.read_text())
    imported_types = {r["type"] for r in imported.get("resources", [])}
    missing = PRESERVED_TYPES - imported_types
    assert not missing, f"import did not recover core resource types: {sorted(missing)}"

    # AND the strongest check: a plan against the imported state finds nothing
    # to change for those core resources. Drift in other types is tolerated —
    # see PRESERVED_TYPES for why.
    #
    # This is the one thing left that needs Terraform directly. `atelier` has no
    # command that answers "would a plan change anything?", and it needs the
    # addresses, not the counts an import --dry-run preview gives.
    tf_manager.latch(wrapper_dir)
    changes = tf_manager.plan_changes()
    drifted = sorted(c for c in changes if c[1] in PRESERVED_TYPES)
    assert not drifted, (
        "import did not preserve these core resources "
        f"(address, type, actions): {drifted}"
    )
