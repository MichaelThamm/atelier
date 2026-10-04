# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""``--json`` output for the commands that report data.

The Go tests pin the payloads byte for byte. These prove the flag reaches a real
command and that stdout stays clean while the human report stays on stderr — the
properties a consumer of a CI log depends on, and the ones a unit test over a
buffer cannot show.
"""

import json
from pathlib import Path

from helpers import run_atelier

PROM_REPO = "https://github.com/canonical/prometheus-k8s-operator.git"
PROM_MODULE = "terraform"
PROM_REF = "main"
# `terraform` is a generic directory name, so the block is named after the
# repository instead.
PROM_BLOCK = "prometheus_k8s_operator"


def _empty_dir(tf_manager, name: str) -> Path:
    """Allocate an empty directory for Atelier to scaffold into."""
    path = Path(tf_manager.base) / name
    path.mkdir(parents=True, exist_ok=True)
    return path


def _add_prom(tf_manager, atelier_bin, name: str, *extra: str) -> dict:
    """Add the prometheus module into a named wrapper and return the payload."""
    wrapper = _empty_dir(tf_manager, name)
    result = run_atelier(
        tf_manager.base,  # `--dir` is resolved against the CWD, so stay above it
        atelier_bin,
        "add",
        PROM_REPO,
        "--module",
        PROM_MODULE,
        "--ref",
        PROM_REF,
        "--dir",
        name,
        "--yes",
        "--json",
        *extra,
        capture=True,
    )
    payload = json.loads(result.stdout)
    # The envelope is the same for every command; see ADR-0048.
    assert payload["schema"] == 1
    assert payload["command"] == "add"
    # The payload says which directory was written, so this is checked rather
    # than assumed from the arguments.
    assert payload["data"]["wrapper"] == str(wrapper)
    return payload["data"]


def _json_atelier(cwd, atelier_bin, *args: str) -> dict:
    """Run Atelier with ``--json`` and return the parsed payload's ``data``."""
    result = run_atelier(cwd, atelier_bin, *args, "--json", capture=True)
    payload = json.loads(result.stdout)
    assert payload["schema"] == 1
    assert payload["command"] == args[0]
    return payload["data"]


def test_add_json_reports_where_the_wrapper_went(tf_manager, atelier_bin):
    # WHEN the module is added with --json
    data = _add_prom(tf_manager, atelier_bin, "json-add")

    # THEN main.tf is where the payload said it would be
    assert (Path(data["wrapper"]) / "main.tf").exists()

    # AND the block is described as it reads back from main.tf
    added = data["added"]
    assert added["name"] == PROM_BLOCK
    assert added["modulePath"] == PROM_MODULE
    assert added["ref"] == PROM_REF
    # The address is decomposed into exactly the three values `add` takes, so
    # they can be handed straight back to a command: no //subdir left in source.
    assert added["source"] == PROM_REPO

    # AND every block in the wrapper is listed, so a composing caller needs no
    # second `ls`. The added block is the last one.
    assert data["blocks"] == [PROM_BLOCK]
    assert data["blocks"][-1] == added["name"]


def test_add_json_sanitises_an_explicit_block_name(tf_manager, atelier_bin):
    # GIVEN a request for a name that is not a valid HCL identifier
    data = _add_prom(tf_manager, atelier_bin, "json-as", "--as", "prom-k8s")

    # THEN the reported name is the one that reached main.tf, not the one asked
    # for. This is why `add --json` reads the wrapper back rather than echoing.
    assert data["added"]["name"] == "prom_k8s"
    assert data["blocks"] == ["prom_k8s"]


def test_add_json_composes_into_an_existing_wrapper(tf_manager, atelier_bin):
    # GIVEN a wrapper with one module in it
    first = _add_prom(tf_manager, atelier_bin, "json-compose")

    # WHEN a second module is added into the same wrapper
    second = _json_atelier(
        tf_manager.base,
        atelier_bin,
        "add",
        "https://github.com/canonical/loki-operators.git",
        "--module",
        PROM_MODULE,
        "--as",
        "loki",
        "--dir",
        "json-compose",
        "--yes",
    )

    # THEN the payload names the wrapper it composed into and every block in it
    assert second["wrapper"] == first["wrapper"]
    assert second["added"]["name"] == "loki"
    assert second["blocks"] == [PROM_BLOCK, "loki"]


