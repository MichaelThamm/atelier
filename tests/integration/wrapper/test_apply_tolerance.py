# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Re-running `atelier apply` converges instead of duplicating.

Running the same command twice inside a wrapper used to either error ("already
references this module at the same ref") or, with a changed `--ref`, append a
second block. These tests pin what replaces both: an identical re-run is
idempotent, a `--ref` change re-points the existing block, and a `--var` merges
into the values already in that block rather than reverting them to defaults.

The module is a local `file://` git repository rather than a directory, because
a local path has no ref and re-pointing one is the behaviour under test. So the
real clone path (ls-remote, resolve, clone) is exercised, with no network.

Atelier is called through `atelier`, spelled out as a command line, because that
is how anything scripting Atelier has to call it — nobody can import from a test
directory.

See ADR-0050.
"""

import os
import re
import subprocess
from pathlib import Path

import pytest

from helpers import TfDirManager, atelier

REPO_V1 = "v1.0.0"
REPO_V2 = "v2.0.0"


def write_module(directory, name: str, variables: dict[str, str], optional: dict[str, str] | None = None) -> str:
    """Write a module and return its path.

    A candidate is a directory declaring a `variable` with a type constraint
    (internal/candidate), so each fixture has exactly one. `variables` are
    required strings; `optional` are strings with a default. Each input gets a
    `terraform_data` resource, which is what makes a deployment observable in
    state, and an output of the same name, so a sibling block can be wired to it.
    """
    directory.mkdir(parents=True, exist_ok=True)
    optional = optional or {}
    decls = "".join(
        f'variable "{var}" {{\n  type = string\n}}\n' for var in variables
    ) + "".join(
        f'variable "{var}" {{\n  type    = string\n  default = "{value}"\n}}\n'
        for var, value in optional.items()
    )
    body = "".join(
        f'resource "terraform_data" "{var}" {{\n  input = var.{var}\n}}\n'
        f'output "{var}" {{\n  value = var.{var}\n}}\n'
        for var in {**variables, **optional}
    )
    (directory / "main.tf").write_text(decls + body)
    return str(directory)


def git(repo, *args: str) -> None:
    """Run git in the fixture repo, hermetically.

    Signing and the editor are disabled explicitly: a developer whose global
    config sets commit.gpgsign or tag.gpgsign would otherwise be prompted for a
    passphrase mid-test, or hang on an editor.
    """
    subprocess.run(
        ["git", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false",
         "-c", "core.hooksPath=/dev/null", *args],
        cwd=repo, check=True, capture_output=True,
        env={**os.environ, "GIT_EDITOR": "true", "EDITOR": "true"},
    )


@pytest.fixture
def module_repo(tmp_path) -> Path:
    """A local git repo holding one module at two tagged revisions.

    v1 declares `model_uuid` and `units`. v2 drops `units` and adds `model`, so a
    re-point has something to carry over, something to drop, and a new required
    input to report. Named for the module so the derived block name is cos_lite,
    which the assertions below refer to.
    """
    repo = tmp_path / "cos-lite"
    # `region` has a default, so an argument set to that default is prunable —
    # which is what the sparse-rule reporting test needs to exercise.
    write_module(repo, "cos_lite", {"model_uuid": "", "units": ""}, {"region": "eu"})
    git(repo, "init", "-q", "-b", "main")
    git(repo, "config", "user.email", "test@example.invalid")
    git(repo, "config", "user.name", "Test")
    git(repo, "add", "-A")
    git(repo, "commit", "-q", "-m", "v1")
    git(repo, "tag", REPO_V1)

    write_module(repo, "cos_lite", {"model_uuid": "", "model": ""}, {"region": "eu"})
    git(repo, "add", "-A")
    git(repo, "commit", "-q", "-m", "v2")
    git(repo, "tag", REPO_V2)
    return repo


def repo_url(repo) -> str:
    """The module source for the fixture repo.

    A file:// URL rather than a bare path: modulesource.IsLocal treats only ./
    ../ and / prefixes as local, and a local path has no ref to point at. The ref
    is passed as --ref, which is how the CLI takes one.
    """
    return f"file://{repo}"


def apply_repo(tmp_path, repo, ref, extra: str = "", check: bool = True):
    """Apply the fixture repo at ref into ./wrapper, from tmp_path."""
    return atelier(f"apply {repo_url(repo)} --ref {ref} --dir wrapper --strict {extra}",
                   cwd=tmp_path, check=check)


def add_repo(tmp_path, repo, ref, extra: str = "", check: bool = True):
    return atelier(f"add {repo_url(repo)} --ref {ref} --dir wrapper"
                   f" --strict --yes --json {extra}", cwd=tmp_path, check=check)


def module_blocks(main_tf: str) -> list[str]:
    return re.findall(r'^module\s+"([^"]+)"', main_tf, re.M)


def arg_value(main_tf: str, block: str, name: str) -> str | None:
    """The value main.tf assigns to `name` inside `block`, or None if unset.

    Read back through the argument rather than by substring, because hclwrite
    aligns the `=`: `units = "5"` and `units      = "5"` are one assignment
    written two ways.
    """
    body = re.search(rf'module\s+"{block}"\s*\{{(.*?)\n\}}', main_tf, re.S)
    if body is None:
        return None
    m = re.search(rf'^\s*{re.escape(name)}\s*=\s*(.+?)\s*$', body.group(1), re.M)
    return m.group(1) if m else None


def two_block_wrapper(tmp_path, repo, second_ref=None) -> Path:
    """Build the two-block wrapper an older version of apply could produce.

    One block is deployed properly, then a second is appended by hand at
    `second_ref` (v2 by default, so the two differ by ref as well as by name).
    """
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir(exist_ok=True)
    apply_repo(tmp_path, repo, REPO_V1, "--var model_uuid=u1 --var units=1")
    with (wrapper / "main.tf").open("a") as fh:
        fh.write(
            f'''
module "cos_lite_2" {{
  source     = "git::{repo_url(repo)}?ref={second_ref or REPO_V2}"
  model_uuid = "u2"
}}'''
        )
    return wrapper


def test_rerunning_an_identical_apply_is_idempotent(tmp_path, module_repo):
    # GIVEN a wrapper deployed at a ref
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir()
    apply_repo(tmp_path, module_repo, REPO_V1, "--var model_uuid=u1 --var units=1")
    first = (wrapper / "main.tf").read_text()
    assert module_blocks(first) == ["cos_lite"], first

    # WHEN the identical command runs again
    again = apply_repo(tmp_path, module_repo, REPO_V1, "--var model_uuid=u1 --var units=1")

    # THEN it succeeds rather than refusing as a duplicate, and the wrapper is
    # unchanged — one block, same values. This is what a CI job re-running its
    # deploy command needs.
    assert again.returncode == 0, again.stderr
    assert (wrapper / "main.tf").read_text() == first


def test_apply_var_merges_into_the_existing_block(tmp_path, module_repo):
    # GIVEN a wrapper holding several configured values
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir()
    apply_repo(tmp_path, module_repo, REPO_V1, "--var model_uuid=u1 --var units=3")

    # WHEN a re-run sets only one of them
    apply_repo(tmp_path, module_repo, REPO_V1, "--var units=5")

    # THEN the named value is updated and the other is untouched. The freshly
    # cloned state carries defaults, not what main.tf held, so writing it without
    # carrying the block's declaration over would silently revert model_uuid —
    # the data-loss case the change exists to prevent.
    main_tf = (wrapper / "main.tf").read_text()
    assert arg_value(main_tf, "cos_lite", "units") == '"5"', main_tf
    assert arg_value(main_tf, "cos_lite", "model_uuid") == '"u1"', main_tf
    assert module_blocks(main_tf) == ["cos_lite"], main_tf


def test_apply_var_file_merges_into_the_existing_block(tmp_path, module_repo):
    # GIVEN a wrapper with values set and a bundle ready beside it
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir()
    apply_repo(tmp_path, module_repo, REPO_V1, "--var model_uuid=u1 --var units=1")

    bundle = tmp_path / "override.tfvars"
    bundle.write_text('units = "7"\n')

    # WHEN a bundle is applied on top
    apply_repo(tmp_path, module_repo, REPO_V1, "--var-file override.tfvars")

    # THEN the bundle's value wins and the value it does not mention survives
    main_tf = (wrapper / "main.tf").read_text()
    assert arg_value(main_tf, "cos_lite", "units") == '"7"', main_tf
    assert arg_value(main_tf, "cos_lite", "model_uuid") == '"u1"', main_tf


def test_apply_repoints_the_ref_rather_than_appending(tmp_path, module_repo):
    # GIVEN a wrapper pinned at v1 with values configured
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir()
    apply_repo(tmp_path, module_repo, REPO_V1, "--var model_uuid=u1 --var units=1")

    # WHEN the same module is applied at v2
    done = apply_repo(tmp_path, module_repo, REPO_V2, "--var model=u2")

    # THEN the block was re-pointed, not duplicated. Before ADR-0050 this
    # appended module "cos_lite_2" and left the wrapper declaring two copies of
    # one module, which Terraform then collides on.
    main_tf = (wrapper / "main.tf").read_text()
    assert module_blocks(main_tf) == ["cos_lite"], main_tf
    assert f"ref={REPO_V2}" in main_tf, main_tf

    # AND the value that survived the revision change was carried over, the one
    # v2 dropped is gone, and the new input was supplied
    assert arg_value(main_tf, "cos_lite", "model_uuid") == '"u1"', main_tf
    assert arg_value(main_tf, "cos_lite", "units") is None, main_tf
    assert arg_value(main_tf, "cos_lite", "model") == '"u2"', main_tf

    # AND the run says what it changed, because a block rewritten under the same
    # name otherwise looks like the command did nothing.
    assert "cos_lite" in done.stderr, done.stderr
    assert REPO_V2 in done.stderr, done.stderr

    # No state assertion here: v2 drops the `model_uuid` variable, so Terraform
    # creates no resource for it and there is nothing in state to read. The
    # carry-over is asserted above, where it is visible — in the file.


def test_apply_reports_a_new_required_input_from_the_new_ref(tmp_path, module_repo):
    # GIVEN a wrapper pinned at v1
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir()
    apply_repo(tmp_path, module_repo, REPO_V1, "--var model_uuid=u1 --var units=1")

    # WHEN v2 is applied without v2's new required input
    done = apply_repo(tmp_path, module_repo, REPO_V2, check=False)

    # THEN it stops before Terraform, naming the input and how to supply it,
    # rather than letting the module fail deep inside apply.
    assert done.returncode != 0, done.stderr
    assert "model" in done.stderr, done.stderr
    assert "--var" in done.stderr, done.stderr


def test_apply_reports_an_argument_it_pruned_for_being_at_its_default(tmp_path, module_repo):
    # GIVEN a wrapper with a hand-written argument set to the module's default.
    # Passing it with --var does not produce this state: Atelier's own writer
    # never emits an at-default argument, so the argument has to be written into
    # main.tf by hand or seeded from an upstream example.
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir()
    apply_repo(tmp_path, module_repo, REPO_V1, "--var model_uuid=u1 --var units=1")
    main_tf = wrapper / "main.tf"
    main_tf.write_text(main_tf.read_text().replace(
        'units      = "1"', 'units      = "1"\n  region     = "eu"'))
    assert arg_value(main_tf.read_text(), "cos_lite", "region") == '"eu"'

    # WHEN the same module is applied again
    done = apply_repo(tmp_path, module_repo, REPO_V1, "--var model_uuid=u1 --var units=1")

    # THEN the at-default argument is gone — the sparse rule (ADR-0007) — and the
    # run says so, so a line leaving the user's file is never a silent edit. It is
    # not reported as a dropped input either: the revision still declares region,
    # it is the value that is redundant.
    after = main_tf.read_text()
    assert arg_value(after, "cos_lite", "region") is None, after
    assert arg_value(after, "cos_lite", "model_uuid") == '"u1"', after
    assert "region" in done.stderr, done.stderr
    assert "pruned" in done.stderr, done.stderr

    # No state assertion here, and that is deliberate rather than an omission.
    # "Pruning an at-default argument changes nothing Terraform computes" cannot be
    # shown from state: the argument is prunable only because its value equals the
    # declared default, so after the prune Terraform reads the same default and
    # writes the same value. The assertion would pass whether the prune happened or
    # not. What the prune must not lose is a *non*-default value, which the
    # re-point test above covers from the file.


def test_apply_refuses_when_two_blocks_declare_the_module(tmp_path, module_repo):
    # GIVEN a wrapper holding two blocks of one module — the state an earlier
    # version produced by appending instead of updating
    wrapper = two_block_wrapper(tmp_path, module_repo)

    # WHEN apply cannot tell which block the request is about
    done = apply_repo(tmp_path, module_repo, REPO_V2, check=False)

    # THEN it refuses and names both, rather than guessing. Re-pointing one at a
    # coin flip would be worse than asking.
    assert done.returncode != 0, done.stderr
    assert "cos_lite" in done.stderr and "cos_lite_2" in done.stderr, done.stderr
    assert "--as" in done.stderr, done.stderr
    assert module_blocks((wrapper / "main.tf").read_text()) == ["cos_lite", "cos_lite_2"]


def test_apply_as_selects_the_block_to_update(tmp_path, module_repo):
    # GIVEN the same two-block wrapper, with both blocks at v1
    wrapper = two_block_wrapper(tmp_path, module_repo, second_ref=REPO_V1)

    # WHEN --as names the block to write
    done = apply_repo(tmp_path, module_repo, REPO_V2,
                      "--as cos_lite_2 --var model_uuid=u1 --var model=u2")

    # THEN only the named block moved, and the sibling was left alone
    main_tf = (wrapper / "main.tf").read_text()
    assert module_blocks(main_tf) == ["cos_lite", "cos_lite_2"], main_tf
    first, second = main_tf.split('module "cos_lite_2"')
    assert REPO_V1 in first, main_tf
    assert REPO_V2 in second, main_tf
    assert done.returncode == 0, done.stderr


def test_apply_as_naming_no_block_updates_the_one_that_declares_the_module(tmp_path, module_repo):
    # GIVEN a wrapper whose block was renamed, so nothing is called the name the
    # caller supplies. A gallery entry fills --as in from its own entry, so this
    # is what `atelier apply <gallery-name>` does after any rename.
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir()
    apply_repo(tmp_path, module_repo, REPO_V1,
               "--as prod_cos --var model_uuid=u1 --var units=1")
    assert module_blocks((wrapper / "main.tf").read_text()) == ["prod_cos"]

    # WHEN a name that matches no block is supplied
    done = apply_repo(tmp_path, module_repo, REPO_V1, "--as cos_lite --var model_uuid=u2")

    # THEN the block that does declare the module is updated, keeps its name, and
    # the unmatched name is reported rather than silently dropped. Refusing here
    # would dead-end the most common gallery flow with "edit main.tf by hand".
    main_tf = (wrapper / "main.tf").read_text()
    assert module_blocks(main_tf) == ["prod_cos"], main_tf
    assert arg_value(main_tf, "prod_cos", "model_uuid") == '"u2"', main_tf
    assert "--as cos_lite names no block" in done.stderr, done.stderr
    assert done.returncode == 0, done.stderr


def test_add_still_refuses_a_module_the_wrapper_already_has(tmp_path, module_repo):
    # GIVEN a wrapper holding one module
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir()
    add_repo(tmp_path, module_repo, REPO_V1, "--var model_uuid=u1 --var units=1")
    before = (wrapper / "main.tf").read_text()

    # WHEN add runs again for the same module. add authors; it does not overwrite
    # configuration the user made by hand without being asked.
    done = add_repo(tmp_path, module_repo, REPO_V1, check=False)

    # THEN it refuses, naming a way out that is not a second copy
    assert done.returncode != 0, done.stderr
    assert "cos_lite" in done.stderr, done.stderr
    assert "atelier apply" in done.stderr, done.stderr
    assert (wrapper / "main.tf").read_text() == before


def test_apply_preserves_a_wired_expression_in_the_updated_block(tmp_path, module_repo):
    # GIVEN a block with a wired reference Atelier cannot evaluate as a value —
    # the ADR-0017 inter-module case
    other = write_module(tmp_path / "src" / "loki", "loki", {"endpoint": ""})
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir()
    atelier(f"add {other} --dir wrapper --strict --yes --json"
            " --var endpoint=http://loki:3100", cwd=tmp_path)
    with (wrapper / "main.tf").open("a") as fh:
        fh.write(
            f'''
module "cos_lite" {{
  source     = "git::{repo_url(module_repo)}?ref={REPO_V1}"
  model_uuid = module.loki.endpoint
  units      = "1"
}}'''
        )

    # WHEN the block is re-pointed at v2
    apply_repo(tmp_path, module_repo, REPO_V2, "--var model=u2")

    # THEN the reference survived verbatim, and the wired input satisfied the
    # required-input gate. Carrying values alone would have dropped the reference,
    # and RenderMain would have pruned it from the block.
    main_tf = (wrapper / "main.tf").read_text()
    assert arg_value(main_tf, "cos_lite", "model_uuid") == "module.loki.endpoint", main_tf
    assert arg_value(main_tf, "cos_lite", "units") is None, main_tf
    assert arg_value(main_tf, "cos_lite", "model") == '"u2"', main_tf


def test_apply_preserves_depends_on_when_repointing(tmp_path, module_repo):
    # GIVEN a block whose meta-argument describes how it is instantiated
    other = write_module(tmp_path / "src" / "loki", "loki", {"endpoint": ""})
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir()
    atelier(f"add {other} --dir wrapper --strict --yes --json"
            " --var endpoint=http://loki:3100", cwd=tmp_path)
    with (wrapper / "main.tf").open("a") as fh:
        fh.write(
            f'''
module "cos_lite" {{
  source     = "git::{repo_url(module_repo)}?ref={REPO_V1}"
  model_uuid = "u1"
  units      = "1"
  depends_on = [module.loki]
}}'''
        )

    # WHEN the block is re-pointed
    apply_repo(tmp_path, module_repo, REPO_V2, "--var model=u2")

    # THEN depends_on is still there. It is not a module input, so it is never a
    # stale variable — the same fix applies to the TUI's ref switch.
    main_tf = (wrapper / "main.tf").read_text()
    assert arg_value(main_tf, "cos_lite", "depends_on") == "[module.loki]", main_tf


# --- lifecycle: the wrapper measured against the state it produced -------------

@pytest.fixture
def versioned_value_repo(tmp_path) -> Path:
    """A local git repo whose two tags differ only in one variable's default.

    Both revisions declare the same variables and resources, so a ref change is
    an in-place update of the same addresses rather than a different resource
    set. `model_uuid` is required, so it is always written; `region` keeps its
    default, so it is pruned and the change between tags rides in the module.
    """
    repo = tmp_path / "value-module"
    write_module(repo, "value_module", {"model_uuid": ""}, {"region": "eu"})
    git(repo, "init", "-q", "-b", "main")
    git(repo, "config", "user.email", "test@example.invalid")
    git(repo, "config", "user.name", "Test")
    git(repo, "add", "-A")
    git(repo, "commit", "-q", "-m", "v1")
    git(repo, "tag", REPO_V1)
    write_module(repo, "value_module", {"model_uuid": ""}, {"region": "us"})
    git(repo, "add", "-A")
    git(repo, "commit", "-q", "-m", "v2")
    git(repo, "tag", REPO_V2)
    return repo


def test_ref_change_updates_in_place_and_keeps_state(tmp_path, versioned_value_repo):
    # GIVEN a wrapper deployed at v1
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir()
    apply_repo(tmp_path, versioned_value_repo, REPO_V1, "--var model_uuid=u1")
    tf = TfDirManager()
    tf.latch(wrapper)
    before = tf.state_addresses()
    assert before, "apply should have written resources to state"

    # WHEN the same module is applied at v2, whose only change is a default
    apply_repo(tmp_path, versioned_value_repo, REPO_V2, "--var model_uuid=u1")

    # THEN the block was re-pointed, not duplicated
    main_tf = (wrapper / "main.tf").read_text()
    assert f"ref={REPO_V2}" in main_tf, main_tf
    assert module_blocks(main_tf) == ["value_module"], main_tf

    # AND the change updated that value in place: the state addresses are
    # identical and a fresh plan is empty, so nothing was re-created. Bumping the
    # ref is not a re-key — the promise a live upgrade rests on.
    assert tf.state_addresses() == before, "a ref change re-keyed the state"
    assert tf.plan_changes() == []


def test_apply_converges_leaving_nothing_to_plan(tmp_path, versioned_value_repo):
    # GIVEN a wrapper Atelier deployed
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir()
    apply_repo(tmp_path, versioned_value_repo, REPO_V1, "--var model_uuid=u1")

    # THEN a plain plan finds the configuration already satisfied — the claim a
    # CI job that treats any pending change as drift depends on
    tf = TfDirManager()
    tf.latch(wrapper)
    assert tf.plan_changes() == []


def test_a_hand_edit_survives_the_next_apply(tmp_path, versioned_value_repo):
    # GIVEN a deployed wrapper a user annotated by hand
    wrapper = tmp_path / "wrapper"
    wrapper.mkdir()
    apply_repo(tmp_path, versioned_value_repo, REPO_V1, "--var model_uuid=u1")
    main = wrapper / "main.tf"
    text = main.read_text()
    block = module_blocks(text)[0]
    main.write_text(text.replace(f'module "{block}"',
                                 f'# hand-written note: keep me\nmodule "{block}"', 1))

    # WHEN a later run changes a value in that block
    apply_repo(tmp_path, versioned_value_repo, REPO_V1, "--var model_uuid=u2")

    # THEN the hand-written comment is still there and the value was written: the
    # sparse-plus-required write preserves the user's edits (ADR-0007)
    text = main.read_text()
    assert "# hand-written note: keep me" in text, text
    assert arg_value(text, block, "model_uuid") == '"u2"', text


def test_wrapper_gitignores_state_and_private_dir(tmp_path, versioned_value_repo):
    # GIVEN a wrapper Atelier authored
    add_repo(tmp_path, versioned_value_repo, REPO_V1)

    # THEN its .gitignore keeps Terraform state (which can hold secrets) and
    # Atelier's regenerable private tree out of version control
    gitignore = (tmp_path / "wrapper" / ".gitignore").read_text()
    assert "terraform.tfstate" in gitignore, gitignore
    assert ".atelier/" in gitignore, gitignore