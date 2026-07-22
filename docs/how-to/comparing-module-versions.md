# Comparing module versions before you upgrade

**Inspect changes when you modify a Terraform module, without leaving your terminal.**

---

Upgrading a Terraform module is one of those tasks that sounds simple until you're staring at a two-week-old `terraform plan` output in one terminal, a freshly-cloned copy of the new version in another, and trying to mentally diff the two. Which resources actually changed? Did the new version drop a variable you were relying on? Did it add required inputs you haven't set yet?

Atelier gives you a structured answer to all of those questions from inside a single TUI session.

## Plan at the current version

Before you switch anything, plan at the version you're running now. Press `P` and Atelier runs `terraform plan` against your wrapper, building a collapsible tree of resource changes organised by module and resource type. The right pane shows per-attribute diffs: before and after values for every changed attribute, with unchanged attributes filtered out.

This is your baseline. You know what the current version of the module would do.

```
╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ Module: cos_lite ·unpinned  ⚠ 3 check warning(s)                                                                │
╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
  Plan: 47 to add, 0 to change, 0 to destroy.                                                                      
  ⚠ 3 check warning(s): grafana.storage_directives is unset, so it will use the default 1G volume. Set a size …    
╭────────────────────────────────────────────╮╭───────────────────────────────────────────────────────────────────╮
│ ▾ module.cos_lite                          ││ Select a resource row to see its attribute diff.                  │ 
│   ▸ juju_integration                       ││                                                                   │ 
│   ▸ juju_model                             ││ Use ↑/↓ to navigate, Enter to collapse/expand, Tab to focus this  │ 
│   ▸ juju_offer                             ││ pane.                                                             │ 
│   ▸ terraform_data                         ││                                                                   │ 
│ ▾ module.cos_lite.module.alertmanager      ││                                                                   │ 
│   ▸ juju_application                       ││                                                                   │ 
│ ▾ module.cos_lite.module.catalogue         ││                                                                   │ 
│   ▸ juju_application                       ││                                                                   │ 
│ ▾ module.cos_lite.module.grafana           ││                                                                   │ 
│   ▸ juju_application                       ││                                                                   │ 
│ (1/21 0%)                                  ││                                                                   │ 
╰────────────────────────────────────────────╯╰───────────────────────────────────────────────────────────────────╯
╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│     [↑︎↓︎] navigate  [Enter] toggle  [Tab] focus diff  [P] re-plan  [A] apply  [W] warnings  [Esc] back  [?] help │
╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
```

## Switch the ref

Press `R` and Atelier opens the ref switch modal. The module you're targeting is context-aware: in a multi-module wrapper, `R` targets whichever module owns the variable your cursor is on, not always the primary one. The modal shows the current ref and a searchable list of available branches and tags from the remote.

Confirm with `Enter` and Atelier:

1. **Re-clones the module** from the remote at the new ref.
2. **Carries your variable values forward**, including wired HCL expressions like `model_uuid = data.juju_model.service_model.uuid`. If a variable exists in both versions, your value comes with it. If a variable was present in the old ref but is missing in the new one, it's flagged as orphaned and dropped. If the new ref introduced required variables you haven't set, Atelier sets those as required with `[!]`.
3. **Runs `terraform init -upgrade`** on the next plan so Terraform re-fetches the module source at the new ref.
4. **Saves the wrapper** with the updated source URL.

You don't need to re-clone, re-init, or reconfigure anything manually.

The message provides context on what variable changes occurred:
```
Switched cos_lite to ref: track/2 (69a6621) · 4 orphaned: risk, model, postgresql_offer_url, ingress · 2 new: channel, model_uuid · 1 required model_uuid
```

## Plan again and read the diff

Press `P` again. The plan that runs now targets the new version of the module with your existing values applied. The diff you see in the tree is the infrastructure delta between your current state and what the new module version would produce.

This is the upgrade comparison you've been wanting: a single view that answers which resources changed, which ones are being replaced, and which ones are being destroyed, without requiring you to hold two separate workspaces in your head.

## Toggle between diff and state

The plan view doesn't stop at diffs. Press `S` to toggle between the **plan diff** view and the **current state** view. The state view shows the live attribute values of every managed resource, read directly from `terraform.tfstate` without invoking Terraform. This is useful for understanding context: before you read a diff, you might want to see what the resource actually looks like right now.

After a successful apply, the plan is consumed and the state view is shown automatically, since there's nothing left to diff against. On the next plan, the diff view returns.

The plan header always tells you where you stand:
```
Plan: 3 to add, 1 to change, 0 to destroy. | State: 54 resource(s) across 8 modules
```

## The full workflow

1. Open your wrapper in Atelier.
2. Press `P` to plan at the current version. Review the baseline.
3. Press `R`, type the new ref, confirm.
4. Fill in any new required variables the upgrade introduced.
5. Press `P` again. Read the infrastructure diff.
6. Press `S` to check current state if you need context.
7. Press `A` to apply when you're satisfied, or `Q` to quit and come back later.

Seven steps, zero context-switching between terminals.

## Why this matters

Module upgrades are the most common source of Terraform drift surprises. A module you depend on might change defaults, rename variables, or introduce new resources. Without a structured way to compare, you're left reading changelogs (if they exist) and hoping nothing catches you off guard.

Atelier makes the comparison mechanical: plan, switch, plan, read. The diff is always against your actual state, not a hypothetical. And because the wrapper carries your values forward through the switch, you're comparing the module bump itself, not a side effect of missing configuration.