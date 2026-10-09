# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""A ref switch against a live deployment works and leaves a valid module.

Deploy cos-lite at the gallery's pinned revision, switch the wrapper to `main`
the way a user does — `atelier apply --ref` — and assert the switch succeeds and
the re-pointed module validates. Whether the bump replaces any resource is a
property of the product module, not of Atelier, so it is deliberately not
asserted here.
"""

import jubilant
import pytest

from helpers import TfDirManager, atelier, wait_for_active_idle_without_error

COS_SOURCE = "cos-lite"  # gallery entry
COS_PRESET = "cos-lite-no-ingress"
SWITCH_REF = "main"


@pytest.mark.cloud
def test_a_ref_switch_works_and_the_module_validates(
    tf_manager: TfDirManager, juju: jubilant.Juju, tmp_path
):
    model_uuid = juju.show_model(juju.model).model_uuid
    (tmp_path / "ci.tfvars").write_text(
        f'model = {{ uuid = "{model_uuid}", name = "{juju.model}" }}\n'
    )
    deploy = (
        f"apply {COS_SOURCE} --var-file ci.tfvars --var-file {COS_PRESET}"
        f" --var internal_tls=false --dir wrapper --strict"
    )

    # GIVEN cos-lite deployed at the gallery's pinned revision
    atelier(deploy, cwd=tmp_path)
    wrapper = tmp_path / "wrapper"
    wait_for_active_idle_without_error(juju)

    # WHEN the wrapper is switched to the revision under test
    atelier(f"{deploy} --ref {SWITCH_REF}", cwd=tmp_path)

    # THEN the block was re-pointed, not duplicated
    main_tf = (wrapper / "main.tf").read_text()
    assert f"?ref={SWITCH_REF}" in main_tf, main_tf
    assert main_tf.count('module "') == 1, main_tf

    # AND the switched module validates — the wrapper is a working Terraform
    # root at the new revision
    tf_manager.latch(wrapper)
    tf_manager.validate()