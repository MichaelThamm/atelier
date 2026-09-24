# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Conftest file for Atelier's Juju + Terraform integration tests."""

import os
import shutil

import jubilant
import pytest

from helpers import TfDirManager, deploy_seaweedfs, get_s3_endpoint


def pytest_addoption(parser):
    parser.addoption(
        "--keep-models",
        action="store_true",
        default=False,
        help="Keep temporarily-created models instead of destroying them after the tests run.",
    )


def _keep_models(request) -> bool:
    """Whether to keep temporary models, via CLI flag or the KEEP_MODELS env var."""
    return bool(request.config.getoption("--keep-models")) or (
        os.environ.get("KEEP_MODELS") is not None
    )


@pytest.fixture(scope="session")
def atelier_bin() -> str:
    """Path to the Atelier binary under test.

    CI builds the binary from the checkout and exports ``ATELIER_BIN``; locally
    we fall back to ``atelier`` on ``PATH``.
    """
    configured = os.environ.get("ATELIER_BIN")
    if configured and os.path.exists(configured):
        return configured
    found = shutil.which("atelier")
    if found:
        return found
    pytest.skip("ATELIER_BIN is not set and no 'atelier' binary is on PATH")


@pytest.fixture(scope="module")
def juju(request) -> jubilant.Juju:
    """A temporary Juju model, destroyed (or kept) after the module's tests run."""
    with jubilant.temp_model(keep=_keep_models(request)) as model:
        yield model


@pytest.fixture(scope="module")
def tf_manager(tmp_path_factory) -> TfDirManager:
    """A Terraform manager that latches onto the wrapper Atelier authors."""
    base = tmp_path_factory.mktemp("atelier_wrapper")
    return TfDirManager(base)


@pytest.fixture(scope="module")
def s3_endpoint(juju: jubilant.Juju) -> str:
    """Deploy a seaweedfs-k8s S3 backend and return its in-model endpoint."""
    deploy_seaweedfs(juju)
    endpoint = get_s3_endpoint(juju)
    print(f"\nseaweedfs S3 endpoint: {endpoint}\n")
    return endpoint
