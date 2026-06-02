# Tutorial: Using Terragrunt with Atelier

This tutorial shows how to use **Atelier** to interactively discover and
configure Terraform modules, then **Terragrunt** to replicate those
configurations across multiple environments (dev, staging, prod) without
duplicating code.

---

## Prerequisites

```bash
# Install Terragrunt
snap install terragrunt --classic
# or: brew install terragrunt

# Verify
terragrunt --version
terraform --version
atelier --help
```

---

## The workflow

```
┌─────────────────────────────────────────────────────────────────┐
│  1. Atelier: discover & configure a module interactively        │
│     → produces a wrapper (main.tf) with your chosen values      │
│                                                                 │
│  2. Extract the configuration into a Terragrunt structure       │
│     → one terragrunt.hcl per environment                        │
│                                                                 │
│  3. Terragrunt: deploy to dev/staging/prod with DRY config      │
└─────────────────────────────────────────────────────────────────┘
```

---

## Step 1: Use Atelier to explore the module

Start by using Atelier to discover the module's API and figure out what
values you want:

```bash
mkdir ~/deployments && cd ~/deployments
atelier module add https://github.com/canonical/observability-stack.git --module terraform/cos-lite
```

This clones the repo, parses variables, and opens the TUI. You can now:
- See every variable the module exposes (risk, base, internal_tls, ingress, etc.)
- Use type-aware editors to configure values
- Run `P` to plan and validate your choices
- Press `Q` to save and quit

After quitting, you have a wrapper with a `main.tf` like:

```hcl
module "cos_lite" {
  source = "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=main"

  risk         = "stable"
  internal_tls = true
  ingress = {
    alertmanager = true
    grafana      = true
  }
  prometheus = {
    units = 3
  }
}
```

This is your **reference configuration** — the values you want. Now you'll
use Terragrunt to deploy variations of this across environments.

---

## Step 2: Create the Terragrunt directory structure

Terragrunt organises environments as a directory tree. Each environment gets
its own `terragrunt.hcl` with environment-specific overrides.

```bash
mkdir -p ~/deployments/live/{dev,staging,prod}/cos-lite
```

Your tree will look like:

```
live/
├── root.hcl                # root config (shared settings)
├── dev/
│   └── cos-lite/
│       └── terragrunt.hcl  # dev-specific values
├── staging/
│   └── cos-lite/
│       └── terragrunt.hcl  # staging-specific values
└── prod/
    └── cos-lite/
        └── terragrunt.hcl  # prod-specific values
```

---

## Step 3: Write the root `root.hcl`

The root config defines settings shared across all environments.

```bash
cat > ~/deployments/live/root.hcl << 'EOF'
# root.hcl — shared configuration for all environments.
# Children inherit this via include blocks.

# Store state locally (or configure a remote backend here).
# For Juju deployments with local state, this is fine:
generate "backend" {
  path      = "backend.tf"
  if_exists = "overwrite"
  contents  = <<BACKEND
terraform {
  backend "local" {}
}
BACKEND
}
EOF
```

> **Note:** For production you'd use a remote backend (S3, GCS, etc.), but
> for local Juju testing, local state is fine.

---

## Step 4: Write environment-specific configs

### `dev/cos-lite/terragrunt.hcl`

```bash
cat > ~/deployments/live/dev/cos-lite/terragrunt.hcl << 'EOF'
# Include the root config for shared settings.
include "root" {
  path = find_in_parent_folders("root.hcl")
}

# Point at the module source. This is the same source Atelier discovered.
terraform {
  source = "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=main"
}

# Dev environment values — fast iteration, single units, edge channel.
inputs = {
  risk         = "edge"
  internal_tls = false

  model = {
    name = "cos-dev"
  }

  prometheus = {
    units = 1
  }

  grafana = {
    units = 1
  }
}
EOF
```

### `staging/cos-lite/terragrunt.hcl`

```bash
cat > ~/deployments/live/staging/cos-lite/terragrunt.hcl << 'EOF'
include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=main"
}

# Staging — closer to prod, still edge channel for early validation.
inputs = {
  risk         = "edge"
  internal_tls = true

  model = {
    name = "cos-staging"
  }

  ingress = {
    alertmanager = true
    grafana      = true
    prometheus   = true
  }

  prometheus = {
    units = 2
  }
}
EOF
```

### `prod/cos-lite/terragrunt.hcl`

```bash
cat > ~/deployments/live/prod/cos-lite/terragrunt.hcl << 'EOF'
include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=v1.0.0"
}

# Production — stable channel, HA, TLS, pinned ref.
inputs = {
  risk         = "stable"
  internal_tls = true

  model = {
    name = "cos-prod"
  }

  ingress = {
    alertmanager = true
    catalogue    = true
    grafana      = true
    loki         = true
    prometheus   = true
  }

  prometheus = {
    units = 3
  }

  alertmanager = {
    units = 3
  }

  grafana = {
    units = 2
  }
}
EOF
```

