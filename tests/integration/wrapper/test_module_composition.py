# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Composing several modules into one wrapper root.

Multi-module composition within a single root is in scope (ADR-0016), and the
TUI already groups and context-switches across module blocks (ADR-0015). These
tests cover the CLI half: that `--dir` names the wrapper to compose into, for
both `add` and `apply`, so a deployment is built one module at a time without
entering the wrapper between steps.

They use tiny local module fixtures rather than upstream repos: composition is a
property of the CLI's target resolution, not of any module's contents, and a
local source keeps the test hermetic and in the fast tier.

Atelier is called through `run`, spelled out, because that is how anything
scripting Atelier has to call it — nobody can import from a test directory.

See ADR-0044.
"""

import json
import re
import subprocess

import pytest

from helpers import TfDirManager, run


def write_module(directory, name: str, required_var: str, outputs=()) -> str:
    """Write a minimal one-variable Terraform module and return its path.

    A candidate is a directory declaring a `variable` with a type constraint
    (internal/candidate), so each fixture has exactly one.
    """
    directory.mkdir(parents=True, exist_ok=True)
    output_blocks = "".join(
        f'\noutput "{o}" {{\n  value = "{name}-{o}"\n}}\n' for o in outputs
    )
    (directory / "main.tf").write_text(
        f'''variable "{required_var}" {{
  type = string
}}
{output_blocks}
resource "terraform_data" "{name}" {{
  input = var.{required_var}
}}
'''
    )
    return str(directory)


def module_blocks(main_tf: str) -> list[str]:
    return re.findall(r'^module\s+"([^"]+)"', main_tf, re.M)


def test_add_composes_a_second_module_by_dir(tmp_path):
    # GIVEN a wrapper holding one module
    first = write_module(tmp_path / "src" / "cos-lite", "cos_lite", "model_uuid")
    run("add", first, "--dir", "wrapper", "--strict", "--yes", "--json", cwd=tmp_path)
    main_tf = (tmp_path / "wrapper" / "main.tf").read_text()
    assert module_blocks(main_tf) == ["cos_lite"], main_tf

    # WHEN a second module is added from outside the wrapper, naming it with
    # --dir. This is the composition path ADR-0044 restored: --dir names the
    # wrapper to compose into, and used to be rejected outright.
    second = write_module(tmp_path / "src" / "charmed-spark", "charmed_spark", "channel")
    added = json.loads(run("add", second, "--dir", "wrapper", "--strict", "--yes", "--json", cwd=tmp_path).stdout)["data"]
    # THEN both blocks are declared in one root
    main_tf = (tmp_path / "wrapper" / "main.tf").read_text()
    assert module_blocks(main_tf) == ["cos_lite", "charmed_spark"], main_tf
    # AND the payload says so, so the file read above is confirmation rather
    # than the only evidence
    assert added["blocks"] == ["cos_lite", "charmed_spark"]


def test_add_composes_into_a_nested_wrapper(tmp_path):
    # GIVEN a wrapper one level under the scratch directory
    first = write_module(tmp_path / "src" / "cos-lite", "cos_lite", "model_uuid")
    run("add", first, "--dir", "stack/cos-lite", "--strict", "--yes", "--json", cwd=tmp_path)

    # WHEN a second module composes into it by a relative --dir, resolved
    # against the invocation directory rather than the runner's
    second = write_module(tmp_path / "src" / "charmed-spark", "charmed_spark", "channel")
    run("add", second, "--dir", "stack/cos-lite", "--strict", "--yes", "--json", cwd=tmp_path)

    # THEN the wrapper holds both
    main_tf = (tmp_path / "stack" / "cos-lite" / "main.tf").read_text()
    assert module_blocks(main_tf) == ["cos_lite", "charmed_spark"], main_tf


def test_composed_wrapper_is_one_valid_terraform_root(tmp_path):
    # GIVEN a wrapper composed of two modules
    first = write_module(tmp_path / "src" / "cos-lite", "cos_lite", "model_uuid")
    run("add", first, "--dir", "wrapper", "--var", "model_uuid=uuid-1",
        "--strict", "--yes", "--json", cwd=tmp_path)
    second = write_module(tmp_path / "src" / "charmed-spark", "charmed_spark", "channel")
    run("add", second, "--dir", "wrapper", "--var", "channel=8.0/stable",
        "--strict", "--yes", "--json", cwd=tmp_path)

    # WHEN Terraform initialises the composed root
    tf = TfDirManager()
    tf.latch(tmp_path / "wrapper")
    tf.init()

    # THEN the two-module root is valid. This is the payoff composition exists
    # for: one root, one state, one apply.
    tf.validate()

    # AND both modules are addressable from the wrapper
    listed = json.loads(run("ls", "--json", cwd=tmp_path / "wrapper").stdout)["data"]["modules"]
    assert [m["name"] for m in listed] == ["cos_lite", "charmed_spark"]


def test_composed_modules_are_wired_by_reference(tmp_path):
    # GIVEN a composed wrapper whose first module exports an output
    first = write_module(tmp_path / "src" / "cos-lite", "cos_lite", "model_uuid", outputs=["greeting"])
    run("add", first, "--dir", "wrapper", "--var", "model_uuid=uuid-1",
        "--strict", "--yes", "--json", cwd=tmp_path)
    second = write_module(tmp_path / "src" / "charmed-spark", "charmed_spark", "greeting")
    run("add", second, "--dir", "wrapper", "--var", "greeting=placeholder",
        "--strict", "--yes", "--json", cwd=tmp_path)

    # WHEN the second module's input is wired to the first module's output, the
    # reference ADR-0017 describes
    main_tf_path = tmp_path / "wrapper" / "main.tf"
    main_tf = main_tf_path.read_text()
    assert 'greeting = "placeholder"' in main_tf, main_tf
    main_tf_path.write_text(main_tf.replace('"placeholder"', "module.cos_lite.greeting"))
    # THEN Terraform resolves it, so the two blocks are composed in the graph
    # rather than merely sharing a file
    tf = TfDirManager()
    tf.latch(tmp_path / "wrapper")
    tf.init()
    tf.validate()


def test_apply_composes_and_deploys_the_whole_root(tmp_path):
    # GIVEN a wrapper holding one module
    first = write_module(tmp_path / "src" / "cos-lite", "cos_lite", "model_uuid")
    run("add", first, "--dir", "wrapper", "--var", "model_uuid=uuid-1",
        "--strict", "--yes", "--json", cwd=tmp_path)

    # WHEN a second module is composed and deployed with `apply --dir`. The
    # fixtures use only the builtin terraform provider, so this runs for real
    # without downloading anything. Note there is no --yes: Terraform's own plan
    # prompt is the confirmation, and the CLI rejects the flag here.
    second = write_module(tmp_path / "src" / "charmed-spark", "charmed_spark", "channel")
    run("apply", second, "--dir", "wrapper", "--var", "channel=8.0/stable",
        "--strict", cwd=tmp_path)

    # THEN the block landed in the existing wrapper rather than a new directory
    # beside it. Before ADR-0044, apply refused a non-empty target outright, so
    # composing into a wrapper was impossible.
    main_tf = (tmp_path / "wrapper" / "main.tf").read_text()
    assert module_blocks(main_tf) == ["cos_lite", "charmed_spark"], main_tf
    assert not (tmp_path / "wrapper" / "charmed-spark").exists(), "apply nested a root"

    # AND the apply succeeded, producing state for both modules in one root
    tf = TfDirManager()
    tf.latch(tmp_path / "wrapper")
    state = tf.state_list()
    assert "module.cos_lite.terraform_data.cos_lite" in state, state
    assert "module.charmed_spark.terraform_data.charmed_spark" in state, state


def test_apply_inside_a_wrapper_deploys_it_rather_than_nesting(tmp_path):
    # GIVEN a wrapper holding one module
    wrapper = tmp_path / "wrapper"
    first = write_module(tmp_path / "src" / "cos-lite", "cos_lite", "model_uuid")
    run("add", first, "--dir", "wrapper", "--strict", "--yes", "--json", cwd=tmp_path)
    before = sorted(p.name for p in wrapper.iterdir())

    # WHEN apply runs with the wrapper as the CWD. It exits non-zero, because
    # the module's required channel is unset; the wrapper is still written.
    second = write_module(tmp_path / "src" / "charmed-spark", "charmed_spark", "channel")
    with pytest.raises(subprocess.CalledProcessError):
        run("apply", second, "--strict", cwd=wrapper)

    # THEN the module joined that wrapper, and no second root was created
    # inside it. Before ADR-0044 apply scaffolded a candidate-named directory
    # under the CWD, quietly nesting a root in a root.
    main_tf = (wrapper / "main.tf").read_text()
    assert module_blocks(main_tf) == ["cos_lite", "charmed_spark"], main_tf
    after = sorted(p.name for p in wrapper.iterdir())
    assert after == before, f"apply created {set(after) - set(before)} inside the wrapper"


def test_compose_resolves_a_relative_local_source_against_the_invocation_dir(tmp_path):
    # GIVEN a wrapper built from one module
    first = write_module(tmp_path / "src" / "cos-lite", "cos_lite", "model_uuid")
    run("add", first, "--dir", "stack/cos-lite", "--strict", "--yes", "--json", cwd=tmp_path)

    # WHEN a second module is composed by a source path relative to the CWD,
    # with the wrapper somewhere else
    write_module(tmp_path / "src" / "charmed-spark", "charmed_spark", "channel")
    run("add", "./src/charmed-spark", "--dir", "stack/cos-lite",
        "--strict", "--yes", "--json", cwd=tmp_path)

    # THEN it resolved against the invocation directory, not the wrapper. Before
    # ADR-0044 the additive path passed no base directory, so a --dir outside
    # the CWD made it look for the module under the wrapper and fail.
    main_tf = (tmp_path / "stack" / "cos-lite" / "main.tf").read_text()
    assert module_blocks(main_tf) == ["cos_lite", "charmed_spark"], main_tf


def test_apply_refuses_a_non_wrapper_non_empty_directory(tmp_path):
    # GIVEN a directory holding files but no main.tf — the mistyped --dir case
    # ADR-0030's preflight exists for
    source = write_module(tmp_path / "src" / "cos-lite", "cos_lite", "model_uuid")
    busy = tmp_path / "busy"
    busy.mkdir()
    (busy / "notes.md").write_text("not a wrapper\n")

    # WHEN apply targets it, THEN it is refused and nothing is written. Only a
    # main.tf opts into the additive case; every other non-empty target stays
    # refused.
    with pytest.raises(subprocess.CalledProcessError):
        run("apply", source, "--dir", "busy", "--strict", cwd=tmp_path)

    assert not (busy / "main.tf").exists()