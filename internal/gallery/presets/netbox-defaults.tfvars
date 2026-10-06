# NetBox, Redis, S3, and the optional SSO integrator at the module's own defaults.

netbox_k8s                    = {}
redis_k8s                     = {}
s3                            = {}
oauth_external_idp_integrator = {}

# Each of these has no default and every one of its attributes is optional, so an
# empty object takes the module's own app name, channel, units, and config.
