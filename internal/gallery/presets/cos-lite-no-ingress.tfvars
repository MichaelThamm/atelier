# Disable ingress (and Traefik) for every COS Lite component.
# Copied from the upstream module's terraform/cos-lite/presets/no-ingress.tfvars.

ingress = {
  alertmanager            = false
  catalogue               = false
  grafana                 = false
  loki                    = false
  prometheus              = false
  opentelemetry_collector = false
}
