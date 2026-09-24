# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Helpers for Atelier's Juju + Terraform integration tests."""

import logging
import os
import shlex
import subprocess
from pathlib import Path
from typing import Optional

import jubilant
import yaml

logger = logging.getLogger(__name__)

# The S3 test backend. seaweedfs-k8s serves its S3 API on the unit address at
# port 8333. The application name is arbitrary, so it is discoverable by charm
# name rather than assumed to be any particular label.
SEAWEEDFS_CHARM = "seaweedfs-k8s"
SEAWEEDFS_CHANNEL = "latest/edge"
SEAWEEDFS_DEFAULT_APP = os.environ.get("SEAWEEDFS_APP", "swfs")
S3_PORT = int(os.environ.get("SEAWEEDFS_S3_PORT", "8333"))


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


def run_atelier(wrapper_dir, atelier_bin: str, *args: str, env: Optional[dict] = None) -> None:
    """Run the Atelier CLI in ``wrapper_dir`` with stdin pinned to ``/dev/null``.

    Pinning stdin to ``/dev/null`` is what makes ``module add`` non-blocking:
    Atelier detects the non-terminal, applies any ``--preset`` and skips the
    TUI instead of trying (and failing) to open one.
    """
    cmd = [atelier_bin, *args]
    logger.info("running: %s", " ".join(shlex.quote(c) for c in cmd))
    with open(os.devnull, "rb") as devnull:
        subprocess.run(
            cmd,
            cwd=wrapper_dir,
            stdin=devnull,
            env={**os.environ, **(env or {})},
            check=True,
        )


def deploy_seaweedfs(
    juju: jubilant.Juju,
    app: Optional[str] = None,
    channel: Optional[str] = None,
    timeout: int = 20 * 60,
) -> str:
    """Ensure a seaweedfs-k8s S3 backend is deployed and active, and return its app name.

    Idempotent: if the app (or any seaweedfs-k8s app) is already present it is
    reused rather than redeployed, so the test works whether the S3 backend was
    deployed here or by the caller.
    """
    channel = channel or os.environ.get("SEAWEEDFS_CHANNEL", SEAWEEDFS_CHANNEL)
    if app is None:
        try:
            app = find_seaweedfs_app(juju)
            logger.info("reusing existing seaweedfs app %r", app)
        except LookupError:
            app = SEAWEEDFS_DEFAULT_APP
    if app not in juju.status().apps:
        logger.info("deploying %s as %r from %s", SEAWEEDFS_CHARM, app, channel)
        juju.deploy(SEAWEEDFS_CHARM, app=app, channel=channel)
    juju.wait(lambda status: jubilant.all_active(status, app), timeout=timeout, delay=5)
    return app


def find_seaweedfs_app(juju: jubilant.Juju, app: Optional[str] = None) -> str:
    """Return the name of the seaweedfs-k8s application in the model.

    If ``app`` is given it is returned unchecked. Otherwise the model is
    searched by charm name, so the application label — ``sw``, ``swfs``,
    ``seaweedfs`` — does not matter.
    """
    if app:
        return app
    apps = juju.status().apps
    matches = [name for name, status in apps.items() if _is_seaweedfs(status)]
    if not matches:
        raise LookupError(
            f"no {SEAWEEDFS_CHARM} application found in model {juju.model!r}; "
            f"apps present: {sorted(apps)}"
        )
    return matches[0]


def _is_seaweedfs(app_status) -> bool:
    """Whether an application status is a seaweedfs-k8s deployment.

    Matches on charm name, tolerating a full charm URL (``ch:amd64/…/name``)
    in the ``charm`` field.
    """
    return any(
        candidate and SEAWEEDFS_CHARM in candidate
        for candidate in (app_status.charm_name, app_status.charm)
    )


def get_unit_address(juju: jubilant.Juju, app: str, unit_no: int = 0) -> str:
    """Return the address of ``<app>/<unit_no>`` from the model status."""
    units = juju.status().apps[app].units
    unit = units.get(f"{app}/{unit_no}") or next(iter(units.values()))
    return unit.address


def get_s3_endpoint(
    juju: jubilant.Juju,
    app: Optional[str] = None,
    port: int = S3_PORT,
) -> str:
    """Return the S3 endpoint of the seaweedfs app in the model.

    Mirrors ``juju status … | yq .applications.<app>.units."<app>/0".address``
    but works for any application name and on a Jubilant-managed model.
    """
    app = find_seaweedfs_app(juju, app)
    return f"http://{get_unit_address(juju, app)}:{port}"


def write_preset_file(
    directory,
    model_uuid: str,
    s3_endpoint: str,
    *,
    s3_access_key: str = "placeholder",
    s3_secret_key: str = "placeholder",
    channel: Optional[str] = None,
    name: str = "cos-s3.yaml",
) -> Path:
    """Write a runtime preset file for loki-operators and return its path.

    ``model_uuid`` and ``s3_endpoint`` are runtime values — the model UUID is
    model-specific and the S3 endpoint is the live seaweedfs unit address — so
    neither can live in a checked-in example. seaweedfs-k8s runs without auth,
    so the credentials are placeholders.
    """
    sets = {
        "channel": channel or os.environ.get("LOKI_CHANNEL", "dev/edge"),
        "model_uuid": model_uuid,
        "s3_access_key": s3_access_key,
        "s3_secret_key": s3_secret_key,
        "s3_endpoint": s3_endpoint,
    }
    path = Path(directory) / name
    path.write_text(yaml.safe_dump(sets, sort_keys=False))
    logger.info("wrote preset %s: %s", path, sets)
    return path


def wait_for_active_idle_without_error(juju: jubilant.Juju, timeout: int = 60 * 45) -> None:
    """Wait for every unit in the model to be active and every agent idle."""
    timeout = int(os.environ.get("INTEGRATION_TIMEOUT", timeout))
    print(f"\nwaiting for the model ({juju.model}) to settle ...\n")
    juju.wait(jubilant.all_active, delay=10, timeout=timeout)
    print("\nwaiting for agents idle ...\n")
    juju.wait(
        jubilant.all_agents_idle,
        delay=10,
        timeout=timeout,
        error=jubilant.any_error,
    )
