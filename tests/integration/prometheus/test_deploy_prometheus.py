# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Deploy canonical/prometheus-k8s-operator with Atelier + Terraform against Juju.

Flow under test:

1. Create a temporary Juju model (Jubilant).
2. Deploy with ``atelier apply``, which writes the wrapper and then runs
   ``terraform init`` and ``apply`` there, and assert on the wrapper it wrote
   rather than on its terminal output.

Whether the charm then settles active is the charm repository's concern, not
Atelier's; this test ends at a successful apply.
"""

import json

import jubilant
import pytest

from helpers import atelier

PROM_REPO = "https://github.com/canonical/prometheus-k8s-operator.git"
PROM_MODULE = "terraform"


@pytest.mark.cloud
def test_deploy_prometheus_k8s(juju: jubilant.Juju, tmp_path):
    # GIVEN a running Juju model
    model_uuid = juju.show_model(juju.model).model_uuid

    # AND a bundle describing the deployment for that model, beside the wrapper
    # rather than inside it, so Atelier has an empty directory to scaffold into
    (tmp_path / "ci.tfvars").write_text(
        f'model_uuid = "{model_uuid}"\nchannel = "dev/edge"\nunits = 1\n'
    )

    # WHEN Atelier deploys it, non-interactively, from the bundle. `apply` is
    # `add` plus `terraform init` and `apply`, so this test needs no Terraform of
    # its own — and no --yes, which the CLI rejects here because Terraform's own
    # plan prompt is the confirmation.
    atelier(
        f"apply {PROM_REPO} --module {PROM_MODULE} --dir wrapper"
        " --var-file ci.tfvars --strict",
        cwd=tmp_path,
    )

    # THEN the wrapper went where the test prepared it and declares the module
    # at the subdirectory the repository puts it in, with the bundle values
    # written through
    wrapper = tmp_path / "wrapper"
    modules = json.loads(atelier("ls --json", cwd=wrapper).stdout)["data"]["modules"]
    assert [m["name"] for m in modules] == ["prometheus_k8s_operator"]
    main_tf = (wrapper / "main.tf").read_text()
    assert "//terraform" in main_tf
    assert model_uuid in main_tf