# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""`atelier module add --tfvars`: the pass-through wrapper and bundle discovery.

This is the feature's flagship path, so it is exercised end to end against a
real module and real Terraform: the generated ``variables.tf`` mirrors the
module, ``main.tf`` forwards every input, values land in ``terraform.tfvars``,
and ``terraform validate`` accepts the result. Runs in the fast wrapper tier
(no Juju model).
"""

import re

from helpers import TfDirManager, run_atelier, write_var_file

PROM_REPO = "https://github.com/canonical/prometheus-k8s-operator.git"
PROM_MODULE = "terraform"


def _add(wrapper_dir, atelier_bin, *extra, check=True):
    return run_atelier(
        wrapper_dir,
        atelier_bin,
        "module",
        "add",
        PROM_REPO,
        "--module",
        PROM_MODULE,
        "--yes",
        *extra,
        capture=True,
        check=check,
    )


def test_tfvars_mode_writes_a_valid_passthrough_wrapper(tmp_path, atelier_bin):
    # GIVEN a bundle with a scalar and a map-typed value
    var_file = write_var_file(
        tmp_path,
        "ci",
        {
            "model_uuid": "00000000-0000-0000-0000-000000000003",
            "channel": "dev/edge",
            "units": 3,
            "config": {"scrape": "true"},
        },
    )

    # WHEN the module is bootstrapped in pass-through mode from it
    _add(tmp_path, atelier_bin, "--tfvars", "--var-file", var_file)

    # THEN main.tf is a generated forwarding interface
    main_tf = (tmp_path / "main.tf").read_text()
    assert "atelier:tfvars" in main_tf
    assert re.search(r"model_uuid\s*=\s*var\.model_uuid", main_tf), main_tf
    assert re.search(r"config\s*=\s*var\.config", main_tf), main_tf

    # AND variables.tf mirrors the module's inputs
    variables_tf = (tmp_path / "variables.tf").read_text()
    assert re.search(r'variable\s+"model_uuid"', variables_tf), variables_tf
    assert re.search(r'variable\s+"channel"', variables_tf), variables_tf

    # AND the values live in terraform.tfvars
    tfvars = (tmp_path / "terraform.tfvars").read_text()
    assert "00000000-0000-0000-0000-000000000003" in tfvars
    assert "dev/edge" in tfvars
    assert re.search(r"units\s*=\s*3", tfvars), tfvars

    # AND Terraform accepts the generated wrapper
    tf = TfDirManager(tmp_path)
    tf.latch(tmp_path)
    tf.init()
    tf.validate()


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
    _add(wrapper, atelier_bin, "--tfvars", "--var-file", "ci")

    # THEN it is found and applied
    tfvars = (wrapper / "terraform.tfvars").read_text()
    assert "00000000-0000-0000-0000-000000000004" in tfvars

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
