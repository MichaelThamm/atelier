# The smallest change that lets the COS entry plan: one Grafana unit.

grafana = { units = 1 }

# More units need `postgresql_offer_url` as well: Grafana keeps its dashboards in
# Postgres above one unit.
