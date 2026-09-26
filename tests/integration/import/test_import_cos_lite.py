# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""`atelier import` round-trip against a live COS-Lite deployment.

Flow under test — the end-to-end value of the provider registry
(internal/importer/providers), exercised with non-default module inputs:

1. Create a temporary Juju model (Jubilant).
2. Shell out to Atelier to bootstrap a COS-Lite wrapper pinned to ``--ref``
   and configured from a ``--var-file`` bundle (the model to deploy into, and
   ``internal_tls = false``), non-interactively (``stdin=/dev/null`` skips the
   TUI).
3. ``terraform init`` + ``apply`` to deploy COS-Lite into the model.
4. Delete the Terraform state, leaving the live deployment orphaned.
5. Run ``atelier import`` with the *same* ``--ref`` and ``--var-file``, letting it
   rebuild the state from live resources — detection, discovery, matching,
   import-ID construction and the post-import steps all run through the
   registered Juju provider.
6. Assert the run reported matches/imports, that the state file was
   repopulated, and — the strongest check — that a following ``terraform plan``
   finds nothing to change for the core resources the module manages
   (applications, integrations, offers). Drift in other types (``terraform_data``,
   secrets) is tolerated; see ``PRESERVED_TYPES``.

``--query-var model_uuid`` is required: the Juju list resources for
applications and integrations carry a required ``model_uuid`` config block, so
without it only ``juju_model``/``juju_offer`` are queryable.

This is deliberately the same recipe as a user's disaster-recovery flow:
``module add`` to author the wrapper, then ``import`` to recover state from a
live model.
"""

import json
from pathlib import Path

import jubilant
import pytest

from helpers import run_atelier, wait_for_active_idle_without_error, write_var_file

COS_REPO = "https://github.com/canonical/observability-stack.git"
COS_MODULE = "terraform/cos-lite"
COS_REF = "main"

# The core resources `atelier import` must recover and reproduce exactly: the
# applications, the relations between them, and the offers they expose.
#
# Other types are allowed to drift without failing the test. `terraform_data`
# has no live object to import at all (Atelier excludes it from import
# candidates for that reason), and provider-generated resources such as
# `juju_secret` carry identifiers that a plan can never satisfy from imported
# state. Asserting on the core set keeps the check meaningful without turning
# every benign re-creation into a failure.
PRESERVED_TYPES = frozenset({"juju_application", "juju_integration", "juju_offer"})

# Types COS-Lite may create that the test deliberately does not require to
# round-trip. Classifying them explicitly means the completeness check below
# fails — rather than silently passing — if COS-Lite ever starts creating a
# type nobody has decided about.
DRIFT_TYPES = frozenset({"terraform_data", "juju_secret", "juju_access_secret"})


@pytest.mark.cloud
def test_import_cos_lite_roundtrip(tf_manager, juju: jubilant.Juju, atelier_bin: str):
    # GIVEN a running Juju model
    model_uuid = juju.show_model(juju.model).model_uuid

    # AND a fresh directory for Atelier to author a wrapper into
    wrapper_dir = tf_manager.new_wrapper_dir()

    # AND a bundle describing the deployment for that model.
    var_file = write_var_file(
        wrapper_dir,
        "ci",
        {
            "model": {"uuid": model_uuid, "name": juju.model},
            "internal_tls": False,
        },
    )

    # WHEN Atelier bootstraps the module, non-interactively, pinned to --ref
    # and configured from the preset
    run_atelier(
        wrapper_dir,
        atelier_bin,
        "module",
        "add",
        COS_REPO,
        "--module",
        COS_MODULE,
        "--ref",
        COS_REF,
        "--var-file",
        var_file,
        "--yes",
    )

    # AND the wrapper really does reference the module at the ref, with the
    # bundle values written through
    main_tf = (Path(wrapper_dir) / "main.tf").read_text()
    assert "//terraform/cos-lite" in main_tf
    assert f"ref={COS_REF}" in main_tf
    assert model_uuid in main_tf
    assert "internal_tls = false" in main_tf

    # AND Terraform deploys COS-Lite into the model
    tf_manager.init()
    tf_manager.apply()

    # THEN the model settles active and idle
    wait_for_active_idle_without_error(juju)

    # AND the state is deleted, orphaning the live deployment — after first
    # recording what apply created, so the test can flag any COS-Lite resource
    # type it has not classified
    wrapper = Path(wrapper_dir)
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

    # WHEN Atelier imports the live deployment back into a fresh state, with
    # the same --ref/--var-file flags and the model UUID as a query variable
    result = run_atelier(
        wrapper_dir,
        atelier_bin,
        "import",
        "juju",
        "--source",
        COS_REPO,
        "--module",
        COS_MODULE,
        "--ref",
        COS_REF,
        "--var-file",
        var_file,
        "--query-var",
        f"model_uuid={model_uuid}",
        capture=True,
    )

    # THEN the run matched live objects to module addresses, imported them,
    # and the post-import steps repopulated the state file
    assert "Matched" in result.stderr, f"no matches reported:\n{result.stderr}"
    assert "Imported" in result.stderr, f"nothing imported:\n{result.stderr}"
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
    changes = tf_manager.plan_changes()
    drifted = sorted(c for c in changes if c[1] in PRESERVED_TYPES)
    assert not drifted, (
        "import did not preserve these core resources "
        f"(address, type, actions): {drifted}"
    )