# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Helpers for Atelier's Juju + Terraform integration tests.

Atelier authors the wrapper and deploys it; a test's job is to ask what happened.
:func:`run` and :func:`write_tfvars` cover the two things that are fiddly to get
right — running Atelier without a terminal, and writing an input bundle — and
:class:`TfDirManager` asks Terraform what became of the wrapper.

Atelier's own commands are called as :func:`run` spells them out, because that is
what anyone scripting Atelier has to write: they cannot import from here. Read the
flags in a test and you can see the command it runs.
"""

import json
import logging
import os
import shlex
import shutil
import subprocess
from functools import lru_cache
from pathlib import Path

import jubilant

logger = logging.getLogger(__name__)

# Flags whose value is a module input, and may be a secret.
SECRET_FLAGS = ("--var", "--query-var")


@lru_cache(maxsize=None)
def atelier_bin() -> str:
    """The Atelier under test: ``ATELIER_BIN``, else ``atelier`` on ``PATH``.

    Absolute, because :func:`run` executes inside the wrapper under test, where a
    relative path would be looked for instead of where the test started.
    """
    configured = os.environ.get("ATELIER_BIN") or "atelier"
    # which() expands a PATH name but returns a path-like argument unchanged.
    return os.path.abspath(shutil.which(configured) or configured)


def _redact(argv: list[str]) -> list[str]:
    """``argv`` with module-input values masked, so a secret cannot reach the log."""
    out, hide = [], False
    for arg in argv:
        if hide:
            out.append(arg.split("=", 1)[0] + "=***")
            hide = False
        else:
            out.append(arg)
            hide = arg in SECRET_FLAGS
    return out


def run(*args: str, cwd: Path | str, check: bool = True) -> subprocess.CompletedProcess[str]:
    """Run ``atelier <args>`` in ``cwd`` and return the finished process.

    Two details are what make this work from CI. Standard input is
    ``/dev/null``, which is how Atelier knows there is no terminal and declines to
    open its editor instead of hanging. And stdout is captured on its own, so the
    ``--json`` payload can be read with ``json.loads(result.stdout)["data"]``
    without progress chatter mixed in.

    With ``check`` (the default) a non-zero exit raises ``CalledProcessError``;
    pass ``check=False`` to assert on the exit instead.
    """
    cmd = [atelier_bin(), *args]
    logger.info("running: %s (in %s)", shlex.join(_redact(cmd)), cwd)
    result = subprocess.run(cmd, cwd=cwd, stdin=subprocess.DEVNULL,
                            capture_output=True, text=True, timeout=3600, check=False)
    logger.debug("exit %d\nstdout:\n%s\nstderr:\n%s", result.returncode, result.stdout, result.stderr)
    if check and result.returncode != 0:
        raise subprocess.CalledProcessError(result.returncode, cmd,
                                            output=result.stdout, stderr=result.stderr)
    return result


def write_tfvars(directory: Path | str, name: str, values: dict) -> Path:
    """Write a ``.tfvars`` input bundle and return its path.

    Returns a path rather than a name on purpose. ``--var-file`` also accepts a
    preset name, and one that resolves to nothing is only a warning — so passing
    the name drops the bundle without failing.
    """
    path = Path(directory) / f"{name}.tfvars"
    path.write_text("".join(f"{k} = {_hcl(v)}\n" for k, v in values.items()))
    logger.info("wrote %s with variables: %s", path, ", ".join(values))
    return path


def _hcl(value) -> str:
    """Render a Python value as HCL. bool is checked before int: bool is an int."""
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, (int, float)):
        return str(value)
    if isinstance(value, str):
        return json.dumps(value)
    if value is None:
        return "null"
    if isinstance(value, dict):
        return "{ " + ", ".join(f"{k} = {_hcl(v)}" for k, v in value.items()) + " }"
    if isinstance(value, (list, tuple)):
        return "[" + ", ".join(_hcl(v) for v in value) + "]"
    raise TypeError(f"cannot render {value!r} as HCL")


class TfDirManager:
    """Asks Terraform what became of a wrapper Atelier wrote. Verification, not authoring.

    The wrapper is the artifact (ADR-0001), so this attaches to the directory
    Atelier wrote and answers only the three questions Atelier has no command for:
    is it valid, what is in state, and would a plan change anything. Editing the
    wrapper afterwards is the test's own business — write the HCL, then ``init``
    and ``validate``.
    """

    def __init__(self) -> None:
        self.dir: str = ""

    def latch(self, directory: Path | str) -> None:
        """Point this at a wrapper directory."""
        self.dir = str(directory)

    def _run(self, *args: str) -> subprocess.CompletedProcess[str]:
        cmd = shlex.split(f"terraform -chdir={self.dir} {shlex.join(args)}")
        logger.info("running: %s", shlex.join(cmd))
        return subprocess.run(cmd, stdin=subprocess.DEVNULL, check=True, capture_output=True, text=True)

    def init(self) -> None:
        """Fetch the module and providers, without deploying anything."""
        self._run("init", "-upgrade")

    def validate(self) -> None:
        """Check the configuration parses — still without deploying."""
        self._run("validate")

    def state_list(self) -> list[str]:
        """Addresses currently in state. Complements :meth:`plan_changes`, which is
        empty once they exist."""
        return [line for line in self._run("state", "list").stdout.splitlines() if line.strip()]

    def plan_changes(self) -> list[tuple[str, str, list[str]]]:
        """Managed resources a plan would ``(address, type, actions)``, no-ops omitted.

        This is how a caller separates real infrastructure drift from
        ``terraform_data`` bookkeeping, which has no live counterpart and can never
        be imported. An import report gives counts; a drift assertion needs
        addresses.
        """
        plan_file = os.path.join(self.dir, ".atelier-integration.tfplan")
        try:
            self._run("plan", f"-out={plan_file}", "-input=false", "-no-color")
            shown = json.loads(self._run("show", "-json", plan_file).stdout)
        finally:
            if os.path.exists(plan_file):
                os.remove(plan_file)
        return [
            (rc.get("address", ""), rc.get("type", ""), (rc.get("change") or {}).get("actions") or [])
            for rc in shown.get("resource_changes") or []
            if rc.get("mode") == "managed" and (rc.get("change") or {}).get("actions") != ["no-op"]
        ]


def wait_for_active_idle_without_error(juju: jubilant.Juju, timeout: int = 60 * 45) -> None:
    """Wait for every unit in the model to be active and every agent idle."""
    print(f"\nwaiting for the model ({juju.model}) to settle ...\n")
    juju.wait(jubilant.all_active, delay=10, timeout=timeout)
    print("\nwaiting for agents idle ...\n")
    juju.wait(jubilant.all_agents_idle, delay=10, timeout=timeout, error=jubilant.any_error)