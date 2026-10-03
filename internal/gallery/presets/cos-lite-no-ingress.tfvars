# Disable ingress (and Traefik) for every COS Lite component.
#
# Optional: the entry applies this only when asked, with
# `--var-file cos-lite-no-ingress`. Use it when the model has no ingress
# controller, or when nothing should be reachable from outside the cluster.

ingress = {
  alertmanager            = false
  catalogue               = false
  grafana                 = false
  loki                    = false
  prometheus              = false
  opentelemetry_collector = false
}