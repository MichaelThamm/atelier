# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Helpers for Atelier's Juju + Terraform integration tests."""

import json
import logging
import os
import shlex
import subprocess
from pathlib import Path
from typing import Optional

import jubilant
import yaml

logger = logging.getLogger(__name__)


class TfDirManager:
    """Runs Terraform against a wrapper directory authored by Atelier.

    A static-fixture manager copies a pre-written ``.tf`` file into a scratch
    directory and plans there. Atelier's wrapper *is* the artifact, so this
    manager instead **latches onto the directory Atelier writes**: allocate it
    with :meth:`new_wrapper_dir`, run ``atelier module add …`` in it, then call
    :meth:`init` / :meth:`apply` to run Terraform in that same directory.
    """

    def __init__(self, base_tmpdir):
        self.base: str = str(base_tmpdir)
        self.dir: str = ""

    @property
    def tf_cmd(self) -> str:
        if not self.dir:
            raise RuntimeError("TfDirManager has not latched onto a directory yet")
        return f"terraform -chdir={self.dir}"

    def new_wrapper_dir(self, name: str = "wrapper") -> str:
        """Allocate and return a fresh directory for Atelier to bootstrap into."""
        self.dir = os.path.join(self.base, name)
        os.makedirs(self.dir, exist_ok=True)
        return self.dir

    def latch(self, directory) -> None:
        """Point the manager at an existing wrapper directory."""
        self.dir = str(directory)

    def init(self, *extra_args: str) -> None:
        """Run ``terraform init -upgrade`` in the latched wrapper directory."""
        cmd = f"{self.tf_cmd} init -upgrade"
        if extra_args:
            cmd += " " + " ".join(shlex.quote(a) for a in extra_args)
        subprocess.run(shlex.split(cmd), check=True)

    def validate(self) -> None:
        """Run ``terraform validate`` in the latched wrapper directory."""
        subprocess.run(shlex.split(f"{self.tf_cmd} validate"), check=True)

    def plan_changes(self) -> list[tuple[str, str, list[str]]]:
        """Return a plan's managed-resource changes as ``(address, type, actions)``.

        Runs ``terraform plan -out`` then ``terraform show -json`` so the result
        is machine-readable. Data sources and no-op changes are omitted; each
        remaining entry is something the plan would add, change, or destroy.

        This is what lets a caller separate real infrastructure deltas from
        ``terraform_data`` bookkeeping, which has no live counterpart and can
        never be imported.
        """
        plan_file = os.path.join(self.dir, ".atelier-integration.tfplan")
        logger.info("running: %s plan -out=%s", self.tf_cmd, plan_file)
        subprocess.run(
            shlex.split(f"{self.tf_cmd} plan -out={plan_file} -input=false -no-color"),
            check=True,
            capture_output=True,
            text=True,
        )
        try:
            shown = subprocess.run(
                shlex.split(f"{self.tf_cmd} show -json {plan_file}"),
                check=True,
                capture_output=True,
                text=True,
            ).stdout
        finally:
            if os.path.exists(plan_file):
                os.remove(plan_file)

        changes: list[tuple[str, str, list[str]]] = []
        for rc in json.loads(shown).get("resource_changes") or []:
            if rc.get("mode") != "managed":
                continue
            actions = (rc.get("change") or {}).get("actions") or []
            if actions == ["no-op"]:
                continue
            changes.append((rc.get("address", ""), rc.get("type", ""), actions))
        return changes

    @staticmethod
    def _args_str(target: Optional[str] = None, **kwargs) -> str:
        target_arg = f"-target module.{target}" if target else ""
        var_args = " ".join(f"-var {k}={v}" for k, v in kwargs.items())
        return "-auto-approve " + f"{target_arg} " + var_args

    def apply(self, target: Optional[str] = None, **kwargs) -> None:
        cmd_str = f"{self.tf_cmd} apply " + self._args_str(target, **kwargs)
        subprocess.run(shlex.split(cmd_str), check=True)

    def destroy(self, **kwargs) -> None:
        cmd_str = f"{self.tf_cmd} destroy " + self._args_str(None, **kwargs)
        subprocess.run(shlex.split(cmd_str), check=True)


def run_atelier(
    wrapper_dir,
    atelier_bin: str,
    *args: str,
    env: Optional[dict] = None,
    capture: bool = False,
    check: bool = True,
) -> subprocess.CompletedProcess:
    """Run the Atelier CLI in ``wrapper_dir`` with stdin pinned to ``/dev/null``.

    Pinning stdin to ``/dev/null`` is what makes ``module add`` non-blocking:
    Atelier detects the non-terminal, applies any ``--preset`` and skips the
    TUI instead of trying (and failing) to open one.

    With ``capture=True`` the completed process is returned with ``stdout`` and
    ``stderr`` as text (useful for ``module list`` assertions).
    """
    cmd = [atelier_bin, *args]
    logger.info("running: %s", " ".join(shlex.quote(c) for c in cmd))
    with open(os.devnull, "rb") as devnull:
        return subprocess.run(
            cmd,
            cwd=wrapper_dir,
            stdin=devnull,
            env={**os.environ, **(env or {})},
            check=check,
            capture_output=capture,
            text=capture,
        )


def write_local_preset(directory, name: str, sets: dict) -> str:
    """Write an ``atelier.local.yaml`` with one named preset; return the name.

    This is the canonical preset mechanism (the same file the TUI's `S` key and
    ``import --preset`` use), so the tests document the real thing.
    """
    path = Path(directory) / "atelier.local.yaml"
    manifest = {"modules": [{"path": ".", "presets": [{"name": name, "sets": sets}]}]}
    path.write_text(yaml.safe_dump(manifest, sort_keys=False))
    logger.info("wrote %s with preset %r: %s", path, name, sets)
    return name


def wait_for_active_idle_without_error(juju: jubilant.Juju, timeout: int = 60 * 45) -> None:
    """Wait for every unit in the model to be active and every agent idle."""
    print(f"\nwaiting for the model ({juju.model}) to settle ...\n")
    juju.wait(jubilant.all_active, delay=10, timeout=timeout)
    print("\nwaiting for agents idle ...\n")
    juju.wait(
        jubilant.all_agents_idle,
        delay=10,
        timeout=timeout,
        error=jubilant.any_error,
    )
