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
import re
from pathlib import Path

import jubilant
import pytest

from helpers import atelier, wait_for_active_idle_without_error

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


def show(path: Path) -> str:
    """Return a file's text, echoing it to the CI log.

    The wrapper is the artifact (ADR-0001), so when an assertion below fails the
    question is always "what did Atelier actually write?" — and a failing job in
    a deleted temp directory cannot answer it. Printing it as it is read means
    the log carries the answer.
    """
    text = path.read_text()
    print(f"\n----- {path.name} -----\n{text}\n----- end {path.name} -----\n")
    return text


def _arg_block(main_tf: str, name: str) -> str:
    """Return the body of `name = {...}` in `main_tf`.

    Atelier writes one argument per line, so a non-greedy match to the first
    closing line ends the block. That is wrong if an argument is itself an object,
    which would truncate the body and let the caller's assertions pass on a
    prefix — so an unclosed brace in the result is a failure, not a shorter body.
    """
    match = re.search(rf"{name} = \{{(.*?)\n\s*\}}", main_tf, re.S)
    assert match, f"no {name} block in main.tf:\n{main_tf}"
    body = match.group(1)
    assert "{" not in body, f"{name} block was truncated at a nested object:\n{main_tf}"
    return body


@pytest.mark.cloud
def test_import_cos_lite_roundtrip(tf_manager, juju: jubilant.Juju, tmp_path):
    # GIVEN a running Juju model
    model_uuid = juju.show_model(juju.model).model_uuid

    # AND a bundle describing the deployment for that model, beside the wrapper
    # rather than inside it, so Atelier has an empty directory to scaffold into
    (tmp_path / "ci.tfvars").write_text(
        f'model = {{ uuid = "{model_uuid}", name = "{juju.model}" }}\ninternal_tls = false\n'
    )

    # WHEN Atelier deploys the module: `apply` is `add` plus `terraform init`
    # and `apply`, so this one call authors the wrapper and stands the stack up.
    # It is pinned to --ref and configured from both bundles, the explicit ci
    # values then the module's own no-ingress preset.
    atelier(
        f"apply {COS_REPO} --module {COS_MODULE} --ref {COS_REF} --dir wrapper"
        f" --var-file ci.tfvars --var-file {COS_PRESET} --strict",
        cwd=tmp_path,
    )

    wrapper = tmp_path / "wrapper"

    # THEN the wrapper went where the test prepared it, at the subdirectory the
    # repository puts the module in, and main.tf has the bundle values written
    # through with every ingress component switched off
    main_tf = show(wrapper / "main.tf")
    assert COS_MODULE in main_tf
    assert f"ref={COS_REF}" in main_tf
    assert f'uuid = "{model_uuid}"' in main_tf
    assert "internal_tls = false" in main_tf
    ingress_block = _arg_block(main_tf, "ingress")
    assert "= false" in ingress_block
    assert "= true" not in ingress_block

    # THEN the model settles active and idle
    wait_for_active_idle_without_error(juju)

    # AND the state is deleted, orphaning the live deployment — after first
    # recording what apply created, so the test can flag any COS-Lite resource
    # type it has not classified
    state_file = wrapper / "terraform.tfstate"
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
    (wrapper / "terraform.tfstate.backup").unlink(missing_ok=True)

    # WHEN Atelier imports the live deployment back into a fresh state, with the
    # same --ref and --var-file as the deploy above, and the model UUID as a
    # query variable. Note --query-var, not --var: the UUID feeds the query, not
    # the module, and conflating them writes it into main.tf.
    #
    # check=False because `atelier import` exits 1 when it matched nothing, and
    # letting that raise would replace the explanation below with a traceback.
    # Its payload is written either way.
    reported = atelier(
        f"import juju --source {COS_REPO} --module {COS_MODULE} --ref {COS_REF} --dir wrapper"
        f" --var-file ci.tfvars --var-file {COS_PRESET}"
        f" --query-var model_uuid={model_uuid} --yes --json",
        cwd=tmp_path, check=False,
    )
    result = json.loads(reported.stdout)["data"]

    # THEN live objects were matched to module addresses and imported
    assert result["matched"], (
        f"nothing matched the module's resources (import exited {reported.returncode})\n"
        f"{reported.stderr}"
    )
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
    tf_manager.latch(wrapper)
    changes = tf_manager.plan_changes()
    drifted = sorted(c for c in changes if c[1] in PRESERVED_TYPES)
    assert not drifted, (
        "import did not preserve these core resources "
        f"(address, type, actions): {drifted}"
    )