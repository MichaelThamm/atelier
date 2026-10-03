# One Kyuubi and one ZooKeeper unit.
#
# Optional: the entry applies this only when asked, with
# `--var-file spark-single-unit`. Both default to three units for high
# availability, which is a development controller's worth of compute.

kyuubi_units    = 1
zookeeper_units = 1