# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Adopt a hand-deployed slice of COS-Lite, then apply the rest.

A user can deploy a couple of charms with the Juju client and want Terraform to
own them, then converge the rest. This deploys `alertmanager` and `catalogue`
with Jubilant (no Terraform), relates them, imports them into a fresh cos-lite
wrapper, checks the plan adds the missing components rather than duplicating
what is live, then applies and asserts the stack converges.

The relation matters: a `juju_integration` has a composite identity, so adopting
the live relation exercises a different matcher from the applications.
"""

import json

import jubilant
import pytest

from helpers import TfDirManager, atelier, wait_for_active_idle_without_error

COS_SOURCE = "cos-lite"  # gallery entry
COS_REF = "main"
COS_PRESET = "cos-lite-no-ingress"

# The applications deployed by hand; the module must adopt them rather than
# create fresh copies.
HAND_DEPLOYED = (("alertmanager-k8s", "alertmanager"), ("catalogue-k8s", "catalogue"))
# The module's `catalogue_integrations["alertmanager"]`: alertmanager
# `requires.catalogue` joined to catalogue `provides.catalogue`.
RELATION = ("alertmanager:catalogue", "catalogue:catalogue")

APPLY = (
    f"apply {COS_SOURCE} --ref {COS_REF} --var-file {COS_PRESET}"
    f" --var internal_tls=false --dir wrapper --strict"
)


@pytest.mark.cloud
def test_adopt_a_hand_deployed_slice_then_apply_the_rest(
    tf_manager: TfDirManager, juju: jubilant.Juju, tmp_path
):
    model_uuid = juju.show_model(juju.model).model_uuid

    # GIVEN two charms deployed and related with the Juju client, no Terraform
    for charm, app in HAND_DEPLOYED:
        juju.deploy(charm, app=app)
    juju.integrate(*RELATION)
    juju.wait(jubilant.all_agents_idle, delay=5, timeout=60 * 20)

    # WHEN Atelier imports the model into a fresh wrapper for the whole module
    reported = atelier(
        f"import juju --source {COS_SOURCE} --ref {COS_REF}"
        f" --var-file {COS_PRESET} --var internal_tls=false"
        f" --query-var model_uuid={model_uuid} --dir wrapper --yes --json",
        cwd=tmp_path, check=False,
    )
    data = json.loads(reported.stdout)["data"]

    # THEN the two live applications were matched and imported
    assert data["matched"], reported.stderr
    for _, app in HAND_DEPLOYED:
        assert any(f".juju_application.{app}" in addr for addr in data["imported"]), (
            app,
            data["imported"],
        )

    # AND so was the live relation, whose composite identity takes a different
    # matcher from the applications
    assert any(
        "juju_integration" in addr and "alertmanager" in addr
        for addr in data["imported"]
    ), data["imported"]

    # AND the plan creates the missing components, not more copies of what is
    # already live
    plan = data["postImportPlan"]
    assert plan is not None, data
    adds = plan["addAddresses"]
    for _, app in HAND_DEPLOYED:
        assert not any(f".juju_application.{app}" in a for a in adds), (app, adds)
    assert not any(
        "catalogue_integrations" in a and "alertmanager" in a for a in adds
    ), adds
    assert any("loki" in a for a in adds), adds
    assert any("grafana" in a for a in adds), adds

    # WHEN the rest of the module is applied
    atelier(APPLY, cwd=tmp_path)
    wait_for_active_idle_without_error(juju)

    # THEN the stack has converged on the module's main state
    tf_manager.latch(tmp_path / "wrapper")
    assert tf_manager.plan_changes() == [], "the converged stack still has drift"