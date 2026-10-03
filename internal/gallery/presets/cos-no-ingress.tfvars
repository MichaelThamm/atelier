# Disable ingress (and Traefik) for every COS component. Use it when the model
# has no ingress controller, or when nothing should be reachable from outside
# the cluster.

ingress = {
  alertmanager            = false
  catalogue               = false
  grafana                 = false
  loki                    = false
  mimir                   = false
  opentelemetry_collector = false
  tempo                   = false
}