def test_ls_json_matches_ls(tf_manager, atelier_bin):
    # GIVEN a wrapper with a module in it
    wrapper = Path(_add_prom(tf_manager, atelier_bin, "json-ls")["wrapper"])

    # WHEN the modules are listed both ways
    data = _json_atelier(wrapper, atelier_bin, "ls")
    text = run_atelier(wrapper, atelier_bin, "ls", capture=True).stdout

    # THEN the payload carries what the table prints, and the //subdir the table
    # drops
    assert data["isWrapper"] is True
    assert [m["name"] for m in data["modules"]] == [PROM_BLOCK]
    assert data["modules"][0]["ref"] == PROM_REF
    assert data["modules"][0]["modulePath"] == PROM_MODULE
    assert PROM_BLOCK in text


def test_ls_json_reports_a_directory_that_is_not_a_wrapper(tf_manager, atelier_bin):
    # GIVEN an empty directory
    # WHEN it is listed with --json
    data = _json_atelier(_empty_dir(tf_manager, "json-empty"), atelier_bin, "ls")

    # THEN it is distinguishable from a wrapper that declares no modules, which
    # the text report also distinguishes but only in prose
    assert data["isWrapper"] is False
    assert data["modules"] == []


def test_ls_json_of_a_wrapper_with_no_modules(tf_manager, atelier_bin):
    # GIVEN a directory holding a main.tf that declares nothing
    wrapper = _empty_dir(tf_manager, "json-no-modules")
    (wrapper / "main.tf").write_text("# nothing here\n")

    # WHEN it is listed with --json
    data = _json_atelier(wrapper, atelier_bin, "ls")

    # THEN the two "nothing to report" cases stay distinguishable
    assert data["isWrapper"] is True
    assert data["modules"] == []


def test_wrappers_json_lists_absolute_paths(tf_manager, atelier_bin):
    # GIVEN a wrapper beside a directory that is not one
    wrapper = Path(_add_prom(tf_manager, atelier_bin, "json-parent")["wrapper"])
    plain = _empty_dir(tf_manager, "json-not-a-wrapper")

    # WHEN the shared parent is scanned with --json
    data = _json_atelier(tf_manager.base, atelier_bin, "wrappers", ".")

    # THEN each wrapper is reported with a path a caller can use, not a basename,
    # and a plain sibling directory is left out
    paths = [w["path"] for w in data["wrappers"]]
    assert str(wrapper) in paths
    assert str(plain) not in paths
    found = next(w for w in data["wrappers"] if w["path"] == str(wrapper))
    assert found["name"] == wrapper.name
    assert found["modules"] == [PROM_BLOCK]


def test_list_var_files_json(tf_manager, atelier_bin):
    # WHEN the bundles a module can be configured from are listed with --json
    data = _json_atelier(
        _empty_dir(tf_manager, "json-vars"),
        atelier_bin,
        "add",
        "https://github.com/canonical/loki-operators.git",
        "--module",
        PROM_MODULE,
        "--list-var-files",
    )

    # THEN each one carries the name to pass to --var-file
    assert data["bundles"], "the loki module ships presets"
    for bundle in data["bundles"]:
        assert bundle["name"]
        assert bundle["source"] in ("local", "repo", "gallery")


def test_json_does_not_suppress_the_human_report(tf_manager, atelier_bin):
    # GIVEN a wrapper with one module in it
    _add_prom(tf_manager, atelier_bin, "json-streams")

    # WHEN a second module is added with --json
    result = run_atelier(
        tf_manager.base,
        atelier_bin,
        "add",
        "https://github.com/canonical/loki-operators.git",
        "--module",
        PROM_MODULE,
        "--as",
        "loki",
        "--dir",
        "json-streams",
        "--yes",
        "--json",
        capture=True,
    )

    # THEN stdout is exactly the payload, and the human report is still on
    # stderr, so a run that went wrong still says why
    assert json.loads(result.stdout)["data"]["added"]["name"] == "loki"
    assert "Added module" in result.stderr
    assert "Added module" not in result.stdout


def test_apply_rejects_json(tf_manager, atelier_bin):
    # WHEN --json is passed to a command that has no report of its own
    result = run_atelier(
        tf_manager.base,
        atelier_bin,
        "apply",
        PROM_REPO,
        "--module",
        PROM_MODULE,
        "--dir",
        _empty_dir(tf_manager, "json-apply").name,
        "--json",
        capture=True,
        check=False,
    )

    # THEN it is a loud error, not a silent no-op a CI job would read as success
    assert result.returncode == 1
    assert "--json is not supported" in result.stderr
    assert result.stdout == ""
