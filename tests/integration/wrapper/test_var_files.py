# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""`--var-file` bundle discovery: a walk-up `atelier.presets/`.

Presets are `.tfvars` bundles consumed by `--var-file` (and the TUI `F` picker).
This exercises resolution by name against a shared walk-up directory rather than
a path. Classic wrapper shape; runs in the fast tier (no Juju model).
"""

from helpers import atelier

PROM_REPO = "https://github.com/canonical/prometheus-k8s-operator.git"
PROM_MODULE = "terraform"


def test_walk_up_preset_bundle_resolves_by_name(tmp_path):
    # GIVEN a shared atelier.presets/ directory above the wrapper
    preset_dir = tmp_path / "atelier.presets"
    preset_dir.mkdir()
    (preset_dir / "ci.tfvars").write_text(
        'model_uuid = "00000000-0000-0000-0000-000000000004"\nchannel    = "dev/edge"\n'
    )
    wrapper = tmp_path / "wrap"
    wrapper.mkdir()

    # WHEN the bundle is applied by name (walk-up, not a path)
    atelier(f"add {PROM_REPO} --module {PROM_MODULE} --dir ."
            " --var-file ci --strict --yes --json", cwd=wrapper)
    # THEN it is found and written as module arguments
    main_tf = (wrapper / "main.tf").read_text()
    assert "00000000-0000-0000-0000-000000000004" in main_tf
    assert "dev/edge" in main_tf
