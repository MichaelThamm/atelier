# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Feature tests for ``atelier add`` against a real upstream module.

These exercise the CLI surface — sub-module selection, ref pinning, presets
(`.tfvars` bundles), block naming, listing/removal, duplicate refusal, and
non-interactive exit — by inspecting the wrapper Atelier writes.

The target is canonical/prometheus-k8s-operator, whose Terraform lives in the
``terraform/`` sub-directory.
"""

import re
import subprocess

import pytest

from helpers import TfDirManager, run_atelier, write_var_file

PROM_REPO = "https://github.com/canonical/prometheus-k8s-operator.git"
PROM_MODULE = "terraform"
PROM_REF = "main"
# `terraform` is a generic directory name, so Atelier names the block after the
# repository instead (see internal/bootstrap.ModuleBlockName).
PROM_BLOCK = "prometheus_k8s_operator"

# `module add` creates a directory of its own unless the current directory is
# already a wrapper, so the tests name it with --dir to keep it predictable.
WRAPPER_DIR = "wrapper"
PROM_SOURCE = "git::https://github.com/canonical/prometheus-k8s-operator.git//terraform"

DEFAULT_VALUES = {
    "model_uuid": "00000000-0000-0000-0000-000000000000",
    "channel": "dev/edge",
}


def _add(wrapper_dir, atelier_bin, *extra: str, check: bool = True):
    """Run ``atelier add`` for the prometheus module and return the process.

    ``--dir`` names the wrapper directory Atelier creates, so the caller knows
    where to find ``main.tf`` regardless of the candidate-derived name.
    """
    return run_atelier(
        wrapper_dir,
        atelier_bin,
        "add",
        PROM_REPO,
        "--module",
        PROM_MODULE,
        "--dir",
        WRAPPER_DIR,
        "--yes",
        *extra,
        capture=True,
        check=check,
    )


def _wrapper(wrapper_dir):
    """The directory Atelier wrote the wrapper into."""
    return wrapper_dir / WRAPPER_DIR


def _main_tf(wrapper_dir) -> str:
    return (_wrapper(wrapper_dir) / "main.tf").read_text()


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


def test_var_file_applies_typed_values(tmp_path, atelier_bin):
    # GIVEN a bundle with scalars and a map-typed value
    var_file = write_var_file(
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
    _add(tmp_path, atelier_bin, "--var-file", var_file)

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

    # THEN exactly one module block uses it, and the candidate-derived name is
    # gone. A rename that left the derived block behind would declare the module
    # twice and fail later at apply, on colliding resource names.
    main_tf = _main_tf(tmp_path)
    assert re.search(r'module\s+"prom"', main_tf), main_tf
    assert len(re.findall(r'^module\s+"', main_tf, re.M)) == 1, main_tf
    assert not re.search(rf'module\s+"{PROM_BLOCK}"', main_tf), main_tf


def test_module_list_and_rm(tmp_path, atelier_bin):
    # GIVEN a named, ref-pinned module
    _add(tmp_path, atelier_bin, "--as", "prom", "--ref", PROM_REF)

    # WHEN listing the wrapper
    listed = run_atelier(_wrapper(tmp_path), atelier_bin, "list", capture=True).stdout

    # THEN the module, its source and its ref are shown
    assert "prom" in listed
    assert "prometheus-k8s-operator" in listed
    assert PROM_REF in listed

    # WHEN removing it
    run_atelier(_wrapper(tmp_path), atelier_bin, "rm", "prom", "--force", capture=True)

    # THEN its block is gone
    assert not re.search(r'module\s+"prom"', _main_tf(tmp_path))


def test_apply_as_names_the_module_block_and_dir(tmp_path, atelier_bin):
    # GIVEN the apply one-liner against a module with required variables that
    # no --var supplies, so it writes the wrapper and stops before applying
    # (non-interactive stdin, no terminal needed).
    # WHEN --as names the block and the target directory
    run_atelier(
        tmp_path,
        atelier_bin,
        "apply",
        PROM_REPO,
        "--module",
        PROM_MODULE,
        "--as",
        "prom",
        capture=True,
        # Exits non-zero on purpose: the module's required variables are unset,
        # so it writes the wrapper and stops before applying.
        check=False,
    )

    # THEN the wrapper lives in a directory named after --as, and main.tf
    # declares exactly one module block under that name. Re-writing the state
    # under the new name would leave the candidate-derived block behind and
    # declare the module twice — the bug this guards.
    main_tf = (tmp_path / "prom" / "main.tf").read_text()
    assert re.search(r'module\s+"prom"', main_tf), main_tf
    assert len(re.findall(r'^module\s+"', main_tf, re.M)) == 1, main_tf
    assert not re.search(rf'module\s+"{PROM_BLOCK}"', main_tf), main_tf


def test_duplicate_add_is_refused(tmp_path, atelier_bin):
    # GIVEN the module is already present
    _add(tmp_path, atelier_bin)

    # WHEN adding the same module at the same ref again, into the same wrapper.
    # THEN it is refused rather than declaring a second copy. (Running in the
    # wrapper directory is the additive path: the existing wrapper's CWD.)
    with pytest.raises(subprocess.CalledProcessError):
        run_atelier(
            _wrapper(tmp_path),
            atelier_bin,
            "add",
            PROM_REPO,
            "--module",
            PROM_MODULE,
            "--yes",
            capture=True,
        )

    # AND the wrapper still declares exactly one module
    assert len(re.findall(r'^module\s+"', _main_tf(tmp_path), re.M)) == 1


def test_wrapper_initialises_and_validates(tmp_path, atelier_bin):
    # GIVEN a wrapper Atelier authored from a bundle
    var_file = write_var_file(tmp_path, "ci", DEFAULT_VALUES)
    _add(tmp_path, atelier_bin, "--var-file", var_file)

    # WHEN Terraform initialises it (fetching the module and provider)
    tf = TfDirManager(tmp_path)
    tf.latch(_wrapper(tmp_path))
    tf.init()

    # THEN the configuration is valid — the wrapper is deployable
    tf.validate()
