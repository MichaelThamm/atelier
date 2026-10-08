# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""`atelier import` must refuse a configuration that targets another model.

The round-trip test proves the happy path; this is the dangerous one it cannot
reach. `model_uuid` forces replacement, so importing a configuration that names
a different model than the live resources came from would make the next apply
destroy every resource it imported. The check runs between the plan and the
match, so nothing is written — which is what this pins.

No deployment is needed: the guard fires before any import, so an empty live
model and an empty target model are enough. The dry-run and repeat-import safety
checks live in ``test_import_cos_lite_roundtrip``, so they share its one deploy
instead of standing a second COS-Lite up.
"""

import jubilant
import pytest

from helpers import atelier

COS_REPO = "https://github.com/canonical/observability-stack.git"
COS_MODULE = "terraform/cos-lite"
COS_REF = "main"
# The module's own preset, resolved by name from the clone (upstream discovery).
COS_PRESET = "no-ingress"


@pytest.mark.cloud
def test_import_refuses_a_configuration_targeting_another_model(juju: jubilant.Juju, tmp_path):
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

        # AND a bundle whose model is that other model, not the live one
        (tmp_path / "wrong.tfvars").write_text(
            f'model = {{ uuid = "{other_uuid}", name = "{other.model}" }}\n'
            "internal_tls = false\n"
        )

        # WHEN importing the live model with a configuration that targets another
        reported = atelier(
            f"import juju --source {COS_REPO} --module {COS_MODULE} --ref {COS_REF} --dir wrapper"
            f" --var-file wrong.tfvars --var-file {COS_PRESET}"
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
