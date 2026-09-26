# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""`--var-file` bundle discovery: walk-up `atelier.presets/` and repo examples.

Presets are `.tfvars` bundles consumed by `--var-file` (and the TUI `F`
picker). This exercises resolution by name against a shared walk-up directory
and the source-labelled `--list-var-files` listing. Classic wrapper shape; runs
in the fast tier (no Juju model).
"""

from helpers import run_atelier, write_var_file

PROM_REPO = "https://github.com/canonical/prometheus-k8s-operator.git"
PROM_MODULE = "terraform"


def test_walk_up_preset_bundle_resolves_by_name(tmp_path, atelier_bin):
    # GIVEN a shared atelier.presets/ directory above the wrapper
    preset_dir = tmp_path / "atelier.presets"
    preset_dir.mkdir()
    write_var_file(
        preset_dir,
        "ci",
        {"model_uuid": "00000000-0000-0000-0000-000000000004", "channel": "dev/edge"},
    )
    wrapper = tmp_path / "wrap"
    wrapper.mkdir()

    # WHEN the bundle is applied by name (walk-up, not a path)
    run_atelier(
        wrapper,
        atelier_bin,
        "module",
        "add",
        PROM_REPO,
        "--module",
        PROM_MODULE,
        "--var-file",
        "ci",
        "--yes",
    )

    # THEN it is found and written as module arguments
    main_tf = (wrapper / "main.tf").read_text()
    assert "00000000-0000-0000-0000-000000000004" in main_tf
    assert "dev/edge" in main_tf

    # AND it is listed, source-labelled, without writing
    listed = run_atelier(
        wrapper,
        atelier_bin,
        "module",
        "add",
        PROM_REPO,
        "--module",
        PROM_MODULE,
        "--list-var-files",
        capture=True,
    ).stdout
    assert "[local]" in listed and "ci" in listed, listed
