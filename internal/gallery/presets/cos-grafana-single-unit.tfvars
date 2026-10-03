# One Grafana unit, which is the most the stack can run without a database.
#
# Grafana holds its dashboards in Postgres above one unit, so the module
# requires `postgresql_offer_url` at that scale. This is the smallest change
# that makes the entry plan; every other component keeps the module's own
# default. Use `cos-single-unit` to scale the whole stack down.

grafana = { units = 1 }