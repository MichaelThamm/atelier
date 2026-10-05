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

from helpers import TfDirManager, atelier


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


def terraform_data_inputs(state_path) -> dict[str, object]:
    """The value each `terraform_data` resource in a state file ended up with.

    Keyed by the address Terraform spells it with. `terraform_data` stores
    `input` as a dynamic value, so the `{"value": ..., "type": ...}` envelope is
    unwrapped and callers see the value they configured.
    """
    state = json.loads(state_path.read_text())
    out: dict[str, object] = {}
    for resource in state.get("resources", []):
        if resource.get("mode") != "managed" or resource["type"] != "terraform_data":
            continue
        address = ".".join(
            p for p in (resource.get("module"), resource["type"], resource["name"]) if p
        )
        for instance in resource.get("instances", []):
            raw = (instance.get("attributes") or {}).get("input")
            out[address] = raw.get("value") if isinstance(raw, dict) else raw
    return out


def test_add_composes_a_second_module_by_dir(tmp_path):
    # GIVEN a wrapper holding one module
    first = write_module(tmp_path / "src" / "cos-lite", "cos_lite", "model_uuid")
    atelier(f"add {first} --dir wrapper --strict --yes --json", cwd=tmp_path)
    main_tf = (tmp_path / "wrapper" / "main.tf").read_text()
    assert module_blocks(main_tf) == ["cos_lite"], main_tf

    # WHEN a second module is added from outside the wrapper, naming it with
    # --dir. This is the composition path ADR-0044 restored: --dir names the
    # wrapper to compose into, and used to be rejected outright.
    second = write_module(tmp_path / "src" / "charmed-spark", "charmed_spark", "channel")
    added = json.loads(atelier(f"add {second} --dir wrapper --strict --yes --json",
                               cwd=tmp_path).stdout)["data"]
    # THEN both blocks are declared in one root
    main_tf = (tmp_path / "wrapper" / "main.tf").read_text()
    assert module_blocks(main_tf) == ["cos_lite", "charmed_spark"], main_tf
    # AND the payload says so, so the file read above is confirmation rather
    # than the only evidence
    assert added["blocks"] == ["cos_lite", "charmed_spark"]


def test_add_composes_into_a_nested_wrapper(tmp_path):
    # GIVEN a wrapper one level under the scratch directory
    first = write_module(tmp_path / "src" / "cos-lite", "cos_lite", "model_uuid")
    atelier(f"add {first} --dir stack/cos-lite --strict --yes --json", cwd=tmp_path)

    # WHEN a second module composes into it by a relative --dir, resolved
    # against the invocation directory rather than the runner's
    second = write_module(tmp_path / "src" / "charmed-spark", "charmed_spark", "channel")
    atelier(f"add {second} --dir stack/cos-lite --strict --yes --json", cwd=tmp_path)

    # THEN the wrapper holds both
    main_tf = (tmp_path / "stack" / "cos-lite" / "main.tf").read_text()
    assert module_blocks(main_tf) == ["cos_lite", "charmed_spark"], main_tf


def test_composed_wrapper_is_one_valid_terraform_root(tmp_path):
    # GIVEN a wrapper composed of two modules
    first = write_module(tmp_path / "src" / "cos-lite", "cos_lite", "model_uuid")
    atelier(f"add {first} --dir wrapper --var model_uuid=uuid-1 --strict --yes --json",
            cwd=tmp_path)
    second = write_module(tmp_path / "src" / "charmed-spark", "charmed_spark", "channel")
    atelier(f"add {second} --dir wrapper --var channel=8.0/stable --strict --yes --json",
            cwd=tmp_path)

    # WHEN Terraform initialises the composed root
    tf = TfDirManager()
    tf.latch(tmp_path / "wrapper")
    tf.init()

    # THEN the two-module root is valid. This is the payoff composition exists
    # for: one root, one state, one apply.
    tf.validate()

    # AND both modules are addressable from the wrapper
    listed = json.loads(atelier("ls --json", cwd=tmp_path / "wrapper").stdout)["data"]["modules"]
    assert [m["name"] for m in listed] == ["cos_lite", "charmed_spark"]


def test_composed_modules_are_wired_by_reference(tmp_path):
    # GIVEN a composed wrapper whose first module exports an output
    first = write_module(tmp_path / "src" / "cos-lite", "cos_lite", "model_uuid", outputs=["greeting"])
    atelier(f"add {first} --dir wrapper --var model_uuid=uuid-1 --strict --yes --json",
            cwd=tmp_path)
    second = write_module(tmp_path / "src" / "charmed-spark", "charmed_spark", "greeting")
    atelier(f"add {second} --dir wrapper --var greeting=placeholder --strict --yes --json",
            cwd=tmp_path)

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
    atelier(f"add {first} --dir wrapper --var model_uuid=uuid-1 --strict --yes --json",
            cwd=tmp_path)

    # WHEN a second module is composed and deployed with `apply --dir`. The
    # fixtures use only the builtin terraform provider, so this runs for real
    # without downloading anything. Note there is no --yes: Terraform's own plan
    # prompt is the confirmation, and the CLI rejects the flag here.
    second = write_module(tmp_path / "src" / "charmed-spark", "charmed_spark", "channel")
    atelier(f"apply {second} --dir wrapper --var channel=8.0/stable --strict", cwd=tmp_path)

    # THEN the block landed in the existing wrapper rather than a new directory
    # beside it. Before ADR-0044, apply refused a non-empty target outright, so
    # composing into a wrapper was impossible.
    main_tf = (tmp_path / "wrapper" / "main.tf").read_text()
    assert module_blocks(main_tf) == ["cos_lite", "charmed_spark"], main_tf
    assert not (tmp_path / "wrapper" / "charmed-spark").exists(), "apply nested a root"

    # AND both modules landed in one state, each with the value it was configured
    # with — which is the whole claim: one root, one state, one apply. The values
    # are the assertion: addresses alone would be there for two separate roots.
    wrapper = tmp_path / "wrapper"
    inputs = terraform_data_inputs(wrapper / "terraform.tfstate")
    assert inputs["module.cos_lite.terraform_data.cos_lite"] == "uuid-1", inputs
    assert inputs["module.charmed_spark.terraform_data.charmed_spark"] == "8.0/stable", inputs


def test_apply_inside_a_wrapper_deploys_it_rather_than_nesting(tmp_path):
    # GIVEN a wrapper holding one module
    wrapper = tmp_path / "wrapper"
    first = write_module(tmp_path / "src" / "cos-lite", "cos_lite", "model_uuid")
    atelier(f"add {first} --dir wrapper --strict --yes --json", cwd=tmp_path)
    before = sorted(p.name for p in wrapper.iterdir())

    # WHEN apply runs with the wrapper as the CWD. It exits non-zero, because
    # the module's required channel is unset; the wrapper is still written.
    second = write_module(tmp_path / "src" / "charmed-spark", "charmed_spark", "channel")
    with pytest.raises(subprocess.CalledProcessError):
        atelier(f"apply {second} --strict", cwd=wrapper)

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
    atelier(f"add {first} --dir stack/cos-lite --strict --yes --json", cwd=tmp_path)

    # WHEN a second module is composed by a source path relative to the CWD,
    # with the wrapper somewhere else
    write_module(tmp_path / "src" / "charmed-spark", "charmed_spark", "channel")
    atelier("add ./src/charmed-spark --dir stack/cos-lite --strict --yes --json",
            cwd=tmp_path)

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
        atelier(f"apply {source} --dir busy --strict", cwd=tmp_path)

    assert not (busy / "main.tf").exists()