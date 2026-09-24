# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Feature tests for ``atelier module add`` against a real upstream module.

These exercise the CLI surface — sub-module selection, ref pinning, presets,
block naming, listing/removal, duplicate refusal, and non-interactive exit — by
inspecting the wrapper Atelier writes.

The target is canonical/prometheus-k8s-operator, whose Terraform lives in the
``terraform/`` sub-directory.
"""

import re
import subprocess

import pytest

from helpers import TfDirManager, run_atelier, write_local_preset

PROM_REPO = "https://github.com/canonical/prometheus-k8s-operator.git"
PROM_MODULE = "terraform"
PROM_REF = "tf-3.11.3"
# `terraform` is a generic directory name, so Atelier names the block after the
# repository instead (see internal/bootstrap.ModuleBlockName).
PROM_BLOCK = "prometheus_k8s_operator"
PROM_SOURCE = "git::https://github.com/canonical/prometheus-k8s-operator.git//terraform"

DEFAULT_PRESET = {
    "model_uuid": "00000000-0000-0000-0000-000000000000",
    "channel": "dev/edge",
}


def _add(wrapper_dir, atelier_bin, *extra: str, check: bool = True):
    """Run ``atelier module add`` for the prometheus module and return the process."""
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


def _main_tf(wrapper_dir) -> str:
    return (wrapper_dir / "main.tf").read_text()


def _source(main_tf: str) -> str:
    match = re.search(r'source\s*=\s*"([^"]+)"', main_tf)
    assert match, f"no source attribute in main.tf:\n{main_tf}"
    return match.group(1)


def test_add_writes_module_block_with_subdir_source(tmp_path, atelier_bin):
    # WHEN a module is added from a repository whose Terraform is in a subdir
    _add(tmp_path, atelier_bin)

    # THEN main.tf declares the module, named after the repo, at //terraform
    main_tf = _main_tf(tmp_path)
    assert re.search(rf'module\s+"{PROM_BLOCK}"', main_tf), main_tf
    assert _source(main_tf) == PROM_SOURCE


def test_ref_is_pinned_in_the_source(tmp_path, atelier_bin):
    # WHEN the module is added at a specific tag
    _add(tmp_path, atelier_bin, "--ref", PROM_REF)

    # THEN the git source pins that ref
    assert _source(_main_tf(tmp_path)) == f"{PROM_SOURCE}?ref={PROM_REF}"


def test_preset_applies_typed_values(tmp_path, atelier_bin):
    # GIVEN a preset with scalars and a map-typed value
    preset = write_local_preset(
        tmp_path,
        "ci",
        {
            "model_uuid": "00000000-0000-0000-0000-000000000001",
            "channel": "dev/edge",
            "app_name": "prom",
            "units": 3,
            "config": {"scrape": "true"},
        },
    )

    # WHEN the module is configured from it
    _add(tmp_path, atelier_bin, "--preset", preset)

    # THEN the values are written as typed HCL arguments
    main_tf = _main_tf(tmp_path)
    assert re.search(r'model_uuid\s*=\s*"00000000-0000-0000-0000-000000000001"', main_tf)
    assert re.search(r'app_name\s*=\s*"prom"', main_tf)
    assert re.search(r'units\s*=\s*3', main_tf)
    assert re.search(r'channel\s*=\s*"dev/edge"', main_tf)
    assert re.search(r'scrape\s*=\s*"true"', main_tf), main_tf


def test_as_names_the_module_block(tmp_path, atelier_bin):
    # WHEN an explicit block name is given
    _add(tmp_path, atelier_bin, "--as", "prom")

    # THEN the block uses it
    assert re.search(r'module\s+"prom"', _main_tf(tmp_path))


def test_module_list_and_rm(tmp_path, atelier_bin):
    # GIVEN a named, ref-pinned module
    _add(tmp_path, atelier_bin, "--as", "prom", "--ref", PROM_REF)

    # WHEN listing the wrapper
    listed = run_atelier(tmp_path, atelier_bin, "module", "list", capture=True).stdout

    # THEN the module, its source and its ref are shown
    assert "prom" in listed
    assert "prometheus-k8s-operator" in listed
    assert PROM_REF in listed

    # WHEN removing it
    run_atelier(tmp_path, atelier_bin, "module", "rm", "prom", "--force", capture=True)

    # THEN its block is gone
    assert not re.search(r'module\s+"prom"', _main_tf(tmp_path))


def test_duplicate_add_is_refused(tmp_path, atelier_bin):
    # GIVEN the module is already present
    _add(tmp_path, atelier_bin)

    # WHEN adding the same module at the same ref again
    # THEN it is refused rather than declaring a second copy
    with pytest.raises(subprocess.CalledProcessError):
        _add(tmp_path, atelier_bin)

    # AND the wrapper still declares exactly one module
    assert len(re.findall(r'^module\s+"', _main_tf(tmp_path), re.M)) == 1


def test_wrapper_initialises_and_validates(tmp_path, atelier_bin):
    # GIVEN a wrapper Atelier authored from a preset
    preset = write_local_preset(tmp_path, "ci", DEFAULT_PRESET)
    _add(tmp_path, atelier_bin, "--preset", preset)

    # WHEN Terraform initialises it (fetching the module and provider)
    tf = TfDirManager(tmp_path)
    tf.latch(tmp_path)
    tf.init()

    # THEN the configuration is valid — the wrapper is deployable
    tf.validate()
