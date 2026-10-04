# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""`atelier import` round-trip against a live COS-Lite deployment.

Flow under test — the end-to-end value of the provider registry
(internal/importer/providers), exercised with non-default module inputs:

1. Create a temporary Juju model (Jubilant).
2. Bootstrap a COS-Lite wrapper pinned to ``--ref``, non-interactively, from two
   ``.tfvars`` bundles: a local ``ci`` file pinning the model and
   ``internal_tls = false`` explicitly, then the module's own ``no-ingress``
   preset, resolved by name from the clone, which disables every ingress
   integration (so no Traefik). Later files win.
3. ``terraform init`` + ``apply`` to deploy COS-Lite into the model.
4. Delete the Terraform state, leaving the live deployment orphaned.
5. Run ``atelier import`` with the *same* ``--ref`` and ``--var-file``, letting it
   rebuild the state from live resources — detection, discovery, matching,
   import-ID construction and the post-import steps all run through the
   registered Juju provider.
6. Assert the run reported matches and imports, that the state file was
   repopulated, and — the strongest check — that a following ``terraform plan``
   finds nothing to change for the core resources the module manages
   (applications, integrations, offers). Drift in other types
   (``terraform_data``, secrets) is tolerated; see ``PRESERVED_TYPES``.

``--query-var model_uuid`` is required: the Juju list resources for
applications and integrations carry a required ``model_uuid`` config block, so
without it only ``juju_model``/``juju_offer`` are queryable.

The ``ci`` bundle is a local path; the ``no-ingress`` bundle is the module's own
preset, committed to the upstream repo, so a preset by name exercises resolution
against the clone.

