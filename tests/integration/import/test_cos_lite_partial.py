# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Import a hand-deployed slice of COS-Lite, then let the plan fill the rest.

A user can deploy a couple of charms with the Juju client and want Terraform to
own them, then add the rest. This deploys `alertmanager` and `catalogue` with
Jubilant (no Terraform), imports them into a fresh cos-lite wrapper, and asserts
the resulting plan *adds* the components that are missing instead of duplicating
the two that are already live.

No `apply`: the plan is the assertion. Applying would stand the whole stack up,
which is the cost this arrangement avoids.
"""

import json

import jubilant
import pytest

from helpers import atelier

COS_SOURCE = "cos-lite"  # gallery entry
COS_REF = "main"
COS_PRESET = "cos-lite-no-ingress"

# The two applications deployed by hand; the module must adopt them rather than
# create fresh copies.
HAND_DEPLOYED = (("alertmanager-k8s", "alertmanager"), ("catalogue-k8s", "catalogue"))


@pytest.mark.cloud
def test_import_a_hand_deployed_slice_then_plan_the_rest(juju: jubilant.Juju, tmp_path):
    model_uuid = juju.show_model(juju.model).model_uuid

    # GIVEN two charms deployed with the Juju client, no Terraform involved
    for charm, app in HAND_DEPLOYED:
        juju.deploy(charm, app=app)
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

    # AND the plan creates the missing components, not more copies of those two
    plan = data["postImportPlan"]
    assert plan is not None, data
    adds = plan["addAddresses"]
    for _, app in HAND_DEPLOYED:
        assert not any(f".juju_application.{app}" in a for a in adds), (app, adds)
    assert any("loki" in a for a in adds), adds
    assert any("grafana" in a for a in adds), adds