# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""`atelier import` round-trip against a live COS-Lite deployment.

Flow under test — the end-to-end value of the provider registry
(internal/importer/providers):

1. Create a temporary Juju model (Jubilant).
2. Shell out to Atelier to bootstrap a COS-Lite wrapper from a preset,
   non-interactively (``stdin=/dev/null`` skips the TUI).
3. ``terraform init`` + ``apply`` to deploy COS-Lite into the model.
4. Delete the Terraform state, leaving the live deployment orphaned.
5. Run ``atelier import`` and let it rebuild the state from live resources —
   detection, discovery, matching, import-ID construction and the post-import
   steps all run through the registered Juju provider.
6. Assert the run reported matches/imports and the state file was
   repopulated.

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


@pytest.mark.cloud
def test_import_cos_lite_roundtrip(tf_manager, juju: jubilant.Juju, atelier_bin: str):
    # GIVEN a running Juju model
    model_uuid = juju.show_model(juju.model).model_uuid

    # AND a fresh directory for Atelier to author a wrapper into
    wrapper_dir = tf_manager.new_wrapper_dir()

    # AND a preset describing the deployment for that model
    preset = write_local_preset(
        wrapper_dir,
        "ci",
        {"model_uuid": model_uuid, "name": juju.model},
    )

    # WHEN Atelier bootstraps the module, non-interactively, from the preset
    run_atelier(
        wrapper_dir,
        atelier_bin,
        "module",
        "add",
        COS_REPO,
        "--module",
        COS_MODULE,
        "--preset",
        preset,
        "--yes",
    )

    # AND the wrapper really does reference the module with the model UUID
    main_tf = (Path(wrapper_dir) / "main.tf").read_text()
    assert "//terraform/cos-lite" in main_tf
    assert model_uuid in main_tf

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

    # WHEN Atelier imports the live deployment back into a fresh state
    result = run_atelier(
        wrapper_dir,
        atelier_bin,
        "import",
        capture=True,
    )

    # THEN the run matched live objects to module addresses, imported them,
    # and the post-import steps repopulated the state file
    assert "Matched" in result.stderr, f"no matches reported:\n{result.stderr}"
    assert "Imported" in result.stderr, f"nothing imported:\n{result.stderr}"
    assert state_file.exists(), "import should have repopulated terraform.tfstate"
    state_text = state_file.read_text()
    assert state_text.count('"type": "juju_') >= 1, "state should contain juju resources"