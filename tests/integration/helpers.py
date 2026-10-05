# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Helpers for Atelier's Juju + Terraform integration tests.

Atelier does the authoring and the deploying: ``atelier add`` writes the wrapper,
and ``atelier apply`` is that plus ``terraform init`` and ``apply``. What is left
for a test is asking questions, which is what the ``atelier_*`` helpers and
:class:`TfDirManager` are for.
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

from atelier_json import (
    MINIMUM_ATELIER,
    AddResult,
    ImportResult,
    ModuleInfo,
    decode_add,
    decode_import,
    decode_ls,
)

logger = logging.getLogger(__name__)

# Flags whose value is a module input and may be a secret, so the value is masked
# before the command is logged.
SECRET_FLAGS = ("--var", "--query-var")


@lru_cache(maxsize=None)
def atelier_bin() -> str:
    """The resolved ``atelier`` under test, from ``ATELIER_BIN`` or ``PATH``.

    Absolute, because every command runs with its working directory set to the
    wrapper under test: a relative ``ATELIER_BIN`` would be looked for inside that
    directory, not the one the test process was started in.
    """
    configured = os.environ.get("ATELIER_BIN") or "atelier"
    # which() resolves a PATH name but returns a path-like argument unchanged, so
    # absolutize either way.
    return os.path.abspath(shutil.which(configured) or configured)


@lru_cache(maxsize=None)
def supports_json(binary: str) -> bool:
    """Whether ``binary`` reports ``--json``. An unreadable version — a ``dev``
    build from ``go build`` — is assumed new enough, so working on Atelier itself
    is never blocked by this check.
    """
    reported = subprocess.run(
        [binary, "--version"], capture_output=True, text=True, check=False
    ).stdout
    found = re.search(r"(\d+)\.(\d+)", reported)
    if found is None:
        return True
    return (int(found[1]), int(found[2])) >= MINIMUM_ATELIER


def _redact(argv: list[str]) -> list[str]:
    """Return ``argv`` with module-input values masked, for logging only."""
    out: list[str] = []
    hide_next = False
    for arg in argv:
        if hide_next:
            out.append(arg.split("=", 1)[0] + "=***")
            hide_next = False
        elif arg in SECRET_FLAGS:
            out.append(arg)
            hide_next = True
        else:
            out.append(arg)
    return out


def run_atelier(
    *args: str,
    cwd: Path | str,
    check: bool = True,
    timeout: float | None = 3600.0,
) -> subprocess.CompletedProcess[str]:
    """Run an Atelier command in ``cwd``, non-interactively.

    Standard input is pinned to ``/dev/null``: that is what makes Atelier skip its
    TUI instead of opening one, which is what makes these helpers usable from CI.
    With ``check`` (the default) a non-zero exit raises ``CalledProcessError`` —
    Atelier exits ``1`` for every error, so stderr is the only detail. Pass
    ``check=False`` to assert on that exit instead.
    """
    binary = atelier_bin()
    if not supports_json(binary):
        pytest.skip(
            f"{binary} predates {MINIMUM_ATELIER[0]}.{MINIMUM_ATELIER[1]}, "
            "which is when --json was added"
        )
    cmd = [binary, *args]
    logger.info("running: %s (in %s)", shlex.join(_redact(cmd)), cwd)
    result = subprocess.run(
        cmd,
        cwd=cwd,
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
        timeout=timeout,
        check=False,
    )
    logger.debug("exit %d\nstdout:\n%s\nstderr:\n%s", result.returncode, result.stdout, result.stderr)
    if check and result.returncode != 0:
        raise subprocess.CalledProcessError(
            result.returncode, cmd, output=result.stdout, stderr=result.stderr
        )
    return result


def _as_list(value: Path | str | list[Path | str] | None) -> list[Path | str]:
    """Normalise a var-file argument to a list.

    A single bundle reads as a scalar at the call site, which is the usual shape.
    """
    if value is None:
        return []
    return [value] if isinstance(value, (str, Path)) else list(value)


