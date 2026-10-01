# Example personal preset bundle — a plain Terraform .tfvars file.
#
# Atelier discovers bundles in an `atelier.presets/` directory, walking UP from
# the wrapper directory to the filesystem root (or $HOME). Placing one at a
# shared parent means every wrapper beneath it inherits these presets; a bundle
# in the wrapper directory itself takes precedence over a same-named one higher
# up.
#
# Apply one by name from the CLI:
#   atelier module add <git-url> --var-file cos-lite-dev --yes
# or pick it in the TUI with `F`. Save the current configuration as a new bundle
# with `S`, which writes atelier.presets/<name>.tfvars in the wrapper directory.

model = {
  name = "cos-lite-dev"
}

internal_tls = false

ingress = {
  alertmanager            = false
  catalogue               = false
  grafana                 = false
  loki                    = false
  opentelemetry_collector = false
  prometheus              = false
}
