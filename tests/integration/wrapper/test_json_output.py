# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""``--json`` output for the commands that report data.

The Go tests pin the payloads byte for byte. These prove the flag reaches a real
command and that stdout stays clean while the human report stays on stderr — the
properties a consumer of a CI log depends on, and the ones a unit test over a
buffer cannot show.
"""

import json
import subprocess
from pathlib import Path

from helpers import run_atelier

PROM_REPO = "https://github.com/canonical/prometheus-k8s-operator.git"
LOKI_REPO = "https://github.com/canonical/loki-operators.git"
PROM_MODULE = "terraform"
PROM_REF = "main"
# `terraform` is a generic directory name, so the block is named after the
# repository instead.
PROM_BLOCK = "prometheus_k8s_operator"


def _two_candidate_repo(base: Path) -> str:
    """Commit a local repository holding two candidate modules; return its path.

    A candidate is a directory declaring a `variable` with a type constraint, so
    each of these two is one. Local rather than a real multi-module repository,
    which keeps the case in the fast tier.
    """
    repo = base / "two-candidates"
    for name in ("alpha", "beta"):
        (repo / name).mkdir(parents=True)
        (repo / name / "main.tf").write_text('variable "token" {\n  type = string\n}\n')
    subprocess.run(["git", "init", "-q", "-b", "main"], cwd=repo, check=True)
    subprocess.run(["git", "add", "-A"], cwd=repo, check=True)
    # Identity from flags, not config: the repo this runs in may have none set.
    subprocess.run(["git", "-c", "user.name=t", "-c", "user.email=t@example.com",
                    "commit", "-qm", "two modules"], cwd=repo, check=True)
    return str(repo)


def _empty_dir(base: Path, name: str) -> Path:
    """Allocate an empty directory for Atelier to scaffold into."""
    path = base / name
    path.mkdir(parents=True, exist_ok=True)
    return path


def _json_atelier(cwd, *args: str) -> dict:
    """Run Atelier with ``--json`` and return the payload's ``data``.

    Reads the envelope rather than :func:`~helpers.payload`, because asserting on
    it is half the point: every command reports the same shape, and ``command``
    must name the one that ran (ADR-0048).
    """
    envelope = json.loads(run_atelier(*args, "--json", cwd=cwd).stdout)
    assert envelope["schema"] == 1
    assert envelope["command"] == args[0]
    return envelope["data"]


def _add_prom(base: Path, name: str, *extra: str) -> dict:
    """Add the prometheus module into a named wrapper and return the payload."""
    wrapper = _empty_dir(base, name)
    data = _json_atelier(
        # `--dir` is resolved against the CWD, so stay above the wrapper.
        base,
        "add",
        PROM_REPO,
        "--module",
        PROM_MODULE,
        "--ref",
        PROM_REF,
        "--dir",
        name,
        "--yes",
        *extra,
    )
    # The payload says which directory was written, so this is checked rather
    # than assumed from the arguments.
    assert data["wrapper"] == str(wrapper)
    return data


def test_add_json_reports_where_the_wrapper_went(tmp_path):
    # WHEN the module is added with --json
    data = _add_prom(tmp_path, "json-add")

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


def test_add_json_sanitises_an_explicit_block_name(tmp_path):
    # GIVEN a request for a name that is not a valid HCL identifier
    data = _add_prom(tmp_path, "json-as", "--as", "prom-k8s")

    # THEN the reported name is the one that reached main.tf, not the one asked
    # for. This is why `add --json` reads the wrapper back rather than echoing.
    assert data["added"]["name"] == "prom_k8s"
    assert data["blocks"] == ["prom_k8s"]


def test_add_json_composes_into_an_existing_wrapper(tmp_path):
    # GIVEN a wrapper with one module in it
    first = _add_prom(tmp_path, "json-compose")

    # WHEN a second module is added into the same wrapper
    second = _json_atelier(
        tmp_path,
        "add",
        LOKI_REPO,
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


def test_ls_json_matches_ls(tmp_path):
    # GIVEN a wrapper with a module in it
    wrapper = Path(_add_prom(tmp_path, "json-ls")["wrapper"])

    # WHEN the modules are listed both ways
    data = _json_atelier(wrapper, "ls")
    text = run_atelier("ls", cwd=wrapper).stdout

    # THEN the payload carries what the table prints, and the //subdir the table
    # drops
    assert data["isWrapper"] is True
    assert [m["name"] for m in data["modules"]] == [PROM_BLOCK]
    assert data["modules"][0]["ref"] == PROM_REF
    assert data["modules"][0]["modulePath"] == PROM_MODULE
    assert PROM_BLOCK in text


def test_ls_json_reports_a_directory_that_is_not_a_wrapper(tmp_path):
    # GIVEN an empty directory
    # WHEN it is listed with --json
    data = _json_atelier(_empty_dir(tmp_path, "json-empty"), "ls")

    # THEN it is distinguishable from a wrapper that declares no modules, which
    # the text report also distinguishes but only in prose
    assert data["isWrapper"] is False
    assert data["modules"] == []


def test_ls_json_of_a_wrapper_with_no_modules(tmp_path):
    # GIVEN a directory holding a main.tf that declares nothing
    wrapper = _empty_dir(tmp_path, "json-no-modules")
    (wrapper / "main.tf").write_text("# nothing here\n")

    # WHEN it is listed with --json
    data = _json_atelier(wrapper, "ls")

    # THEN the two "nothing to report" cases stay distinguishable
    assert data["isWrapper"] is True
    assert data["modules"] == []


def test_wrappers_json_lists_absolute_paths(tmp_path):
    # GIVEN a wrapper beside a directory that is not one
    wrapper = Path(_add_prom(tmp_path, "json-parent")["wrapper"])
    plain = _empty_dir(tmp_path, "json-not-a-wrapper")

    # WHEN the shared parent is scanned with --json
    data = _json_atelier(tmp_path, "wrappers", ".")

    # THEN each wrapper is reported with a path a caller can use, not a basename,
    # and a plain sibling directory is left out
    paths = [w["path"] for w in data["wrappers"]]
    assert str(wrapper) in paths
    assert str(plain) not in paths
    found = next(w for w in data["wrappers"] if w["path"] == str(wrapper))
    assert found["name"] == wrapper.name
    assert found["modules"] == [PROM_BLOCK]


def test_list_var_files_json(tmp_path):
    # WHEN the bundles a module can be configured from are listed with --json
    data = _json_atelier(
        _empty_dir(tmp_path, "json-vars"),
        "add",
        LOKI_REPO,
        "--module",
        PROM_MODULE,
        "--list-var-files",
    )

    # THEN each one carries the name to pass to --var-file
    assert data["bundles"], "the loki module ships presets"
    for bundle in data["bundles"]:
        assert bundle["name"]
        assert bundle["source"] in ("local", "repo", "gallery")


def test_json_does_not_suppress_the_human_report(tmp_path):
    # GIVEN a wrapper with one module in it
    _add_prom(tmp_path, "json-streams")

    # WHEN a second module is added with --json
    result = run_atelier(
        "add",
        LOKI_REPO,
        "--module",
        PROM_MODULE,
        "--as",
        "loki",
        "--dir",
        "json-streams",
        "--yes",
        "--json",
        cwd=tmp_path,
    )

    # THEN stdout is exactly the payload, and the human report is still on
    # stderr, so a run that went wrong still says why
    assert json.loads(result.stdout)["data"]["added"]["name"] == "loki"
    assert "Added module" in result.stderr
    assert "Added module" not in result.stdout


def test_apply_rejects_json(tmp_path):
    # GIVEN an empty directory for apply to target
    target = _empty_dir(tmp_path, "json-apply")

    # WHEN --json is passed to a command that has no report of its own
    result = run_atelier(
        "apply",
        PROM_REPO,
        "--module",
        PROM_MODULE,
        "--dir",
        target.name,
        "--json",
        cwd=tmp_path,
        check=False,
    )

    # THEN it is a loud error, not a silent no-op a CI job would read as success
    assert result.returncode == 1
    assert "--json is not supported" in result.stderr
    assert result.stdout == ""

def test_add_json_reports_an_ambiguous_source_as_a_failure(tmp_path):
    # GIVEN a source that matches more than one module
    repo = _two_candidate_repo(tmp_path)
    wrapper = _empty_dir(tmp_path, "json-ambiguous")

    # WHEN it is added with --json and no --module to disambiguate
    result = run_atelier(
        "add",
        repo,
        "--dir",
        wrapper.name,
        "--json",
        cwd=tmp_path,
        check=False,
    )

    # THEN stdout stays empty, so a consumer parsing it gets a clean failure
    # rather than prose, and the exit code says the run did not succeed
    assert result.stdout == "", result.stdout
    assert result.returncode == 1
    # AND the candidates it needed are still on stderr, where a person reads them
    assert "--module" in result.stderr
    assert "alpha" in result.stderr and "beta" in result.stderr
    assert not (wrapper / "main.tf").exists(), "an ambiguous source wrote a wrapper"