def _wrapper_flags(
    *,
    as_: str | None,
    module: str | None,
    ref: str | None,
    dir: Path | str | None,
    var_file: str | list[str] | None,
    var: dict[str, str] | None,
    strict: bool,
) -> list[str]:
    """Build the module-selection and input flags shared by ``add`` and ``apply``.

    Bundles go out before individual ``--var`` values, so a value wins over a
    bundle in the order Terraform applies them.
    """
    flags: list[str] = []
    if as_ is not None:
        flags += ["--as", as_]
    if module is not None:
        flags += ["--module", module]
    if ref is not None:
        flags += ["--ref", ref]
    if dir is not None:
        flags += ["--dir", str(dir)]
    for bundle in _as_list(var_file):
        flags += ["--var-file", str(bundle)]
    for key, value in (var or {}).items():
        flags += ["--var", f"{key}={value}"]
    if strict:
        flags.append("--strict")
    return flags


def atelier_add(
    source: str,
    *,
    cwd: Path | str,
    as_: str | None = None,
    ref: str | None = None,
    module: str | None = None,
    dir: Path | str | None = None,
    var_file: Path | str | list[Path | str] | None = None,
    var: dict[str, str] | None = None,
    strict: bool = True,
    yes: bool = True,
    timeout: float | None = 3600.0,
) -> AddResult:
    """``atelier add``, with its ``--json`` payload decoded.

    Writes the wrapper without running Terraform; returns where it went and what
    is now in it. A target that already holds a wrapper gains another block.
    ``--as`` is sanitised to a valid HCL identifier, so ``cos-lite`` arrives as
    ``cos_lite`` — read the name off the result, not off the argument.

    ``strict`` defaults on: an unknown key or type mismatch in a var-file is a
    typo, and without it the typo surfaces much later as a missing required input.
    A local ``source`` should be absolute — Atelier resolves a relative one
    against the invocation directory but records it verbatim, and Terraform then
    resolves it against the wrapper.
    """
    flags = _wrapper_flags(
        as_=as_,
        module=module,
        ref=ref,
        dir=dir,
        var_file=var_file,
        var=var,
        strict=strict,
    )
    if yes:
        flags.append("--yes")
    return decode_add(run_atelier("add", source, *flags, "--json", cwd=cwd, timeout=timeout).stdout)


def atelier_apply(
    source: str,
    *,
    cwd: Path | str,
    as_: str | None = None,
    ref: str | None = None,
    module: str | None = None,
    dir: Path | str | None = None,
    var_file: Path | str | list[Path | str] | None = None,
    var: dict[str, str] | None = None,
    strict: bool = True,
    timeout: float | None = 3600.0,
) -> subprocess.CompletedProcess[str]:
    """``atelier apply``: write the wrapper, then ``terraform init`` and ``apply``.

    The one-call way to stand a stack up in CI. Same arguments as
    :func:`atelier_add`, less ``yes`` — the CLI rejects that flag here, because
    Terraform's own prompt is the confirmation. Returns the process rather than a
    result: this reports Terraform's output, so there is no ``--json`` to decode.
    """
    flags = _wrapper_flags(
        as_=as_,
        module=module,
        ref=ref,
        dir=dir,
        var_file=var_file,
        var=var,
        strict=strict,
    )
    return run_atelier("apply", source, *flags, cwd=cwd, timeout=timeout)


def atelier_import(
    provider: str,
    *,
    cwd: Path | str,
    source: str,
    module: str | None = None,
    ref: str | None = None,
    dir: Path | str | None = None,
    var_file: Path | str | list[Path | str] | None = None,
    query_var: dict[str, str] | None = None,
    timeout: float | None = 3600.0,
) -> ImportResult:
    """``atelier import``, with its ``--json`` payload decoded.

    Recovers live resources into Terraform state; it cannot change infrastructure.
    A run that matched nothing still exits 0, so read the result rather than
    relying on the absence of an exception.

    ``query_var`` feeds the query file's own variables, such as ``model_uuid``, and
    is separate from ``var_file``, which feeds the module — conflating them is how
    a model UUID ends up in ``main.tf``. A type that errors during the query is
    left non-fatal: it is usually environmental, and the skipped ones are reported
    in ``ImportResult.skipped_types``.
    """
    flags = ["--source", source, "--yes", "--json"]
    if module is not None:
        flags += ["--module", module]
    if ref is not None:
        flags += ["--ref", ref]
    if dir is not None:
        flags += ["--dir", str(dir)]
    for bundle in _as_list(var_file):
        flags += ["--var-file", str(bundle)]
    for key, value in (query_var or {}).items():
        flags += ["--query-var", f"{key}={value}"]

    return decode_import(run_atelier("import", provider, *flags, cwd=cwd, timeout=timeout).stdout)