---

## Step 5: Deploy with Terragrunt

### Deploy a single environment

```bash
cd ~/deployments/live/dev/cos-lite

# Initialise (downloads the module, sets up backend)
terragrunt init

# See what will be created
terragrunt plan

# Deploy
terragrunt apply
```

### Deploy all environments at once

```bash
cd ~/deployments/live

# Plan across all environments in parallel
terragrunt run-all plan

# Apply all (Terragrunt will ask for confirmation per environment)
terragrunt run-all apply
```

### Target a specific environment

```bash
# Only staging
cd ~/deployments/live/staging/cos-lite
terragrunt apply
```

---

## Step 6: DRY it up further (optional)

If all environments share the same module source and you only vary inputs,
you can extract the `terraform` block into a shared file:

```bash
cat > ~/deployments/live/_envcommon/cos-lite.hcl << 'EOF'
# Shared module config for cos-lite across all environments.
terraform {
  source = "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=${local.ref}"
}
EOF
```

Then each environment includes it:

```hcl
# dev/cos-lite/terragrunt.hcl
include "root" {
  path = find_in_parent_folders("root.hcl")
}

include "cos_lite" {
  path   = "${dirname(find_in_parent_folders("root.hcl"))}/_envcommon/cos-lite.hcl"
  expose = true
}

locals {
  ref = "main"  # dev follows HEAD
}

inputs = {
  risk = "edge"
  model = { name = "cos-dev" }
}
```

```hcl
# prod/cos-lite/terragrunt.hcl
include "root" {
  path = find_in_parent_folders("root.hcl")
}

include "cos_lite" {
  path   = "${dirname(find_in_parent_folders("root.hcl"))}/_envcommon/cos-lite.hcl"
  expose = true
}

locals {
  ref = "v1.0.0"  # prod is pinned
}

inputs = {
  risk         = "stable"
  internal_tls = true
  model        = { name = "cos-prod" }
  prometheus   = { units = 3 }
}
```

---

## Step 7: The Atelier → Terragrunt handoff

Here's the concrete workflow:

1. **Discover** — `atelier module add <url>` to explore the module's full API.
2. **Configure** — Use the TUI to find the right values. Plan to validate.
3. **Extract** — Copy the relevant `inputs` from Atelier's `main.tf` into
   your `terragrunt.hcl` files, adjusting per environment.
4. **Iterate** — When the module gets a new version, use `atelier` + `R` to
   see what changed (new vars, changed defaults), then update your
   Terragrunt inputs accordingly.

Atelier stays the **discovery and authoring tool**. Terragrunt stays the
**replication and orchestration tool**. They don't overlap.

---

## Key Terragrunt commands reference

| Command | What it does |
|---------|--------------|
| `terragrunt init` | Download module + initialise backend |
| `terragrunt plan` | Show what will change |
| `terragrunt apply` | Deploy the infrastructure |
| `terragrunt destroy` | Tear down the infrastructure |
| `terragrunt run-all plan` | Plan across all subdirectories |
| `terragrunt run-all apply` | Apply across all subdirectories |
| `terragrunt output` | Show outputs from deployed state |
| `terragrunt validate` | Validate the configuration |

---

## Multi-module example (cos-lite + seaweedfs)

If you used `atelier module add` twice to compose a deployment:

```
live/
├── dev/
│   ├── cos-lite/
│   │   └── terragrunt.hcl
│   └── seaweedfs/
│       └── terragrunt.hcl      # depends on cos-lite
└── prod/
    ├── cos-lite/
    │   └── terragrunt.hcl
    └── seaweedfs/
        └── terragrunt.hcl
```

Use Terragrunt's `dependency` block to wire them:

```hcl
# dev/seaweedfs/terragrunt.hcl
include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "git::https://github.com/canonical/observability-stack.git//terraform/seaweedfs?ref=main"
}

# Declare dependency on cos-lite (Terragrunt ensures order)
dependency "cos_lite" {
  config_path = "../cos-lite"
}

inputs = {
  model = {
    name = dependency.cos_lite.outputs.model_name
  }
}
```

This is where Terragrunt handles cross-module orchestration that Atelier
intentionally avoids (ADR-0016). Atelier helped you discover that seaweedfs
has a `model` input; Terragrunt wires the actual value from cos-lite's
output at deploy time.

---

## Summary

| Concern | Tool | Why |
|---------|------|-----|
| "What variables does this module have?" | Atelier | Interactive discovery with type-aware editors |
| "What values work?" | Atelier | Plan + validate inline |
| "Deploy the same thing to 3 environments" | Terragrunt | DRY fan-out with `run-all` |
| "Module B depends on Module A's output" | Terragrunt | `dependency` blocks + ordered execution |
| "New module version — what changed?" | Atelier | `R` to switch ref, see new/orphaned vars |
| "Roll out the version bump everywhere" | Terragrunt | Update ref in `_envcommon/`, `run-all apply` |
