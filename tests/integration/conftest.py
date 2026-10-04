# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Conftest file for Atelier's Juju + Terraform integration tests."""

import os

import jubilant
import pytest

from helpers import TfDirManager


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


@pytest.fixture(scope="module")
def juju(request) -> jubilant.Juju:
    """A temporary Juju model, destroyed (or kept) after the module's tests run."""
    with jubilant.temp_model(keep=_keep_models(request)) as model:
        yield model


@pytest.fixture
def tf_manager() -> TfDirManager:
    """A Terraform checker for the wrapper Atelier wrote.

    Function-scoped, because each test latches it onto its own directory.
    """
    return TfDirManager()