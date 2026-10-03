# Disable ingress (and Traefik) for every COS component.
#
# Optional: the entry applies this only when asked, with
# `--var-file cos-no-ingress`. Use it when the model has no ingress controller,
# or when nothing should be reachable from outside the cluster.

ingress = {
  alertmanager            = false
  catalogue               = false
  grafana                 = false
  loki                    = false
  mimir                   = false
  opentelemetry_collector = false
  tempo                   = false
}
