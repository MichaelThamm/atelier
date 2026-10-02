# Disable ingress (and Traefik) for every COS component.

# Atelier-hosted copy of terraform/cos/presets/no-ingress.tfvars in
# canonical/observability-stack: generic enough that Atelier ships it rather
# than relying on the product module to maintain it (ADR-0038).

ingress = {
  alertmanager            = false
  catalogue               = false
  grafana                 = false
  loki                    = false
  mimir                   = false
  opentelemetry_collector = false
  tempo                   = false
}
