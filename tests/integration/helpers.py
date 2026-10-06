# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Helpers for Atelier's Juju + Terraform integration tests.

Atelier authors the wrapper and deploys it; a test's job is to ask what happened.
:func:`atelier` runs a command, :class:`TfDirManager` asks Terraform what became
of the wrapper, and :func:`wait_for_active_idle_without_error` settles a model.

:func:`atelier` takes one command line as a string, so every call reads as the
command it stands for and nobody has to guess which binary and flags are behind
it. Nothing here is importable from a user's own repository, which is the point:
the tests spell commands out the way anyone scripting Atelier has to write them,
and read the wrapper back rather than asking Atelier to interpret it for them.
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


@lru_cache(maxsize=None)
def atelier_bin() -> str:
    """The Atelier under test: ``ATELIER_BIN``, else ``atelier`` on ``PATH``.

    Absolute, because :func:`atelier` executes inside the wrapper under test,
    where a relative path would be looked for instead of where the test started.
    """
    configured = os.environ.get("ATELIER_BIN") or "atelier"
    # which() expands a PATH name but returns a path-like argument unchanged.
    return os.path.abspath(shutil.which(configured) or configured)


def atelier(
    command: str, *, cwd: Path | str, check: bool = True
) -> subprocess.CompletedProcess[str]:
    """Run one ``atelier`` command line in ``cwd`` and return the finished process.
    ``command`` is the whole command line except the word ``atelier``, so
    ``atelier("apply cos-lite --dir runner --ref track/2")`` reads as the command
    it runs and can be pasted into a shell with the word put back. It is split
    with POSIX rules, so wrap a value that contains spaces in quotes.

    One consequence to know: POSIX splitting removes a quote used as syntax, so a
    value whose own text needs double quotes — an HCL object, say
    ``cos_offers={dashboard="admin/cos-lite.grafana-dashboards"}`` — arrives
    without them. Such a value belongs in a ``.tfvars`` bundle passed with
    ``--var-file``, which carries no quoting through this path.

    Standard input is ``/dev/null``, which is how Atelier knows no terminal is
    there: ``apply`` auto-approves rather than opening Terraform's plan prompt,
    and ``add`` writes the wrapper rather than launching the editor. A user gets
    the same behaviour by ending their own command line with ``< /dev/null``.

    stdout is captured on its own, so a ``--json`` payload can be read with
    ``json.loads(result.stdout)["data"]`` without progress chatter mixed in.

    With ``check`` (the default) a non-zero exit raises ``CalledProcessError``;
    pass ``check=False`` to assert on the exit instead.
    """
    cmd = [atelier_bin(), *shlex.split(command)]
    logger.info("running: %s (in %s)", shlex.join(cmd), cwd)
    result = subprocess.run(cmd, cwd=cwd, stdin=subprocess.DEVNULL,
                            capture_output=True, text=True, timeout=3600, check=False)
    logger.debug("exit %d\nstdout:\n%s\nstderr:\n%s", result.returncode, result.stdout, result.stderr)
    if check and result.returncode != 0:
        raise subprocess.CalledProcessError(result.returncode, cmd,
                                            output=result.stdout, stderr=result.stderr)
    return result


class TfDirManager:
    """Asks Terraform what became of a wrapper Atelier wrote. Verification, not authoring.

    The wrapper is the artifact (ADR-0001), so this attaches to the directory
    Atelier wrote and answers the questions Atelier has no command for: does
    Terraform accept it, and would a plan change anything? Editing the wrapper
    afterwards is the test's own business.

    Asking Terraform directly is Atelier CI's own business, not a user's: it
    checks what Atelier claims about a wrapper against the tool the wrapper has
    to survive, independently of whatever Atelier reported about itself.
    """

    def __init__(self) -> None:
        self.dir: str = ""

    def latch(self, directory: Path | str) -> None:
        """Point this at a wrapper directory."""
        self.dir = str(directory)

    def _run(self, *args: str) -> subprocess.CompletedProcess[str]:
        cmd = ["terraform", f"-chdir={self.dir}", *args]
        logger.info("running: %s", shlex.join(cmd))
        return subprocess.run(cmd, stdin=subprocess.DEVNULL, check=True, capture_output=True, text=True)

    def init(self) -> None:
        """Fetch the module and providers, without deploying anything."""
        self._run("init", "-upgrade")

    def validate(self) -> None:
        """Check the configuration parses — still without deploying."""
        self._run("validate")

    def plan_changes(self) -> list[tuple[str, str, list[str]]]:
        """Managed resources a plan would ``(address, type, actions)``, no-ops omitted.

        This is how a caller separates real infrastructure drift from
        ``terraform_data`` bookkeeping, which has no live counterpart and can never
        be imported. An import report gives counts; a drift assertion needs
        addresses.

        Deliberately the same question `atelier import --json` answers in
        ``postImportPlan``, reached without reading that payload. A report of drift
        is worth only as much as the plan behind it, so the CI test asserts both
        and is free to catch the two disagreeing.
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