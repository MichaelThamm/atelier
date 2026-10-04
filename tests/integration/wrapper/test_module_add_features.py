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

from helpers import TfDirManager, atelier_add, atelier_apply, atelier_ls, write_tfvars

PROM_REPO = "https://github.com/canonical/prometheus-k8s-operator.git"
PROM_MODULE = "terraform"
PROM_REF = "main"
# `terraform` is a generic directory name, so Atelier names the block after the
# repository instead (see internal/bootstrap.ModuleBlockName).
PROM_BLOCK = "prometheus_k8s_operator"

# `atelier add` creates a directory of its own unless the target already holds a
# wrapper, so the tests name it to keep it predictable.
WRAPPER_DIR = "wrapper"
PROM_SOURCE = "git::https://github.com/canonical/prometheus-k8s-operator.git//terraform"

DEFAULT_VALUES = {
    "model_uuid": "00000000-0000-0000-0000-000000000000",
    "channel": "dev/edge",
}


def _main_tf(base) -> str:
    return (base / WRAPPER_DIR / "main.tf").read_text()


def _source(main_tf: str) -> str:
    match = re.search(r'source\s*=\s*"([^"]+)"', main_tf)
    assert match, f"no source attribute in main.tf:\n{main_tf}"
    return match.group(1)


def test_add_writes_module_block_with_subdir_source(tmp_path):
    # WHEN a module is added from a repository whose Terraform is in a subdir
    added = atelier_add(PROM_REPO, module=PROM_MODULE, dir=WRAPPER_DIR, cwd=tmp_path)

    # THEN the block is named after the repo, at //terraform
    main_tf = _main_tf(tmp_path)
    assert re.search(rf'module\s+"{PROM_BLOCK}"', main_tf), main_tf
    assert _source(main_tf) == PROM_SOURCE
    # AND the payload reports where the wrapper went, so the test need not guess
    assert added.wrapper == tmp_path / WRAPPER_DIR
    assert added.module.name == PROM_BLOCK


def test_ref_is_pinned_in_the_source(tmp_path):
    # WHEN the module is added at a specific tag
    added = atelier_add(PROM_REPO, module=PROM_MODULE, ref=PROM_REF, dir=WRAPPER_DIR, cwd=tmp_path)

    # THEN the git source pins that ref
    assert _source(_main_tf(tmp_path)) == f"{PROM_SOURCE}?ref={PROM_REF}"
    assert added.module.ref == PROM_REF


def test_var_file_applies_typed_values(tmp_path):
    # GIVEN a bundle with scalars and a map-typed value
    var_file = write_tfvars(
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
    atelier_add(
        PROM_REPO,
        module=PROM_MODULE,
        dir=WRAPPER_DIR,
        cwd=tmp_path,
        var_file=[str(var_file)],
    )

    # THEN the values are written as typed HCL arguments
    main_tf = _main_tf(tmp_path)
    assert re.search(r'model_uuid\s*=\s*"00000000-0000-0000-0000-000000000001"', main_tf)
    assert re.search(r'app_name\s*=\s*"prom"', main_tf)
    assert re.search(r'units\s*=\s*3', main_tf)
    assert re.search(r'channel\s*=\s*"dev/edge"', main_tf)
    assert re.search(r'scrape\s*=\s*"true"', main_tf), main_tf


def test_as_names_the_module_block(tmp_path):
    # WHEN an explicit block name is given
    added = atelier_add(PROM_REPO, module=PROM_MODULE, as_="prom", dir=WRAPPER_DIR, cwd=tmp_path)

    # THEN exactly one module block uses it, and the candidate-derived name is
    # gone. A rename that left the derived block behind would declare the module
    # twice and fail later at apply, on colliding resource names.
    main_tf = _main_tf(tmp_path)
    assert re.search(r'module\s+"prom"', main_tf), main_tf
    assert len(re.findall(r'^module\s+"', main_tf, re.M)) == 1, main_tf
    assert not re.search(rf'module\s+"{PROM_BLOCK}"', main_tf), main_tf
    # the payload reports the name that reached main.tf, not the one requested
    assert added.module.name == "prom"


def test_module_list_reports_the_block(tmp_path):
    # GIVEN a named, ref-pinned module
    atelier_add(
        PROM_REPO,
        module=PROM_MODULE,
        as_="prom",
        ref=PROM_REF,
        dir=WRAPPER_DIR,
        cwd=tmp_path,
    )

    # WHEN listing the wrapper
    listed = atelier_ls(cwd=tmp_path / WRAPPER_DIR)

    # THEN the module, its source and its ref are reported
    assert [m.name for m in listed] == ["prom"]
    assert listed[0].source == "https://github.com/canonical/prometheus-k8s-operator.git"
    assert listed[0].ref == PROM_REF
    # The payload carries the subdirectory, which the human report drops.
    assert listed[0].module_path == PROM_MODULE


def test_apply_as_names_the_module_block(tmp_path):
    # GIVEN the apply one-liner with a named target, against a module whose
    # required variables no --var supplies. stdin is /dev/null, so there is no
    # prompt and no terminal needed.
    with pytest.raises(subprocess.CalledProcessError):
        # Exits non-zero on purpose: the required variables are unset, so it
        # writes the wrapper and stops before applying.
        atelier_apply(PROM_REPO, module=PROM_MODULE, as_="prom", dir=".", cwd=tmp_path)

    # THEN the wrapper is the directory the caller prepared, and main.tf declares
    # exactly one module block under the requested name. Renaming into an existing
    # wrapper that kept the candidate-derived block would declare the module twice
    # and fail later at apply, on colliding resource names — the bug this guards.
    main_tf = (tmp_path / "main.tf").read_text()
    assert re.search(r'module\s+"prom"', main_tf), main_tf
    assert len(re.findall(r'^module\s+"', main_tf, re.M)) == 1, main_tf
    assert not re.search(rf'module\s+"{PROM_BLOCK}"', main_tf), main_tf


def test_duplicate_add_is_refused(tmp_path):
    # GIVEN the module is already present
    atelier_add(PROM_REPO, module=PROM_MODULE, dir=WRAPPER_DIR, cwd=tmp_path)

    # WHEN adding the same module at the same ref again, into the same wrapper.
    # THEN it is refused rather than declaring a second copy.
    with pytest.raises(subprocess.CalledProcessError):
        atelier_add(PROM_REPO, module=PROM_MODULE, dir=WRAPPER_DIR, cwd=tmp_path)

    # AND the wrapper still declares exactly one module
    assert len(re.findall(r'^module\s+"', _main_tf(tmp_path), re.M)) == 1


def test_wrapper_initialises_and_validates(tmp_path):
    # GIVEN a wrapper Atelier authored from a bundle
    var_file = write_tfvars(tmp_path, "ci", DEFAULT_VALUES)
    atelier_add(
        PROM_REPO,
        module=PROM_MODULE,
        dir=WRAPPER_DIR,
        cwd=tmp_path,
        var_file=[str(var_file)],
    )

    # WHEN Terraform initialises it (fetching the module and provider)
    tf = TfDirManager()
    tf.latch(tmp_path / WRAPPER_DIR)
    tf.init()

    # THEN the configuration is valid — the wrapper is deployable
    tf.validate()