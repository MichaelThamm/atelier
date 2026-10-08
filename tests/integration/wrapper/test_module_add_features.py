# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Feature tests for ``atelier add`` against a real upstream module.

These exercise the CLI surface — sub-module selection, ref pinning, presets
(`.tfvars` bundles), block naming, listing/removal, duplicate refusal, and
non-interactive exit — by inspecting the wrapper Atelier writes.

The target is canonical/prometheus-k8s-operator, whose Terraform lives in the
``terraform/`` sub-directory.
"""

import json
import re
import shutil
import subprocess
from pathlib import Path

import pytest

from helpers import TfDirManager, atelier

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

# The bundle both bundle-driven tests configure from, written as the HCL a user
# would write rather than rendered from a dict: `--var-file` takes a Terraform
# file, and a dict here would only be a second way to spell it.
CI_VALUES = """\
model_uuid = "00000000-0000-0000-0000-000000000000"
channel    = "dev/edge"
"""


def _main_tf(base) -> str:
    return (base / WRAPPER_DIR / "main.tf").read_text()


def _source(main_tf: str) -> str:
    match = re.search(r'source\s*=\s*"([^"]+)"', main_tf)
    assert match, f"no source attribute in main.tf:\n{main_tf}"
    return match.group(1)


def test_add_writes_module_block_with_subdir_source(tmp_path):
    # WHEN a module is added from a repository whose Terraform is in a subdir
    added = json.loads(atelier(f"add {PROM_REPO} --module {PROM_MODULE}"
                               f" --dir {WRAPPER_DIR} --strict --yes --json",
                               cwd=tmp_path).stdout)["data"]
    # THEN the block is named after the repo, at //terraform
    main_tf = _main_tf(tmp_path)
    assert re.search(rf'module\s+"{PROM_BLOCK}"', main_tf), main_tf
    assert _source(main_tf) == PROM_SOURCE
    # AND the payload reports where the wrapper went, so the test need not guess
    assert Path(added["wrapper"]) == tmp_path / WRAPPER_DIR
    assert added["added"]["name"] == PROM_BLOCK


def test_ref_is_pinned_in_the_source(tmp_path):
    # WHEN the module is added at a specific tag
    added = json.loads(atelier(f"add {PROM_REPO} --module {PROM_MODULE} --ref {PROM_REF}"
                               f" --dir {WRAPPER_DIR} --strict --yes --json",
                               cwd=tmp_path).stdout)["data"]
    # THEN the git source pins that ref
    assert _source(_main_tf(tmp_path)) == f"{PROM_SOURCE}?ref={PROM_REF}"
    assert added["added"]["ref"] == PROM_REF


def test_var_file_applies_typed_values(tmp_path):
    # GIVEN a bundle with scalars and a map-typed value
    (tmp_path / "ci.tfvars").write_text(
        'model_uuid = "00000000-0000-0000-0000-000000000001"\n'
        'channel    = "dev/edge"\n'
        'app_name   = "prom"\n'
        "units      = 3\n"
        'config     = { scrape = "true" }\n'
    )

    # WHEN the module is configured from it
    atelier(f"add {PROM_REPO} --module {PROM_MODULE} --dir {WRAPPER_DIR}"
            " --var-file ci.tfvars --strict --yes --json",
            cwd=tmp_path)

    # THEN the values are written as typed HCL arguments
    main_tf = _main_tf(tmp_path)
    assert re.search(r'model_uuid\s*=\s*"00000000-0000-0000-0000-000000000001"', main_tf)
    assert re.search(r'app_name\s*=\s*"prom"', main_tf)
    assert re.search(r'units\s*=\s*3', main_tf)
    assert re.search(r'channel\s*=\s*"dev/edge"', main_tf)
    assert re.search(r'scrape\s*=\s*"true"', main_tf), main_tf


def test_as_names_the_module_block(tmp_path):
    # WHEN an explicit block name is given
    added = json.loads(atelier(f"add {PROM_REPO} --module {PROM_MODULE} --as prom"
                               f" --dir {WRAPPER_DIR} --strict --yes --json",
                               cwd=tmp_path).stdout)["data"]
    # THEN exactly one module block uses it, and the candidate-derived name is
    # gone. A rename that left the derived block behind would declare the module
    # twice and fail later at apply, on colliding resource names.
    main_tf = _main_tf(tmp_path)
    assert re.search(r'module\s+"prom"', main_tf), main_tf
    assert len(re.findall(r'^module\s+"', main_tf, re.M)) == 1, main_tf
    assert not re.search(rf'module\s+"{PROM_BLOCK}"', main_tf), main_tf
    # the payload reports the name that reached main.tf, not the one requested
    assert added["added"]["name"] == "prom"


def test_module_list_reports_the_block(tmp_path):
    # GIVEN a named, ref-pinned module
    atelier(f"add {PROM_REPO} --module {PROM_MODULE} --as prom --ref {PROM_REF}"
            f" --dir {WRAPPER_DIR} --strict --yes --json",
            cwd=tmp_path)
    # WHEN listing the wrapper
    listed = json.loads(atelier("ls --json", cwd=tmp_path / WRAPPER_DIR).stdout)["data"]["modules"]

    # THEN the module, its source and its ref are reported
    assert [m["name"] for m in listed] == ["prom"]
    assert listed[0]["source"] == "https://github.com/canonical/prometheus-k8s-operator.git"
    assert listed[0]["ref"] == PROM_REF
    # The payload carries the subdirectory, which the human report drops.
    assert listed[0]["modulePath"] == PROM_MODULE


def test_apply_as_names_the_module_block(tmp_path):
    # GIVEN the apply one-liner with a named target, against a module whose
    # required variables no --var supplies. stdin is /dev/null, so there is no
    # prompt and no terminal needed.
    with pytest.raises(subprocess.CalledProcessError):
        # Exits non-zero on purpose: the required variables are unset, so it
        # writes the wrapper and stops before applying.
        atelier(f"apply {PROM_REPO} --module {PROM_MODULE} --as prom"
                " --dir . --strict", cwd=tmp_path)

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
    def add() -> None:
        atelier(f"add {PROM_REPO} --module {PROM_MODULE} --dir {WRAPPER_DIR}"
                " --strict --yes --json", cwd=tmp_path)

    add()

    # WHEN adding the same module at the same ref again, into the same wrapper.
    # THEN it is refused rather than declaring a second copy.
    with pytest.raises(subprocess.CalledProcessError):
        add()

    # AND the wrapper still declares exactly one module
    assert len(re.findall(r'^module\s+"', _main_tf(tmp_path), re.M)) == 1


def test_wrapper_initialises_and_validates(tmp_path):
    # GIVEN a wrapper Atelier authored from a bundle
    (tmp_path / "ci.tfvars").write_text(CI_VALUES)
    atelier(f"add {PROM_REPO} --module {PROM_MODULE} --dir {WRAPPER_DIR}"
            " --var-file ci.tfvars --strict --yes --json",
            cwd=tmp_path)
    # WHEN Terraform initialises it (fetching the module and provider)
    tf = TfDirManager()
    tf.latch(tmp_path / WRAPPER_DIR)
    tf.init()

    # THEN the configuration is valid — the wrapper is deployable
    tf.validate()


def test_wrapper_runs_without_atelier_state(tmp_path):
    # GIVEN a wrapper Atelier authored
    (tmp_path / "ci.tfvars").write_text(CI_VALUES)
    atelier(f"add {PROM_REPO} --module {PROM_MODULE} --dir {WRAPPER_DIR}"
            " --var-file ci.tfvars --strict --yes --json",
            cwd=tmp_path)
    wrapper = tmp_path / WRAPPER_DIR

    # AND Atelier left its internal state, which is not the artifact (ADR-0001)
    assert (wrapper / ".atelier").is_dir()

    # AND nothing in the wrapper refers to it, so nothing depends on it at run time
    for name in ("main.tf", "versions.tf", "providers.tf"):
        path = wrapper / name
        if path.exists():
            assert ".atelier" not in path.read_text(), f"{name} references .atelier"

    # WHEN the user deletes it — cleanup, or a checkout without the ignored tree
    shutil.rmtree(wrapper / ".atelier")

    # THEN Terraform alone still initialises and validates the wrapper: the
    # wrapper is a normal Terraform root, and Atelier is not needed to run it.
    tf = TfDirManager()
    tf.latch(wrapper)
    tf.init()
    tf.validate()