# NetBox's product module takes its app definitions as variables without
# defaults. Every attribute of each is optional, so an empty object takes the
# module's own defaults: app name, charm channel, units, and config.

netbox_k8s                    = {}
redis_k8s                     = {}
s3                            = {}
oauth_external_idp_integrator = {}