def atelier_ls(*, cwd: Path | str) -> tuple[ModuleInfo, ...]:
    """``atelier ls``: the modules a wrapper declares. Empty when there is no
    ``main.tf``, which the CLI also reports as a success.
    """
    return decode_ls(run_atelier("ls", "--json", cwd=cwd).stdout)


def _hcl_value(value: object) -> str:
    """Render a Python value as an HCL expression.

    A bool is checked before an int because ``isinstance(True, int)`` is True in
    Python, and HCL needs ``true``, not ``1``.
    """
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, (int, float)):
        return str(value)
    if isinstance(value, str):
        return json.dumps(value)
    if value is None:
        return "null"
    if isinstance(value, dict):
        return "{ " + ", ".join(f"{k} = {_hcl_value(v)}" for k, v in value.items()) + " }"
    if isinstance(value, (list, tuple)):
        return "[" + ", ".join(_hcl_value(v) for v in value) + "]"
    raise TypeError(f"cannot render {value!r} as HCL")


def write_tfvars(directory: Path | str, name: str, values: dict) -> Path:
    """Write a ``<name>.tfvars`` bundle and return its path.

    An ordinary Terraform variable file, so anything that runs the wrapper can
    read it — which keeps a CI job's inputs next to the code that derives them.
    Raises ``TypeError`` on a value with no unambiguous HCL rendering, since
    Terraform would otherwise have to guess.
    """
    path = Path(directory) / f"{name}.tfvars"
    path.write_text("".join(f"{k} = {_hcl_value(v)}\n" for k, v in values.items()))
    logger.info("wrote %s with variables: %s", path, ", ".join(values))
    return path


class TfDirManager:
    """Runs Terraform against a wrapper directory, to check what Atelier wrote.

    Verification only. The wrapper *is* the artifact (ADR-0001), so this latches
    onto the directory Atelier wrote rather than staging a ``.tf`` of its own, and
    asks the three questions Atelier has no command for: is it valid, what is in
    state, and would a plan change anything. Editing the wrapper afterwards needs
    nothing here but :meth:`init` and :meth:`validate` — the wrapper is yours.
    """

    def __init__(self) -> None:
        self.dir: str = ""

    @property
    def tf_cmd(self) -> str:
        if not self.dir:
            raise RuntimeError("TfDirManager has not latched onto a directory yet")
        return f"terraform -chdir={self.dir}"

    def latch(self, directory: Path | str) -> None:
        """Point the manager at an existing wrapper directory."""
        self.dir = str(directory)

    def _run(self, *args: str) -> subprocess.CompletedProcess[str]:
        cmd = shlex.split(f"{self.tf_cmd} {shlex.join(args)}")
        logger.info("running: %s", shlex.join(cmd))
        return subprocess.run(
            cmd, stdin=subprocess.DEVNULL, check=True, capture_output=True, text=True
        )

    def init(self, *extra_args: str) -> None:
        """Run ``terraform init -upgrade`` in the latched wrapper directory."""
        self._run("init", "-upgrade", *extra_args)

    def validate(self) -> None:
        """Run ``terraform validate`` in the latched wrapper directory."""
        self._run("validate")

    def state_list(self) -> list[str]:
        """The resource addresses currently in state. Complements
        :meth:`plan_changes`, which reports what a plan *would* change and is
        therefore empty once the resources exist.
        """
        return [line for line in self._run("state", "list").stdout.splitlines() if line.strip()]

    def plan_changes(self) -> list[tuple[str, str, list[str]]]:
        """The plan's managed-resource changes as ``(address, type, actions)``.

        Runs ``plan -out`` then ``show -json``, so the result is machine-readable;
        data sources and no-ops are omitted. Separating these addresses is how a
        caller tells real infrastructure drift from ``terraform_data``
        bookkeeping, which has no live counterpart and can never be imported. An
        import report gives counts, but a drift assertion needs addresses.
        """
        plan_file = os.path.join(self.dir, ".atelier-integration.tfplan")
        try:
            self._run("plan", f"-out={plan_file}", "-input=false", "-no-color")
            shown = json.loads(self._run("show", "-json", plan_file).stdout)
        finally:
            if os.path.exists(plan_file):
                os.remove(plan_file)

        changes: list[tuple[str, str, list[str]]] = []
        for rc in shown.get("resource_changes") or []:
            if rc.get("mode") != "managed":
                continue
            actions = (rc.get("change") or {}).get("actions") or []
            if actions == ["no-op"]:
                continue
            changes.append((rc.get("address", ""), rc.get("type", ""), actions))
        return changes


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