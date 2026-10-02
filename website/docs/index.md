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
atelier apply cos-lite
```

Every entry on the [gallery page](https://michaelthamm.github.io/atelier/gallery/)
has its own one-liner. What Atelier writes is an ordinary Terraform project, so
it runs without Atelier installed:

```bash
terraform init && terraform apply
```
