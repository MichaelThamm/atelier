# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Typed results decoded from ``atelier --json`` output.

Atelier writes a machine-readable payload for the commands whose result is data,
so a test can assert on what happened instead of matching sentences in the
human-facing report. The CLI's own report is still on stderr, so a failed run
explains itself.
"""

import json
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, cast

#: The oldest Atelier that reports ``--json``, as ``(major, minor)``. Enforced by
#: the ``atelier_bin`` fixture, which skips rather than letting every command fail
#: on an unknown flag.
MINIMUM_ATELIER = (0, 20)


class PayloadError(ValueError):
    """A command succeeded and its ``--json`` output could not be decoded.

    Only possible if Atelier's payload schema changed. Raised rather than
    returning empty results, which would let a test pass on a run that did
    nothing.
    """


@dataclass(frozen=True)
class ModuleInfo:
    """One ``module`` block in a wrapper's ``main.tf``.

    ``name`` is the name that reached ``main.tf``, which is not always the one
    passed to ``--as``: ``cos-lite`` is not a valid HCL identifier, so it is
    written ``cos_lite``. ``source`` excludes the ``//subdir`` and ``?ref=``
    query, which ``module_path`` and ``ref`` carry instead.
    """

    name: str
    source: str
    module_path: str | None = None
    ref: str | None = None


@dataclass(frozen=True)
class AddResult:
    """The outcome of ``atelier add``.

    ``wrapper`` answers "where did my wrapper go?", which is worth knowing when
    ``--dir`` was not given and the directory came from the module name.
    ``blocks`` is every block afterwards, so a composing test needs no second
    ``ls``.
    """

    wrapper: Path
    module: ModuleInfo
    blocks: tuple[str, ...] = ()


@dataclass(frozen=True)
class PlannedResource:
    """A module resource Atelier could not account for."""

    address: str
    type: str


@dataclass(frozen=True)
class UnmatchedLiveGroup:
    """Live objects no module resource claimed, grouped by resource type."""

    type: str
    count: int
    names: tuple[str, ...]


@dataclass(frozen=True)
class ImportResult:
    """The outcome of ``atelier import``.

    A successful run does not mean anything was imported: ``atelier import``
    exits ``0`` when nothing matched, so read :attr:`imported` or
    :attr:`matched_nothing` rather than trusting the exit code.

    :attr:`already_in_state` (nothing to do, a re-run) and :attr:`matched_nothing`
    (no live object matched anything the module wants — usually a wrong model UUID
    or a ``--query-var`` that never reached the query) look identical in the shape
    of a run and mean opposite things, which is why they are reported apart.

    :attr:`unresolved` and :attr:`unmatched_module` are both resources an apply
    would *create* beside live infrastructure.
    """

    matched: dict[str, str] = field(default_factory=dict)
    imported: tuple[str, ...] = ()
    already_in_state: bool = False
    matched_nothing: bool = False
    unresolved: tuple[PlannedResource, ...] = ()
    unmatched_module: tuple[PlannedResource, ...] = ()
    unmatched_live: tuple[UnmatchedLiveGroup, ...] = ()
    queried_types: tuple[str, ...] = ()
    skipped_types: tuple[str, ...] = ()
    dry_run: bool = False
    query_file: Path | None = None
    terraform_version: str = ""


def _payload(stdout: str, command: str) -> dict[str, Any]:
    """Unwrap the envelope Atelier wraps every payload in."""
    try:
        data: Any = json.loads(stdout)["data"]
    except (json.JSONDecodeError, KeyError, TypeError) as exc:
        raise PayloadError(f'atelier {command} --json produced no readable payload') from exc
    if not isinstance(data, dict):
        raise PayloadError(f'atelier {command} --json payload is not an object')
    return cast("dict[str, Any]", data)


def decode_module(raw: dict[str, Any]) -> ModuleInfo:
    """Decode one module block."""
    return ModuleInfo(
        name=raw["name"],
        source=raw["source"],
        module_path=raw.get("modulePath"),
        ref=raw.get("ref"),
    )


def decode_add(stdout: str) -> AddResult:
    """Decode the ``atelier add`` payload."""
    data = _payload(stdout, "add")
    return AddResult(
        wrapper=Path(data["wrapper"]),
        module=decode_module(data["added"]),
        blocks=tuple(data.get("blocks", ())),
    )


def decode_ls(stdout: str) -> tuple[ModuleInfo, ...]:
    """Decode the ``atelier ls`` payload. Empty for a directory that is not a
    wrapper, which the CLI also reports as a success.
    """
    data = _payload(stdout, "ls")
    return tuple(decode_module(m) for m in data.get("modules", ()))


def decode_import(stdout: str) -> ImportResult:
    """Decode the ``atelier import`` payload."""
    data = _payload(stdout, "import")
    return ImportResult(
        matched=dict(data.get("matched", {})),
        imported=tuple(data.get("imported", ())),
        already_in_state=data.get("alreadyInState", False),
        matched_nothing=data.get("matchedNothing", False),
        unresolved=tuple(
            PlannedResource(r["address"], r["type"]) for r in data.get("unresolved", ())
        ),
        unmatched_module=tuple(
            PlannedResource(r["address"], r["type"]) for r in data.get("unmatchedModule", ())
        ),
        unmatched_live=tuple(
            UnmatchedLiveGroup(g["type"], g["count"], tuple(g.get("names", ())))
            for g in data.get("unmatchedLive", ())
        ),
        queried_types=tuple(data.get("queriedTypes", ())),
        skipped_types=tuple(data.get("skippedTypes", ())),
        dry_run=data.get("dryRun", False),
        query_file=Path(data["queryFile"]) if data.get("queryFile") else None,
        terraform_version=data.get("terraformVersion", ""),
    )
