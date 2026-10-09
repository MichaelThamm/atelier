# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""`atelier import` must refuse a configuration that targets another model.

The round-trip test proves the happy path; this is the dangerous one it cannot
reach. `model_uuid` forces replacement, so importing a configuration that names
a different model than the live resources came from would make the next apply
destroy every resource it imported. The check runs between the plan and the
match, so nothing is written — which is what this pins.

The module is a local `file://` git repo declaring one importable resource, not
a product module: the guard reads the planned `model_uuid` from any importable
resource, so a product module buys nothing here and costs a clone of the whole
observability stack plus a plan of fifty resources. The local module reaches the
same check for a fraction of the runner time, and needs only `terraform validate`
to stay honest about the provider schema.

No deployment is needed either: the guard fires before any import, so an empty
live model and an empty target model are enough. The dry-run and repeat-import
safety checks live in ``test_import_cos_lite_roundtrip``, so they share its one
deploy instead of standing a second COS-Lite up.
"""

import os
import subprocess
from pathlib import Path

import jubilant
import pytest

from helpers import atelier

# One importable resource carrying `model_uuid`, which is all the model
# consistency check reads. `charm` is required by the resource but never
# resolved for a create, so a placeholder name is enough.
PROBE_MODULE = """\
terraform {
  required_providers {
    juju = { source = "juju/juju" }
  }
}

variable "model_uuid" {
  type = string
}

resource "juju_application" "probe" {
  name       = "probe"
  model_uuid = var.model_uuid

  charm {
    name = "probe"
  }
}
"""


def _git(repo: Path, *args: str) -> None:
    """Run git in the fixture repo, hermetically (no signing, no editor, no hooks)."""
    subprocess.run(
        ["git", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false",
         "-c", "core.hooksPath=/dev/null", *args],
        cwd=repo, check=True, capture_output=True,
        env={**os.environ, "GIT_EDITOR": "true", "EDITOR": "true"},
    )


@pytest.fixture
def probe_module(tmp_path) -> Path:
    """A local `file://` git module declaring one importable juju resource."""
    repo = tmp_path / "probe-module"
    repo.mkdir()
    (repo / "main.tf").write_text(PROBE_MODULE)
    _git(repo, "init", "-q", "-b", "main")
    _git(repo, "config", "user.email", "test@example.invalid")
    _git(repo, "config", "user.name", "Test")
    _git(repo, "add", "-A")
    _git(repo, "commit", "-q", "-m", "probe")
    return repo


@pytest.mark.cloud
def test_import_refuses_a_configuration_targeting_another_model(
    juju: jubilant.Juju, tmp_path, probe_module: Path
):
    """The model-mismatch guard, which is the one that prevents mass destruction.

    Both models are empty here: the point is not what runs, but that the
    configuration names a model the query did not enumerate. Reaching the plan at
    all is what the check is for, so the config targets a real second model
    rather than a fake UUID a provider read might reject first.
    """
    # GIVEN the model the live objects come from
    live_uuid = juju.show_model(juju.model).model_uuid

    # AND a different, real model to point the configuration at
    with jubilant.temp_model() as other:
        other_uuid = other.show_model(other.model).model_uuid
        assert other_uuid != live_uuid

        # WHEN importing the live model with a configuration that targets another
        reported = atelier(
            f"import juju --source file://{probe_module} --ref main --dir wrapper"
            f" --var model_uuid={other_uuid}"
            f" --query-var model_uuid={live_uuid} --yes",
            cwd=tmp_path, check=False,
        )

    # THEN it is refused, naming the mismatch
    assert reported.returncode != 0, (
        "importing a configuration that targets another model must fail\n"
        f"{reported.stdout}"
    )
    assert "model mismatch" in reported.stderr, reported.stderr

    # AND no state was written — nothing was imported
    assert not (tmp_path / "wrapper" / "terraform.tfstate").exists(), (
        "a refused import must not write state"
    )
