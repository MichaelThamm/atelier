# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""The golden path: a producer and a consumer that share a typed interface are
wired by `atelier integrate`.

The producer publishes an interface catalogue (`cos_offers`, keyed by the Juju
interface each offer satisfies); the consumer takes a `map(string)` and gates
each integration on the keys it was given. That is the shape ADR-0062 specifies,
and it is what makes the wire plan correctly: a wired offer URL is unknown until
the producer applies, so the consumer gates on `keys(...)` (known at plan), never
on the value.

Hermetic: both module sources are local directories, so there is no network and
no Juju.
"""

from pathlib import Path

from helpers import atelier

# A producer "COS Lite": every interface it exposes, as a typed object.
PRODUCER = '''
output "cos_offers" {
  type = object({
    grafana_dashboard       = string
    loki_push_api           = string
    prometheus_remote_write = string
  })
  value = {
    grafana_dashboard       = "admin/cos.grafana-dashboards"
    loki_push_api           = "admin/cos.loki-logging"
    prometheus_remote_write = "admin/cos.prometheus-receive-remote-write"
  }
}
'''

# A consumer "Charmed Spark": a map of interface -> URL, so it can accept a
# subset and gate each integration on the provided keys.
CONSUMER = '''
variable "cos_offers" {
  type    = map(string)
  default = {}
}
'''


def stack(tmp_path: Path) -> Path:
    """A wrapper composing the producer and the consumer by local source."""
    wrapper = tmp_path / "stack"
    (wrapper / "producer").mkdir(parents=True)
    (wrapper / "consumer").mkdir(parents=True)
    (wrapper / "producer" / "outputs.tf").write_text(PRODUCER)
    (wrapper / "consumer" / "variables.tf").write_text(CONSUMER)
    (wrapper / "main.tf").write_text(
        'module "cos_lite" {\n  source = "./producer"\n}\n'
        'module "charmed_spark" {\n  source = "./consumer"\n}\n'
    )
    return wrapper


def test_integrate_wires_the_whole_object_by_default(tmp_path):
    wrapper = stack(tmp_path)
    atelier("integrate --yes", cwd=wrapper)
    main = (wrapper / "main.tf").read_text()
    assert "cos_offers = module.cos_lite.cos_offers" in main, main


def test_integrate_wires_a_chosen_subset(tmp_path):
    wrapper = stack(tmp_path)
    atelier("integrate --yes --interfaces=loki_push_api", cwd=wrapper)
    main = (wrapper / "main.tf").read_text()
    assert (
        "cos_offers = { loki_push_api = module.cos_lite.cos_offers.loki_push_api }"
        in main
    ), main
    assert "grafana_dashboard =" not in main, main


def test_integrate_dry_run_writes_nothing(tmp_path):
    wrapper = stack(tmp_path)
    before = (wrapper / "main.tf").read_text()
    atelier("integrate --dry-run", cwd=wrapper)
    assert (wrapper / "main.tf").read_text() == before


def test_integrate_reports_an_existing_wire_without_rewriting_it(tmp_path):
    wrapper = stack(tmp_path)
    atelier("integrate --yes --interfaces=loki_push_api", cwd=wrapper)
    before = (wrapper / "main.tf").read_text()
    done = atelier("integrate --dry-run", cwd=wrapper)
    assert "(existing)" in done.stdout, done.stdout
    assert (wrapper / "main.tf").read_text() == before


def test_integrate_overwrites_an_existing_wire(tmp_path):
    wrapper = stack(tmp_path)
    atelier("integrate --yes --interfaces=loki_push_api", cwd=wrapper)
    atelier("integrate --yes --interfaces=grafana_dashboard", cwd=wrapper)
    main = (wrapper / "main.tf").read_text()
    assert "grafana_dashboard =" in main, main
    assert "loki_push_api =" not in main, main