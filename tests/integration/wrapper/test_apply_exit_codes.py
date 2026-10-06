# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""`atelier apply` says which side failed, so a CI job need not read stderr.

A deploy can fail because Atelier declined to write the wrapper, or because the
wrapper was fine and Terraform failed. Both used to exit 1, which left a job
unable to tell "fix your wrapper" from "look at the infrastructure".

Both fixtures are real local git repositories, because the distinction under test
only exists once the clone succeeds: a source that is not a repository fails
earlier, in Atelier, and is exit 1 by design.

See ADR-0051.
"""

import re
import subprocess

import pytest

from helpers import atelier

REPO_V1 = "v1.0.0"

MODULE = """variable "model_uuid" {
  type = string
}
resource "terraform_data" "model_uuid" {
  input = var.model_uuid
}
"""

# Names a provider that does not exist, so `terraform init` fails — after Atelier
# has already written the wrapper, which is what makes it Terraform's failure.
UNINITIALISABLE = """variable "model_uuid" {
  type = string
}
resource "nonexistent_provider_thing" "x" {
  input = var.model_uuid
}
"""


def git(repo, *args):
    subprocess.run(
        ["git", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", *args],
        cwd=repo, check=True, capture_output=True,
    )


def make_repo(directory, source: str) -> str:
    """Write a one-module git repository and return its file:// source."""
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "main.tf").write_text(source)
    git(directory, "init", "-q", "-b", "main")
    git(directory, "config", "user.email", "t@example.invalid")
    git(directory, "config", "user.name", "T")
    git(directory, "add", "-A")
    git(directory, "commit", "-q", "-m", "v1")
    git(directory, "tag", REPO_V1)
    return f"file://{directory}"


def module_blocks(main_tf: str) -> list[str]:
    return re.findall(r'^module\s+"([^"]+)"', main_tf, re.M)


@pytest.fixture
def good_module(tmp_path) -> str:
    return make_repo(tmp_path / "cos-lite", MODULE)


@pytest.fixture
def broken_module(tmp_path) -> str:
    return make_repo(tmp_path / "broken", UNINITIALISABLE)


def test_apply_exits_zero_when_the_deployment_succeeds(tmp_path, good_module):
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir()
    done = atelier(f"apply {good_module} --ref {REPO_V1} --dir wrapper"
                   " --strict --var model_uuid=u1", cwd=tmp_path)
    assert done.returncode == 0, done.stderr
    assert (wrapper / "terraform.tfstate").exists(), "a successful apply wrote no state"


def test_apply_exits_two_when_terraform_fails(tmp_path, broken_module):
    # WHEN the module's provider does not exist, so the clone succeeds and the
    # wrapper is written, then `terraform init` fails
    done = atelier(f"apply {broken_module} --ref {REPO_V1} --dir wrapper"
                   " --strict --var model_uuid=u1", cwd=tmp_path, check=False)
    # THEN the exit code says the failure was Terraform's, not Atelier's, so a job
    # knows the wrapper is fine and the deployment is what broke.
    assert done.returncode == 2, f"exit={done.returncode}\n{done.stderr}"
    # AND Atelier did get as far as writing the wrapper, which is what makes the
    # distinction worth having.
    assert module_blocks((tmp_path / "wrapper" / "main.tf").read_text()) == ["broken"]


def test_apply_exits_one_when_atelier_refuses(tmp_path, good_module):
    # GIVEN a wrapper holding two blocks of one module, which `apply` refuses.
    # Deployed first so the directory is a wrapper Atelier wrote, then a second
    # block appended by hand — a hand-written main.tf on its own is a
    # preflight question, which is a different refusal.
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir()
    atelier(f"apply {good_module} --ref {REPO_V1} --dir wrapper --strict"
            " --var model_uuid=u1", cwd=tmp_path)
    with (wrapper / "main.tf").open("a") as fh:
        fh.write(f'\nmodule "cos_lite_2" {{\n  source = "{good_module}?ref={REPO_V1}"\n}}\n')

    # WHEN apply declines to guess which block to write
    done = atelier(f"apply {good_module} --ref {REPO_V1} --dir wrapper"
                   " --strict --var model_uuid=u2", cwd=tmp_path, check=False)
    # THEN it exits 1 — Atelier's — tellable apart from a failed deployment
    # without reading the message.
    assert done.returncode == 1, f"exit={done.returncode}\n{done.stderr}"
    assert "more than once" in done.stderr, done.stderr


def test_apply_exits_one_when_a_clone_fails(tmp_path, tmp_path_factory):
    # A source that is not a git repository fails before any wrapper is written,
    # so it is Atelier's failure and not Terraform's — the boundary the taxonomy
    # draws, and the one a fixture bug would silently cross.
    not_a_repo = tmp_path / "plain"
    not_a_repo.mkdir()
    (not_a_repo / "main.tf").write_text(MODULE)

    done = atelier(f"apply file://{not_a_repo} --dir wrapper --strict"
                   " --var model_uuid=u1", cwd=tmp_path, check=False)
    assert done.returncode == 1, f"exit={done.returncode}\n{done.stderr}"
    assert not (tmp_path / "wrapper" / "main.tf").exists()


def test_apply_exits_one_when_a_required_input_is_missing(tmp_path, good_module):
    # A gate Atelier owns: it stops before Terraform, having written the wrapper.
    done = atelier(f"apply {good_module} --ref {REPO_V1} --dir wrapper --strict",
                    cwd=tmp_path, check=False)

    assert done.returncode == 1, f"exit={done.returncode}\n{done.stderr}"
    assert "model_uuid" in done.stderr, done.stderr
