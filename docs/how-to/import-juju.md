# Comparing module versions before you upgrade

**(Re)construct Terraform state from a live deployment by pointing Atelier at the upstream module that manages it.**

---

Lost your Terraform state, or never had one to begin with? With `atelier import`, you can reconstruct it from a live deployment by pointing Atelier at the upstream module that manages it.

## What `atelier import` does

`atelier import juju` clones the upstream module, writes an Atelier wrapper, discovers live resources via `terraform query`, matches them to the module's resource addresses, and runs `terraform import` for each match. The result is a wrapper directory whose Terraform state reflects your running infrastructure, ready to manage with normal Atelier operations.

## Use cases

1. **Bundle to Terraform migration**. A COS Lite deployment originally deployed via a Juju bundle is now managed through Terraform, but the bundle's resource graph and the Terraform module's resource graph don't map 1:1.
2. **Lost state file**. You deployed COS Lite with Terraform, deleted or lost the `terraform.tfstate`, and need to continue managing the existing deployment.

> To avoid data loss, `juju_application` resources must not show `replace` or `create` actions in the Terraform plan.

### Importing a full deployment

Given a running [Canonical Observability Stack (COS)](https://github.com/canonical/observability-stack/tree/main/terraform/cos) deployment, note the model UUID from `juju models`, then run:

```bash
atelier module add https://github.com/canonical/observability-stack.git \
    --module terraform/cos-lite \
    --ref track/3.0  # plan and apply
rm terraform.*  # remove Terraform state
atelier import juju \
    --source https://github.com/canonical/observability-stack.git \
    --module terraform/cos \
    --ref track/3.0 \
    --query-var model_uuid=2af837d8-f470-488e-84cb-c588a39732d8 \
    --preset cos-vars
```

The `--source`, `--module`, and `--ref` flags tell Atelier which upstream module to clone. The `--query-var` input (here, `model_uuid`) is required by the Juju provider's query engine. The `--var` or `--preset` flags supply variable values the module needs.

Atelier queries the live deployment and reports what it matched. For COS, `98/118` resources are automatically importable:

```
Matched 98 resource(s):
  module.cos.module.catalogue.juju_application.catalogue  (import ID: 2af837d8-…:catalogue)
  module.cos.module.grafana.juju_application.grafana      (import ID: 2af837d8-…:grafana)
  module.cos.juju_integration.ingress["loki"]             (import ID: 2af837d8-…:traefik:ingress:loki:ingress)
  …
```

Some module resources have zero or ambiguous live matches and must be imported manually. Live resources not declared by the module are left alone. From this point forward, you manage the deployment with normal Atelier operations. Running `atelier` in the wrapper directory opens the TUI with the imported state loaded:

```
Module: cos@track/3.0  ✓ valid  ⚠ 6 check warning(s)
```

The variables pane shows the module's inputs, and the plan view confirms the import has a small delta against the live deployment:

```
Plan: 6 to add, 7 to change, 0 to destroy.  |  State: 111 resource(s) across 25 modules
```

The six resources to add are `juju_access_secret` objects that `terraform query` cannot resolve (zero or ambiguous matches). The seven changes are attribute drift, mostly where the module's defaults diverge slightly from the live state. Neither category represents a real infrastructure change, which is exactly what you want to see after an import: the state is close enough that a plan is a formality, not a warning.

### Importing a partial deployment

Similar to the `Importing a full deployment` section, we can also import a partially complete module. In this example, Loki is deployed with Atelier:

```bash
atelier module add https://github.com/canonical/loki-operators.git  # plan and apply
rm terraform.*  # remove Terraform state
juju remove-application --destroy-storage s3-integrator  # create a partially complete deployment
atelier import juju \                       
  --source https://github.com/canonical/loki-operators.git \
  --query-var model_uuid=a3592360-792b-412d-814f-8a29e82191b6 \
  --var channel=dev/edge \
  --var model_uuid=a3592360-792b-412d-814f-8a29e82191b6 \
  --preset loki-vars
```

which completes with:
```
⠹ Matching live resources to module addresses…
Injected model UUID a3592360-792b-412d-814f-8a29e82191b6 into wrapper.

Unmatched module resources (no single live object identified): 3
  ? module.loki_operators.juju_access_secret.loki_s3_secret_access
  ? module.loki_operators.juju_application.s3_integrator
  ? module.loki_operators.juju_integration.coordinator_to_s3_integrator

Imported 5 resource(s) into state:
  ✓ module.loki_operators.juju_secret.loki_s3_credentials_secret
  ✓ module.loki_operators.module.loki_backend.juju_application.loki_worker
  ✓ module.loki_operators.module.loki_coordinator.juju_application.loki_coordinator
  ✓ module.loki_operators.module.loki_read.juju_application.loki_worker
  ✓ module.loki_operators.module.loki_write.juju_application.loki_worker
```

and correctly identifies the `juju_application.s3_integrator` (and its associated resources) as not importable, since they were manually removed. When we open the wrapper again with `atelier`, we can plan and apply the state to continue operations as if we never lost the state:
```
Model  Controller  Cloud/Region  Version  SLA          Timestamp
loki   k8s         k8s           3.6.23   unsupported  15:18:50-04:00

App                 Version  Status  Scale  Charm                 Channel     Rev  Address         Exposed  Message
loki                         active      1  loki-coordinator-k8s  3.7/stable   88  10.152.183.164  no       
loki-backend        3.7.1    active      1  loki-worker-k8s       3.7/stable  106  10.152.183.248  no       backend ready.
loki-read           3.7.1    active      1  loki-worker-k8s       3.7/stable  106  10.152.183.252  no       read ready.
loki-s3-integrator           active      1  s3-integrator         2/stable    544  10.152.183.59   no       
loki-write          3.7.1    active      1  loki-worker-k8s       3.7/stable  106  10.152.183.229  no       write ready.

Unit                   Workload  Agent  Address     Ports  Message
loki-backend/0*        active    idle   10.1.0.167         backend ready.
loki-read/0*           active    idle   10.1.0.91          read ready.
loki-s3-integrator/0*  active    idle   10.1.0.239         
loki-write/0*          active    idle   10.1.0.82          write ready.
loki/0*                active    idle   10.1.0.134         

Integration provider               Requirer                         Interface     Type     Message
loki-s3-integrator:s3-credentials  loki:s3                          s3            regular  
loki-s3-integrator:status-peers    loki-s3-integrator:status-peers  status_peers  peer     
loki:loki-cluster                  loki-backend:loki-cluster        loki_cluster  regular  
loki:loki-cluster                  loki-read:loki-cluster           loki_cluster  regular  
loki:loki-cluster                  loki-write:loki-cluster          loki_cluster  regular  
loki:loki-peers                    loki:loki-peers                  loki_peers    peer
```

## Next steps

With state imported, you can use Atelier like any other wrapper: edit variables in the TUI, plan to check the diff, apply to converge, or bump the module ref to upgrade the deployment. The wrapper is a normal Atelier wrapper; the import just gave it a head start.
