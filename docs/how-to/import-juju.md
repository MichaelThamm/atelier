# Comparing module versions before you upgrade

**Inspect changes when you modify a Terraform module, without leaving your terminal.**

---

Lost your Terraform state, or never had one to begin with? With `atelier import`, you can reconstruct it from a live deployment by pointing Atelier at the upstream module that manages it.

## What `atelier import` does

`atelier import juju` clones the upstream module, writes an Atelier wrapper, discovers live resources via `terraform query`, matches them to the module's resource addresses, and runs `terraform import` for each match. The result is a wrapper directory whose Terraform state reflects your running infrastructure, ready to manage with normal Atelier operations.

## Importing a COS deployment

Given a running [Canonical Observability Stack (COS)](https://github.com/canonical/observability-stack/tree/main/terraform/cos) deployment, note the model UUID from `juju models`, then run:

```bash
mkdir import-target && cd import-target

atelier import juju \
    --source https://github.com/canonical/observability-stack.git \
    --module terraform/cos \
    --ref track/3.0 \
    --query-var model_uuid=2af837d8-f470-488e-84cb-c588a39732d8 \
    --preset cos-vars
```

The `--source`, `--module`, and `--ref` flags tell Atelier which upstream module to clone. The `--query-var` input (here, `model_uuid`) is required by the Juju provider's query engine. The `--var` or `--preset` flags supply variable values the module needs.

## Use cases

The key sign that an import is needed (and that it worked) is the plan after import: `juju_application` resources should not show `replace` or `create` actions. If the plan proposes replacing applications that are already running, something is wrong with the import. A clean import means the state matches reality closely enough that the plan only shows attribute drift or minor additions.

Common scenarios:

1. **Bundle to Terraform migration.** A COS Lite deployment originally stood up via a Juju bundle is now managed through Terraform, but the bundle's resource graph and the Terraform module's resource graph don't map 1:1. Importing lets you start from the live deployment and let Terraform take over without redeploying.
2. **Lost state file.** You deployed COS Lite with Terraform, deleted or lost the `terraform.tfstate`, and need to continue managing the existing deployment. Importing reconstructs the state so you can plan and apply again without tearing anything down.

## After the import

Atelier queries the live deployment and reports what it matched. For COS, 98 out of 118 resources are automatically importable:

```
Matched 98 resource(s):
  module.cos.module.catalogue.juju_application.catalogue  (import ID: 2af837d8-…:catalogue)
  module.cos.module.grafana.juju_application.grafana      (import ID: 2af837d8-…:grafana)
  module.cos.juju_integration.ingress["loki"]              (import ID: 2af837d8-…:traefik:ingress:loki:ingress)
  …
```

Four module resources (like `juju_model` and `juju_access_secret`) have zero or ambiguous live matches and must be imported manually. Twenty-nine live resources not declared by the module are left alone.

The full match list is printed to the terminal during import. The important thing is the ratio: `98/118` is typical for a Juju-backed module, and the imported state structure matches the upstream module exactly.

From this point forward, you manage the deployment with normal Atelier operations. Running `atelier` in the wrapper directory opens the TUI with the imported state loaded:

```
Module: cos@track/3.0  ✓ valid  ⚠ 6 check warning(s)
```

The variables pane shows the module's inputs, and the plan view confirms the import has a small delta against the live deployment:

```
Plan: 6 to add, 7 to change, 0 to destroy.  |  State: 111 resource(s) across 25 modules
```

The six resources to add are `juju_access_secret` objects that `terraform query` cannot resolve (zero or ambiguous matches). The seven changes are attribute drift, mostly where the module's defaults diverge slightly from the live state. Neither category represents a real infrastructure change, which is exactly what you want to see after an import: the state is close enough that a plan is a formality, not a warning.

## Next steps

With state imported, you can use Atelier like any other wrapper: edit variables in the TUI, plan to check the diff, apply to converge, or press `R` to upgrade the module ref and compare versions. The wrapper is a normal Atelier wrapper; the import just gave it a head start.
