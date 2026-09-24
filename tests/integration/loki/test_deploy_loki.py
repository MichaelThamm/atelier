# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Deploy canonical/loki-operators with Atelier + Terraform against Juju.

Flow under test:

1. Create a temporary Juju model (Jubilant).
2. Deploy seaweedfs-k8s into it and discover the S3 endpoint from the model.
3. Shell out to Atelier to bootstrap a wrapper and configure the module from a
   preset, non-interactively (``stdin=/dev/null`` skips the TUI).
4. Latch Terraform onto the wrapper Atelier wrote and ``init`` + ``apply`` it.
5. Assert the model settles active and idle.
"""

import jubilant
import pytest

from helpers import run_atelier, wait_for_active_idle_without_error, write_preset_file

LOKI_REPO = "https://github.com/canonical/loki-operators.git"
# loki-operators ships three Terraform roots (repo root, coordinator/, worker/);
# pick the root module explicitly so candidate discovery is unambiguous.
LOKI_MODULE = "terraform"

# Application names the root module deploys.
EXPECTED_APPS = (
    "loki",
    "loki-backend",
    "loki-read",
    "loki-write",
    "loki-s3-integrator",
)


@pytest.mark.cloud
def test_deploy_loki_operators(
    tf_manager, juju: jubilant.Juju, atelier_bin: str, s3_endpoint: str
):
    # GIVEN a running Juju model
    model_uuid = juju.show_model(juju.model).model_uuid

    # AND a preset describing the S3-backed loki deployment for that model,
    # pointing at the seaweedfs S3 endpoint discovered from the model
    preset_path = write_preset_file(tf_manager.base, model_uuid, s3_endpoint)

    # AND a fresh directory for Atelier to author a wrapper into
    wrapper_dir = tf_manager.new_wrapper_dir()

    # WHEN Atelier bootstraps the module, non-interactively, from the preset
    run_atelier(
        wrapper_dir,
        atelier_bin,
        "module",
        "add",
        LOKI_REPO,
        "--module",
        LOKI_MODULE,
        "--preset",
        str(preset_path),
        "--yes",
    )

    # AND Terraform latches onto the wrapper Atelier wrote and applies it
    tf_manager.init()
    tf_manager.apply()

    # THEN the model settles active and idle
    wait_for_active_idle_without_error(juju)

    # AND the expected applications are present
    apps = juju.status().apps
    for name in EXPECTED_APPS:
        assert name in apps, f"{name} missing from model; got {sorted(apps)}"