This is deliberately the same recipe as a user's disaster-recovery flow:
``add`` to author the wrapper, then ``import`` to recover state from a live
model.
"""

import json

import jubilant
import pytest

from helpers import (
    atelier_add,
    atelier_apply,
    atelier_import,
    wait_for_active_idle_without_error,
    write_tfvars,
)

COS_REPO = "https://github.com/canonical/observability-stack.git"
COS_MODULE = "terraform/cos-lite"
COS_REF = "main"
# The module's own preset, resolved by name from the clone (upstream discovery).
COS_PRESET = "no-ingress"

# The core resources `atelier import` must recover and reproduce exactly: the
# applications, the relations between them, and the offers they expose.
#
# Other types are allowed to drift without failing the test. `terraform_data`
# has no live object to import at all (Atelier excludes it from import
# candidates for that reason), and provider-generated resources such as
# `juju_secret` carry identifiers that a plan can never satisfy from imported
# state. Asserting on the core set keeps the check meaningful without turning
# every benign re-creation into a failure.
PRESERVED_TYPES = frozenset({"juju_application", "juju_integration", "juju_offer"})

# Types COS-Lite may create that the test deliberately does not require to
# round-trip. Classifying them explicitly means the completeness check below
# fails — rather than silently passing — if COS-Lite ever starts creating a
# type nobody has decided about.
DRIFT_TYPES = frozenset({"terraform_data", "juju_secret", "juju_access_secret"})


def _hcl_block(text: str, name: str) -> str:
    """Return the `{...}` body of `name = {...}` in `text`, brace-balanced.

    A plain `split("}", 1)` truncates at the first closing brace, which is wrong
    for a block whose values are themselves objects. This walks the braces so
    nested values are included, and skips quoted strings so a `}` inside one
    does not end the block early.
    """
    start = text.index(f"{name} = {{") + len(f"{name} = ")
    depth = 0
    in_str = False
    escaped = False
    for i, ch in enumerate(text[start:], start):
        if in_str:
            if escaped:
                escaped = False
            elif ch == "\\":
                escaped = True
            elif ch == '"':
                in_str = False
            continue
        if ch == '"':
            in_str = True
        elif ch == "{":
            depth += 1
        elif ch == "}":
            depth -= 1
            if depth == 0:
                return text[start + 1 : i]
    raise AssertionError(f"unbalanced braces in {name} block")


@pytest.mark.cloud
def test_import_cos_lite_roundtrip(tf_manager, juju: jubilant.Juju, tmp_path):
    # GIVEN a running Juju model
    model_uuid = juju.show_model(juju.model).model_uuid

    # AND a fresh directory for Atelier to author a wrapper into
    wrapper_dir = tmp_path

    # AND a bundle describing the deployment for that model, kept beside the
    # wrapper directory: the wrapper directory itself stays empty for `--dir`.
    write_tfvars(
        wrapper_dir.parent,
        "ci",
        {
            "model": {"uuid": model_uuid, "name": juju.model},
            "internal_tls": False,
        },
    )

    # WHEN Atelier bootstraps the module, non-interactively, pinned to --ref
    # and configured from both bundles: the explicit ci values, then the
    # module's own no-ingress preset.
    added = atelier_add(
        COS_REPO,
        module=COS_MODULE,
        ref=COS_REF,
        dir=".",
        cwd=wrapper_dir,
        var_file=["ci", COS_PRESET],
    )

    # THEN the wrapper went where the test prepared it, at the subdirectory the
    # repository puts the module in, and main.tf has the bundle values written
    # through with every ingress component switched off
    assert added.wrapper == wrapper_dir
    assert added.module.module_path == COS_MODULE
    assert added.module.ref == COS_REF
    main_tf = (wrapper_dir / "main.tf").read_text()
    assert f'uuid = "{model_uuid}"' in main_tf
    assert "internal_tls = false" in main_tf
    ingress_block = _hcl_block(main_tf, "ingress")
    assert "= false" in ingress_block
    assert "= true" not in ingress_block

    # AND the module is deployed. `apply` is `add` plus `terraform init` and
    # `apply`, so Terraform needs no separate invocation here.
    atelier_apply(
        COS_REPO,
        module=COS_MODULE,
        ref=COS_REF,
        dir=".",
        cwd=wrapper_dir,
        var_file=["ci", COS_PRESET],
    )

    # THEN the model settles active and idle
    wait_for_active_idle_without_error(juju)

    # AND the state is deleted, orphaning the live deployment — after first
    # recording what apply created, so the test can flag any COS-Lite resource
    # type it has not classified
    state_file = wrapper_dir / "terraform.tfstate"
    assert state_file.exists(), "apply should have produced terraform.tfstate"
    applied = json.loads(state_file.read_text())
    applied_types = {
        r["type"]
        for r in applied.get("resources", [])
        if r.get("mode", "managed") == "managed" and r.get("instances")
    }
    unclassified = applied_types - PRESERVED_TYPES - DRIFT_TYPES
    assert not unclassified, (
        "COS-Lite created resource type(s) this test does not classify: "
        f"{sorted(unclassified)}. Add each to PRESERVED_TYPES (must round-trip) "
        "or DRIFT_TYPES (may drift)."
    )
    state_file.unlink()
    (wrapper_dir / "terraform.tfstate.backup").unlink(missing_ok=True)

    # WHEN Atelier imports the live deployment back into a fresh state, with
    # the same --ref/--var-file bundles and the model UUID as a query variable
    result = atelier_import(
        "juju",
        cwd=wrapper_dir,
        source=COS_REPO,
        module=COS_MODULE,
        ref=COS_REF,
        dir=".",
        var_file=["ci", COS_PRESET],
        query_var={"model_uuid": model_uuid},
    )

    # THEN live objects were matched to module addresses and imported
    assert result.matched, "nothing matched the module's resources"
    assert result.imported, f"nothing was imported: {result.unresolved}"
    assert state_file.exists(), "import should have repopulated terraform.tfstate"

    # AND the core resource types really were recovered (guards against a
    # vacuous pass where nothing matched)
    imported = json.loads(state_file.read_text())
    imported_types = {r["type"] for r in imported.get("resources", [])}
    missing = PRESERVED_TYPES - imported_types
    assert not missing, f"import did not recover core resource types: {sorted(missing)}"

    # AND the strongest check: a plan against the imported state finds nothing
    # to change for those core resources. Drift in other types is tolerated —
    # see PRESERVED_TYPES for why.
    #
    # This is the one thing left that needs Terraform directly. `atelier` has no
    # command that answers "would a plan change anything?", and it needs the
    # addresses, not the counts an import --dry-run preview gives.
    tf_manager.latch(wrapper_dir)
    changes = tf_manager.plan_changes()
    drifted = sorted(c for c in changes if c[1] in PRESERVED_TYPES)
    assert not drifted, (
        "import did not preserve these core resources "
        f"(address, type, actions): {drifted}"
    )
