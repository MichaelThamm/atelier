# Trino's product module takes its ingress as a variable without a default;
# every attribute is optional, so an empty object takes the module's own
# defaults.
#
# The model name, the model logging config, and the Charmhub risk stay on the
# entry: Atelier does not invent them. `logging_config=all` and `risk=edge` are
# the values that satisfy the module's own validation rules; the model name is
# the user's.

proxy = {}