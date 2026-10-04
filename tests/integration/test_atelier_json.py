# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""The ``--json`` contract as these helpers read it.

``cmd/atelier/json_test.go`` pins the payload Atelier emits; this pins the other
half — that :mod:`atelier_json` reads every field the import test relies on. A
misspelled key decodes to a default rather than raising, so a mapping that drifts
lets the COS-Lite round-trip pass on a run that imported nothing.

The import command itself is only exercised on the cloud tier, because it queries
a live Juju model.
"""

import json

import pytest

from atelier_json import PayloadError, decode_import


def _payload(**data) -> str:
    return json.dumps({"schema": 1, "command": "import", "data": data})


def test_import_payload_fields_are_all_read():
    # GIVEN a fully populated import payload
    stdout = _payload(
        matched={"juju_offer.cos-lite": "cos-lite:admin/cos-lite.alertmanager"},
        imported=["juju_offer.cos-lite"],
        alreadyInState=False,
        matchedNothing=False,
        unresolved=[{"address": "juju_model.cos_lite", "type": "juju_model"}],
        unmatchedModule=[{"address": "juju_app.a", "type": "juju_application"}],
        unmatchedLive=[{"type": "juju_offer", "count": 6, "names": ["admin/cos-lite.grafana"]}],
        queriedTypes=["juju_model", "juju_offer"],
        skippedTypes=["juju_machine"],
        dryRun=True,
        importsFile="/tmp/wrapper/imports.tf",
        queryFile="/tmp/wrapper/atelier-import.tfquery.hcl",
        terraformVersion="1.16.5",
    )

    # THEN every field lands, under the name the test reads it by
    result = decode_import(stdout)
    assert result.matched == {"juju_offer.cos-lite": "cos-lite:admin/cos-lite.alertmanager"}
    assert result.imported == ("juju_offer.cos-lite",)
    assert result.already_in_state is False
    assert result.matched_nothing is False
    assert [(r.address, r.type) for r in result.unresolved] == [
        ("juju_model.cos_lite", "juju_model")
    ]
    assert [(r.address, r.type) for r in result.unmatched_module] == [
        ("juju_app.a", "juju_application")
    ]
    assert [(g.type, g.count, g.names) for g in result.unmatched_live] == [
        ("juju_offer", 6, ("admin/cos-lite.grafana",))
    ]
    assert result.queried_types == ("juju_model", "juju_offer")
    assert result.skipped_types == ("juju_machine",)
    assert result.dry_run is True
    assert result.query_file is not None and result.query_file.name.startswith("atelier-import")
    assert result.terraform_version == "1.16.5"


def test_a_matched_nothing_run_is_distinguishable():
    # GIVEN the payload for a run that matched nothing, which still exits 0
    stdout = _payload(matched={}, matchedNothing=True, unmatchedLive=[{"type": "juju_offer", "count": 6}])

    # THEN the decoder says so, so a caller cannot mistake it for a clean import
    result = decode_import(stdout)
    assert result.matched_nothing is True
    assert result.matched == {}
    assert result.imported == ()


def test_an_unreadable_payload_is_an_error():
    # WHEN the output is not a payload envelope
    # THEN decoding fails loudly rather than yielding empty results
    with pytest.raises(PayloadError):
        decode_import("atelier: no provider configured in /tmp/wrapper.\n")
    with pytest.raises(PayloadError):
        decode_import(json.dumps({"schema": 1, "command": "import"}))