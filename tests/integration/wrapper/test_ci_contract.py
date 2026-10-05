# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""The two ways a CI job can be told a run went wrong, other than by reading prose.

Both are quiet by default, because from the outside both look like success: a
wrapper missing every value a bundle held still looks like a wrapper, and an
import that recovered nothing still exits 0. A job that trusts either proceeds.
"""

import subprocess

import pytest

from helpers import run_atelier, write_var_file


def local_module(base) -> str:
    """A one-variable module, so `add` needs neither network nor a provider."""
    module = base / "src" / "stack"
    module.mkdir(parents=True, exist_ok=True)
    (module / "main.tf").write_text(
        'variable "greeting" {\n  type = string\n}\n'
        'resource "terraform_data" "said" {\n  input = var.greeting\n}\n'
    )
    return str(module)


def test_an_unresolvable_var_file_is_fatal_under_strict(tmp_path, atelier_bin):
    # GIVEN a bundle written under one name
    write_var_file(tmp_path, "ci", {"greeting": "hello"})
    target = tmp_path / "wrapper"
    target.mkdir()

    # WHEN `add` is asked for it by a name that resolves to nothing, under --strict
    with pytest.raises(subprocess.CalledProcessError) as failure:
        run_atelier(tmp_path, atelier_bin, "add", local_module(tmp_path),
                    "--as", "stack", "--dir", "wrapper",
                    "--var-file", "no-such-bundle", "--strict", "--yes",
                    capture=True)

    # THEN the run fails, rather than writing a wrapper that silently holds none
    # of the values the bundle carried. Without this the omission surfaces later
    # as a missing required input — or not at all, if the module has a default.
    assert "no-such-bundle" in failure.value.stderr
    assert not (target / "main.tf").exists()


def test_an_unresolvable_var_file_is_only_a_warning_without_strict(tmp_path, atelier_bin):
    # GIVEN the same typo, without --strict
    write_var_file(tmp_path, "ci", {"greeting": "hello"})
    target = tmp_path / "wrapper"
    target.mkdir()

    # WHEN it is asked for by a name that resolves to nothing
    result = run_atelier(tmp_path, atelier_bin, "add", local_module(tmp_path),
                         "--as", "stack", "--dir", "wrapper",
                         "--var-file", "no-such-bundle", "--yes",
                         capture=True, check=False)

    # THEN the wrapper is still written and the bundle is reported as skipped.
    # A gallery entry's preset may be superseded by one the caller supplies, so
    # this case has to keep working — it is a judgement, not an error.
    assert result.returncode == 0
    assert "not found" in result.stderr
    assert (target / "main.tf").exists()