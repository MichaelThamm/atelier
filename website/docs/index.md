# Atelier

A terminal UI for deploying Terraform modules.

Point Atelier at any git repository containing a Terraform module. It asks for
the values the module needs, then writes a small `main.tf` you can run like any
other Terraform configuration:

```hcl
module "cos_lite" {
  source = "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=main"
  model  = { name = "cos-lite" }
}
```

Only the values you chose appear — everything else uses the module's defaults.
You get a browsable variable list, plan and apply without leaving the terminal,
and presets for reusable configurations.

[Browse the module gallery](https://michaelthamm.github.io/atelier/gallery/){ .md-button .md-button--primary }
[:octicons-mark-github-16: Juju modules](https://michaelthamm.github.io/atelier/juju/){ .md-button }
[:octicons-mark-github-16: GitHub](https://github.com/MichaelThamm/atelier){ .md-button }

## Install

Download the archive for your OS and CPU, extract it, and put `atelier` on your
`PATH`. The [README](https://github.com/MichaelThamm/atelier#install) has the
full command. Or, with Go:

```bash
go install github.com/MichaelThamm/atelier/cmd/atelier@latest
```

## Quick start

Start from a bundled gallery entry by name. Atelier expands it to the module,
its pinned revision, and any preset the entry names:

```bash
atelier gallery list          # every entry, with the command that deploys it
atelier apply cos-lite
```

`atelier apply` deploys: it writes the wrapper, runs `terraform init`, and hands
the plan to Terraform's own prompt. To configure the values first — and review
them in Atelier's TUI — use `atelier add`:

```bash
atelier add cos-lite
```

Every entry on the [gallery page](https://michaelthamm.github.io/atelier/gallery/)
has its own one-liner. The [Juju page](https://michaelthamm.github.io/atelier/juju/)
has the same entries, plus a variant under each that deploys into whichever model
you have switched to.

Either way, what Atelier writes is an ordinary Terraform project, so it runs
without Atelier installed:

```bash
terraform init && terraform apply
```
