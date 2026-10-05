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

import jubilant
import pytest

from helpers import atelier_apply, atelier_ls, write_tfvars

PROM_REPO = "https://github.com/canonical/prometheus-k8s-operator.git"
PROM_MODULE = "terraform"


@pytest.mark.cloud
def test_deploy_prometheus_k8s(juju: jubilant.Juju, tmp_path):
    # GIVEN a running Juju model
    model_uuid = juju.show_model(juju.model).model_uuid

    # AND a fresh directory for Atelier to author a wrapper into
    wrapper_dir = tmp_path

    # AND a bundle describing the deployment for that model, kept beside the
    # wrapper directory so the prepared directory stays empty for `--dir`.
    bundle = write_tfvars(
        wrapper_dir.parent,
        "ci",
        {"model_uuid": model_uuid, "channel": "dev/edge", "units": 1},
    )

    # WHEN Atelier deploys it, non-interactively, from the bundle. `apply` is
    # `add` plus `terraform init` and `apply`, so this test needs no Terraform
    # of its own.
    atelier_apply(
        PROM_REPO,
        module=PROM_MODULE,
        dir=".",
        cwd=wrapper_dir,
        var_file=[bundle],
    )

    # THEN the wrapper went where the test prepared it and declares the module
    # at the subdirectory the repository puts it in, with the bundle values
    # written through
    assert [m["name"] for m in atelier_ls(cwd=wrapper_dir)] == ["prometheus_k8s_operator"]
    main_tf = (wrapper_dir / "main.tf").read_text()
    assert "//terraform" in main_tf
    assert model_uuid in main_tf
