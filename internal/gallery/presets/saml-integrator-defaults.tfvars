# The SAML integrator's product module takes its app definition as a variable
# without a default; every attribute is optional, so an empty object takes the
# module's own defaults. No offer consumers yet: the user names them after
# applying, once the consuming app exists to integrate with.

saml_integrator      = {}
saml_offer_consumers = []