# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""`atelier import` round-trip against a live COS-Lite deployment.

Flow under test — the end-to-end value of the provider registry
(internal/importer/providers), exercised with non-default module inputs:

1. Create a temporary Juju model (Jubilant).
2. Shell out to Atelier to bootstrap a COS-Lite wrapper pinned to ``--ref``
   and configured from a ``--preset`` (the model to deploy into, and
   ``internal_tls = false``), non-interactively (``stdin=/dev/null`` skips the
   TUI).
3. ``terraform init`` + ``apply`` to deploy COS-Lite into the model.
4. Delete the Terraform state, leaving the live deployment orphaned.
5. Run ``atelier import`` with the *same* ``--ref`` and ``--preset``, letting it
   rebuild the state from live resources — detection, discovery, matching,
   import-ID construction and the post-import steps all run through the
   registered Juju provider.
6. Assert the run reported matches/imports, that the state file was
   repopulated, and — the strongest check — that a following ``terraform plan``
   finds nothing to change for any resource with a live counterpart (only the
   unimportable ``terraform_data`` bookkeeping may remain).

``--query-var model_uuid`` is required: the Juju list resources for
applications and integrations carry a required ``model_uuid`` config block, so
without it only ``juju_model``/``juju_offer`` are queryable.

This is deliberately the same recipe as a user's disaster-recovery flow:
``module add`` to author the wrapper, then ``import`` to recover state from a
live model.
"""

from pathlib import Path

import jubilant
import pytest

from helpers import run_atelier, wait_for_active_idle_without_error, write_local_preset

COS_REPO = "https://github.com/canonical/observability-stack.git"
COS_MODULE = "terraform/cos-lite"
COS_REF = "main"


@pytest.mark.cloud
def test_import_cos_lite_roundtrip(tf_manager, juju: jubilant.Juju, atelier_bin: str):
    # GIVEN a running Juju model
    model_uuid = juju.show_model(juju.model).model_uuid

    # AND a fresh directory for Atelier to author a wrapper into
    wrapper_dir = tf_manager.new_wrapper_dir()

    # AND a preset describing the deployment for that model. COS-Lite takes a
    # `model` object (not the flat `model_uuid` the prometheus module uses):
    # setting `model.uuid` makes the module look up the temp model instead of
    # creating a new one. `internal_tls = false` is a non-default input that
    # changes the resource set (no self-signed-certificates app, no internal
    # certificate integrations), so the import has real configuration to
    # reproduce rather than just module defaults.
    preset = write_local_preset(
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
        "--preset",
        preset,
        "--yes",
    )

    # AND the wrapper really does reference the module at the ref, with the
    # preset values written through
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

    # AND the state is deleted, orphaning the live deployment
    wrapper = Path(wrapper_dir)
    state_file = wrapper / "terraform.tfstate"
    assert state_file.exists(), "apply should have produced terraform.tfstate"
    state_file.unlink()
    (wrapper / "terraform.tfstate.backup").unlink(missing_ok=True)

    # WHEN Atelier imports the live deployment back into a fresh state, with
    # the same --ref/--preset flags and the model UUID as a query variable
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
        "--preset",
        preset,
        "--query-var",
        f"model_uuid={model_uuid}",
        capture=True,
    )

    # THEN the run matched live objects to module addresses, imported them,
    # and the post-import steps repopulated the state file
    assert "Matched" in result.stderr, f"no matches reported:\n{result.stderr}"
    assert "Imported" in result.stderr, f"nothing imported:\n{result.stderr}"
    assert state_file.exists(), "import should have repopulated terraform.tfstate"
    assert '"type": "juju_' in state_file.read_text(), "state should contain juju resources"

    # AND the strongest check: the import reproduced the deployment. A plan
    # against the imported state must find nothing to change for resources that
    # have a live counterpart. The only permitted delta is `terraform_data`
    # (replace-trigger bookkeeping): it exists only in Terraform state, has no
    # live object to import, and Atelier excludes it from import candidates for
    # exactly that reason.
    changes = tf_manager.plan_changes()
    importable = [c for c in changes if c[1] != "terraform_data"]
    assert not importable, (
        "import did not reproduce state; a plan still wants to change these "
        f"importable resources (address, type, actions): {importable}"
    )