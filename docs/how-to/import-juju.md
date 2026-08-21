# Reconstructing Terraform state from a live deployment

**(Re)construct Terraform state from a live deployment by pointing Atelier at the upstream module that manages it.**

---

Lost your Terraform state, or never had one to begin with? With `atelier import`, you can reconstruct it from a live deployment by pointing Atelier at the upstream module that manages it.

## What `atelier import` does

`atelier import juju` clones the upstream module, writes an Atelier wrapper, discovers live resources via `terraform query`, matches them to the module's resource addresses, and runs `terraform import` for each match. The result is a wrapper directory whose Terraform state reflects your running infrastructure, ready to manage with normal Atelier operations.

## Use cases

1. **Juju to Terraform migration**. A Juju client deployment to be managed by Terraform.
2. **Lost state file**. A Terraform deployment with a deleted or lost `terraform.tfstate`.

> To avoid data loss, `juju_application` resources must not show `replace` or `create` actions in the Terraform plan.

### Importing a full deployment

Given a running [Canonical Observability Stack (COS)](https://github.com/canonical/observability-stack/tree/main/terraform/cos) deployment, note the model UUID from `juju models`, then run:

```bash
atelier module add https://github.com/canonical/observability-stack.git \
    --module terraform/cos-lite \
    --ref track/3.0  # plan and apply
rm terraform.*  # remove Terraform state
atelier import juju \
    --query-var model_uuid=2af837d8-f470-488e-84cb-c588a39732d8
    --source https://github.com/canonical/observability-stack.git \
    --module terraform/cos-lite \
    --ref track/3.0 \
```

The `--source`, `--module`, and `--ref` flags tell Atelier which upstream module to clone. `--query-var model_uuid` is required by the Juju provider's query engine — it selects which model to enumerate.

### When you still need `--var` or `--preset`

Atelier cannot invent values for variables the module requires:

- **Variables with no default must be supplied.** Atelier omits them from the wrapper's `module {}` block entirely, so the run stops early — at the `terraform query` step, which is the first command to load the root module — and names every one at once:

  ```
  atelier: Required variables not set: channel, model_uuid, s3_access_key, s3_endpoint, s3_secret_key

  To fix this, supply them and re-run:
    --var channel=<value> --var model_uuid=<value> …
  ```

  Supply them with `--var`, a `--preset`, or by editing the wrapper.

- **The model UUID is not one of them.** You never need to pass it twice. If the module declares a variable with the same name as one of your `--query-var` values — as `loki-operators` does with its required `model_uuid` — Atelier seeds the module input from the query variable before anything runs, and says so:

  ```
  Using query variable(s) for module input(s): model_uuid
  ```

  A value you have already set is never overwritten. Modules that take the UUID under a different shape, such as COS Lite's `model = { uuid = optional(string) }`, are handled separately by deriving it from the discovered resources.
- **Variables that change the module's shape may need supplying.** Anything feeding a `count`, a `for_each`, or a resource name decides which addresses exist. If your deployment diverges from the module's defaults — an application renamed, a component disabled — the planned addresses will not line up with what is live. Atelier reports the discrepancy rather than guessing at it; see [Checking coverage first](#checking-coverage-first).
- **Values that only affect attributes do not need supplying.** Channels, revisions, config, constraints, unit counts and similar do not affect matching, and after import the state holds whatever is actually deployed. The plan diff then tells you exactly what to set, which is more reliable than guessing up front.

COS Lite gives every variable a default, which is why the command above needs no `--var` at all. A module with required inputs will need them.

### Checking coverage first

`--dry-run` writes an `imports.tf` artifact, plans with it in place, and stops without touching Terraform state:

```bash
atelier import juju \
    --source https://github.com/canonical/observability-stack.git \
    --module terraform/cos-lite \
    --query-var model_uuid=2af837d8-f470-488e-84cb-c588a39732d8 \
    --ref track/3.0 \
    --dry-run
```

```
Dry run — nothing was imported. Terraform state is untouched.
Wrote import artifact: ./imports.tf

Preview: 43 to import, 3 to add, 0 to change, 0 to destroy.
  3 of those 3 additions are Terraform-internal types with no live
  counterpart (e.g. terraform_data), and are expected.

  Every importable resource the module declares is covered.
```

The number to watch is **to add**. Those are resources the module would create rather than import — so if they already exist, your variables do not describe the live deployment. Terraform-internal types that can never be imported are sub-counted separately and are expected. Anything else is listed by address.

`imports.tf` is a reviewable artifact you can edit and keep. Note that Atelier does **not** apply it: importing is done with `terraform import`, which only writes state and so cannot alter infrastructure. Applying `imports.tf` yourself is riskier, because `import {}` blocks land in the same plan as everything else — a `terraform apply` would also create every resource the file does not cover.

Atelier queries the live deployment and reports what it matched. Some module resources have zero or ambiguous live matches and must be imported manually. Live resources not declared by the module are left alone. From this point forward, you manage the deployment with normal Atelier operations. Running `atelier` in the wrapper directory opens the TUI with the imported state loaded:

```
Module: cos_lite@track/3.0  ✓ valid  ⚠ 6 check warning(s)
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
  --preset loki-vars
```

`loki-operators` declares `channel`, `model_uuid` and the S3 settings without defaults, so they must all have values — but `model_uuid` is covered by the `--query-var` you already passed, so only `channel` and the S3 settings need supplying.

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

## Safety properties

Worth knowing before you run this against something you care about:

- **Importing cannot change your infrastructure.** Atelier uses `terraform import`, which only writes state. A wrong or partial import produces a bad state file, not a damaged deployment.
- **Re-running is safe and expected.** Resources already in state are skipped, so the intended loop is: run, read the report, fix your variables, run again. A second run over a finished import reports `Nothing to import: all N matched resource(s) are already in state.` and changes nothing.
- **A model mismatch is refused.** If the configuration targets a different model than the live resources came from, Atelier aborts before writing anything. `model_uuid` forces replacement on every Juju resource, so proceeding would make the next apply destroy everything just imported and recreate it in the other model.
- **If an import fails part-way**, the resources that did not get imported are written to `imports.tf` so you can inspect, fix and retry rather than reconstructing the list by hand.
- **Check the plan before applying.** `juju_application` resources must not show `replace` or `create`. A clean import shows only attribute drift and any Terraform-internal resources that have no live counterpart.

## Next steps

With state imported, you can use Atelier like any other wrapper: edit variables in the TUI, plan to check the diff, apply to converge, or bump the module ref to upgrade the deployment. The wrapper is a normal Atelier wrapper; the import just gave it a head start.
