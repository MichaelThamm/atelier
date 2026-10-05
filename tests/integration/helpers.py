# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Helpers for Atelier's Juju + Terraform integration tests.

Atelier authors the wrapper and deploys it; a test's job is to ask what happened.
:func:`atelier_add` and friends ask Atelier and decode the answer,
:class:`TfDirManager` asks Terraform, and :func:`write_tfvars` writes the input
bundle they configure it from.
"""

import json
import logging
import os
import re
import shlex
import shutil
import subprocess
from functools import lru_cache
from pathlib import Path

import jubilant
import pytest

from atelier_json import payload

logger = logging.getLogger(__name__)

#: The oldest Atelier that reports ``--json``, as ``(major, minor)``.
MINIMUM_ATELIER = (0, 20)

# Flags whose value is a module input, and may be a secret.
SECRET_FLAGS = ("--var", "--query-var")


@lru_cache(maxsize=None)
def atelier_bin() -> str:
    """The Atelier under test: ``ATELIER_BIN``, else ``atelier`` on ``PATH``.

    Absolute, because every command below runs *inside* the wrapper it is testing,
    where a relative path would be looked for instead of where the test started.
    """
    configured = os.environ.get("ATELIER_BIN") or "atelier"
    # which() expands a PATH name but returns a path-like argument unchanged.
    return os.path.abspath(shutil.which(configured) or configured)


@lru_cache(maxsize=None)
def supports_json(binary: str) -> bool:
    """Whether this Atelier has ``--json``. An unreadable version — a ``dev``
    build from ``go build`` — counts as new, so working on Atelier is not blocked.
    """
    version = subprocess.run([binary, "--version"], capture_output=True, text=True).stdout
    found = re.search(r"(\d+)\.(\d+)", version)
    return found is None or (int(found[1]), int(found[2])) >= MINIMUM_ATELIER


def run_atelier(*args: str, cwd: Path | str, check: bool = True) -> subprocess.CompletedProcess[str]:
    """Run an Atelier command in ``cwd`` and return the finished process.

    Two details make this work from CI. Standard input is ``/dev/null``, which is
    how Atelier knows there is no terminal and declines to open its editor instead
    of hanging. And stdout and stderr are captured separately, so the ``--json``
    payload can be read without the progress chatter mixed into it.
    """
    binary = atelier_bin()
    if not supports_json(binary):
        pytest.skip(f"{binary} predates {MINIMUM_ATELIER[0]}.{MINIMUM_ATELIER[1]}, when --json was added")
    cmd = [binary, *args]
    logger.info("running: %s (in %s)", shlex.join(_redact(cmd)), cwd)
    result = subprocess.run(cmd, cwd=cwd, stdin=subprocess.DEVNULL,
                            capture_output=True, text=True, timeout=3600, check=False)
    logger.debug("exit %d\nstdout:\n%s\nstderr:\n%s", result.returncode, result.stdout, result.stderr)
    if check and result.returncode != 0:
        raise subprocess.CalledProcessError(result.returncode, cmd,
                                            output=result.stdout, stderr=result.stderr)
    return result


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


def _as_list(value) -> list:
    """Normalise a var-file argument. One bundle reads as a scalar at the call site."""
    if value is None:
        return []
    return [value] if isinstance(value, (str, Path)) else list(value)


def _input_flags(module: str | None = None, ref: str | None = None, dir: Path | str | None = None,
                 var_file=None, var: dict | None = None, as_: str | None = None) -> list[str]:
    """The flags that choose a module and give it values, for add and apply alike.

    ``--strict`` is always on: an unknown key or type mismatch in a bundle is a
    typo, and without it the typo surfaces much later as a missing required input.
    """
    flags: list[str] = []
    for flag, value in (("--as", as_), ("--module", module), ("--ref", ref), ("--dir", dir)):
        if value is not None:
            flags += [flag, str(value)]
    for bundle in _as_list(var_file):
        flags += ["--var-file", str(bundle)]
    for key, value in (var or {}).items():
        flags += ["--var", f"{key}={value}"]
    return flags + ["--strict"]


def atelier_add(source: str, *, cwd: Path | str, as_: str | None = None, ref: str | None = None,
                module: str | None = None, dir: Path | str | None = None,
                var_file=None, var: dict | None = None) -> dict:
    """Write a wrapper for ``source`` without running Terraform; return the payload.

    ``--as`` is sanitised into a valid HCL identifier, so ``cos-lite`` arrives as
    ``cos_lite`` — read the name off ``added["added"]["name"]``, not off ``as_``.
    """
    flags = _input_flags(module, ref, dir, var_file, var, as_)
    return payload(run_atelier("add", source, *flags, "--yes", "--json", cwd=cwd).stdout)


def atelier_apply(source: str, *, cwd: Path | str, as_: str | None = None, ref: str | None = None,
                  module: str | None = None, dir: Path | str | None = None,
                  var_file=None, var: dict | None = None) -> subprocess.CompletedProcess[str]:
    """Write the wrapper, then ``terraform init`` and ``apply`` — the one-call deploy.

    No ``--yes``: Atelier rejects it here, because Terraform's own plan prompt is
    the confirmation. Returns the process, not a payload; this reports Terraform's
    output, which is why ``atelier apply`` has no ``--json``.
    """
    return run_atelier("apply", source, *_input_flags(module, ref, dir, var_file, var, as_), cwd=cwd)


def atelier_import(provider: str, *, cwd: Path | str, source: str, ref: str | None = None,
                   module: str | None = None, dir: Path | str | None = None,
                   var_file=None, query_var: dict | None = None) -> dict:
    """Rebuild Terraform state from what is already running; return the payload.

    State-only: this cannot change infrastructure. It exits ``0`` even when nothing
    matched, so read ``matchedNothing`` or ``imported`` rather than the exit code.

    ``query_var`` feeds the *query* — the model UUID, typically — and must stay
    separate from ``var_file``, which feeds the module. Conflating them is how a
    model UUID ends up written into ``main.tf``.
    """
    flags = ["--source", source, "--yes", "--json"]
    for flag, value in (("--module", module), ("--ref", ref), ("--dir", dir)):
        if value is not None:
            flags += [flag, str(value)]
    for bundle in _as_list(var_file):
        flags += ["--var-file", str(bundle)]
    for key, value in (query_var or {}).items():
        flags += ["--query-var", f"{key}={value}"]
    return payload(run_atelier("import", provider, *flags, cwd=cwd).stdout)


def atelier_ls(*, cwd: Path | str) -> list[dict]:
    """The module blocks a wrapper declares. Empty when ``cwd`` holds no wrapper."""
    return payload(run_atelier("ls", "--json", cwd=cwd).stdout)["modules"]


def write_tfvars(directory: Path | str, name: str, values: dict) -> Path:
    """Write a ``.tfvars`` input bundle and return its path, ready to pass to ``var_file``.

    Returns a path rather than a name on purpose: ``--var-file`` also accepts a
    preset name, and one that resolves to nothing is only a warning — so a name
    here would drop the whole bundle without failing.
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