# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""Reading an Atelier command's ``--json`` output.

Every payload sits in one envelope::

    {"schema": 1, "command": "ls", "data": { ... }}

Unwrapping ``data`` is the whole client — the fields inside it are the contract,
and ``cmd/atelier/json_test.go`` pins their exact bytes. Read them as they arrive
(``result["matchedNothing"]``) rather than through a mirror type: Atelier renaming
a field then raises ``KeyError`` at the test that uses it, instead of quietly
decoding to a default that makes an assertion pass for the wrong reason.
"""

import json
from typing import Any


def payload(stdout: str) -> dict[str, Any]:
    """Return one command's ``data`` object, given what it wrote to stdout.

    Raises:
        json.JSONDecodeError: stdout is not JSON. Usually the command was run
            without ``--json``, or rejected it — ``atelier apply`` does.
        KeyError: The output has no envelope, so it is not an Atelier payload.
    """
    return json.loads(stdout)["data"]