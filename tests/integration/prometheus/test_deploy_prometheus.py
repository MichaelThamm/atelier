# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Deploy canonical/prometheus-k8s-operator with Atelier + Terraform against Juju.

Flow under test:

1. Create a temporary Juju model (Jubilant).
2. Shell out to Atelier to bootstrap a wrapper and configure the module from a
   preset, non-interactively (``stdin=/dev/null`` skips the TUI).
3. Latch Terraform onto the wrapper Atelier wrote and ``init`` + ``apply`` it.
4. Assert the model settles active and idle with the expected application.

The module deploys a single application and needs no relations or object
storage, so it settles quickly.
"""

from pathlib import Path

import jubilant
import pytest

from helpers import run_atelier, wait_for_active_idle_without_error, write_var_file

PROM_REPO = "https://github.com/canonical/prometheus-k8s-operator.git"
PROM_MODULE = "terraform"
PROM_APP = "prometheus"


@pytest.mark.cloud
def test_deploy_prometheus_k8s(tf_manager, juju: jubilant.Juju, atelier_bin: str):
    # GIVEN a running Juju model
    model_uuid = juju.show_model(juju.model).model_uuid

    # AND a fresh directory for Atelier to author a wrapper into
    wrapper_dir = tf_manager.new_wrapper_dir()

    # AND a bundle describing the deployment for that model
    var_file = write_var_file(
        wrapper_dir,
        "ci",
        {"model_uuid": model_uuid, "channel": "dev/edge", "units": 1},
    )

    # WHEN Atelier bootstraps the module, non-interactively, from the bundle
    run_atelier(
        wrapper_dir,
        atelier_bin,
        "module",
        "add",
        PROM_REPO,
        "--module",
        PROM_MODULE,
        "--var-file",
        var_file,
        "--yes",
    )

    # AND the wrapper really does reference the module with the bundle values
    main_tf = (Path(wrapper_dir) / "main.tf").read_text()
    assert "//terraform" in main_tf
    assert model_uuid in main_tf

    # AND Terraform latches onto it and applies
    tf_manager.init()
    tf_manager.apply()

    # THEN the model settles active and idle
    wait_for_active_idle_without_error(juju)

    # AND the expected application is present and active
    status = juju.status()
    assert PROM_APP in status.apps, f"{PROM_APP} missing; got {sorted(status.apps)}"
    assert status.apps[PROM_APP].app_status.current == "active"
