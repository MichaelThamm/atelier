# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""A revision bump re-plans in place rather than replacing the running apps.

Deploy cos-lite at the gallery's pinned revision, then re-point the wrapper at
`main` and plan. The assertion is Terraform's own: no application, integration
or offer is destroyed to make the bump happen. Creating a component the newer
revision adds is fine; replacing the running ones is the disaster this guards.

The re-point itself — rewriting the block instead of appending a second one — is
covered in the fast tier. What needs a live deployment is the consequence.
"""

import re

import jubilant
import pytest

from helpers import TfDirManager, atelier, wait_for_active_idle_without_error

COS_SOURCE = "cos-lite"  # gallery entry
COS_PRESET = "cos-lite-no-ingress"
UPGRADE_REF = "main"
CORE_TYPES = frozenset({"juju_application", "juju_integration", "juju_offer"})


@pytest.mark.cloud
def test_a_ref_bump_does_not_replace_the_running_apps(
    tf_manager: TfDirManager, juju: jubilant.Juju, tmp_path
):
    model_uuid = juju.show_model(juju.model).model_uuid
    (tmp_path / "ci.tfvars").write_text(
        f'model = {{ uuid = "{model_uuid}", name = "{juju.model}" }}\n'
    )

    # GIVEN cos-lite deployed at the gallery's pinned revision
    atelier(
        f"apply {COS_SOURCE} --var-file ci.tfvars --var-file {COS_PRESET}"
        f" --var internal_tls=false --dir wrapper --strict",
        cwd=tmp_path,
    )
    wrapper = tmp_path / "wrapper"
    wait_for_active_idle_without_error(juju)

    # AND the wrapper re-pointed at main, as a `--ref` bump would leave it
    main_tf = wrapper / "main.tf"
    original = main_tf.read_text()
    bumped = re.sub(r"\?ref=[^\"&\s]+", f"?ref={UPGRADE_REF}", original)
    assert bumped != original, "the wrapper did not pin a ref to bump"
    main_tf.write_text(bumped)

    # WHEN the bumped configuration is planned against the running state
    tf_manager.latch(wrapper)
    tf_manager.init()
    changes = tf_manager.plan_changes()

    # THEN nothing is destroyed to make the bump: creating a component the new
    # revision adds is fine, replacing a running one is not.
    destroyed = [c for c in changes if c[1] in CORE_TYPES and "delete" in c[2]]
    assert not destroyed, f"a ref bump would destroy these core resources: {destroyed}"