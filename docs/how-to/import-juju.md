# Importing a live Juju deployment

`atelier import` pulls a running Juju deployment under Terraform management.
The workflow is: deploy normally, then import the live applications so Atelier
can manage them going forward.

## Prerequisites

- `terraform` (or `tofu`) >= 1.14 on your `PATH`
- `atelier` on your `PATH`
- `juju` with an active controller and a deployed model
- `git` on your `PATH`

## Step 1: Deploy COS Lite

```bash
atelier module add https://github.com/canonical/observability-stack.git \
  --module terraform/cos-lite --ref track/3.0
atelier plan
atelier apply
```

This deploys COS Lite through the standard Atelier flow. Note the model UUID
from `juju status`.

## Step 2: Remove state

Delete the Terraform state so we can re-import from the live deployment:

```bash
rm -f terraform.tfstate terraform.tfstate.backup
```

## Step 3: Import into a fresh wrapper

Create a new directory (not the one you applied from) and import:

```bash
mkdir import-target && cd import-target
atelier import juju \
  --source https://github.com/canonical/observability-stack.git \
  --module terraform/cos-lite \
  --ref track/3.0 \
  --var model_uuid=<your-model-uuid>
```

This clones the module, writes an Atelier wrapper, discovers live applications
via `terraform query`, matches them to the module's resource addresses, and
runs `terraform import` for each match.

## Step 4: Check the plan

Run `atelier` to open the TUI, or use the CLI directly:

```bash
terraform plan
```

The plan should show:

- **Applications** — no changes (imported successfully).
- **Integrations, offers, and other resources** — to be created. These are
  resources the module declares that weren't imported.

## Step 5: Reconcile

You have two options:

1. **Remove stale resources via Juju** — delete offers, integrations, and
   other resources that the module will recreate:
   ```bash
   juju remove-relation ...
   juju remove-application ...
   ```

2. **Let Terraform recreate everything** — `terraform apply` will create all
   integrations, offers, and other resources the module declares. Existing
   applications are preserved.

Either way, from this point forward you manage the deployment with normal
Atelier/Terraform operations (`atelier plan`, `atelier apply`).
