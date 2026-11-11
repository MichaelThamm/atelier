# Integration interface specification

How Terraform product modules in the Juju ecosystem advertise the relations
they expose and consume, so that one product can be wired to another without an
operator pasting live offer URLs by hand.

This document is the concrete, product-facing convention. It is normative for
any module that wants to interoperate with COS: producers publish an
**interface catalog**, consumers declare the interfaces they require, and a
wire is a plain `module.<producer>.<output>` reference. The interface-naming
rule is recorded in [ADR-0067](adr/0067-interface-keys-are-charm-interface-names.md).

## 1. Why

Integrating two products today means reading live Juju offer URLs out of one
model and pasting them into the other as variables, for example:

```bash
atelier apply cos-lite --dir stack
atelier apply charmed-spark --dir stack \
  --var 'cos_offers={dashboard="admin/cos.grafana-dashboards",…}'
```

Each consumer invents its own object shape (`cos_offers` as
`{dashboard, logging, metrics}` in one product, an optional
`{dashboard, metrics, logging, tracing}` in another), and the relation names
that actually define the integration live only in each charm's
`charmcraft.yaml`. Nothing about the relation is machine-readable or
reviewable from the Terraform side.

The convention makes the relation itself the contract:

- A **producer** publishes a typed output whose keys are the Juju interface
  names and whose values are the offer URLs.
- A **consumer** declares the interfaces it requires, in its own input type.
- A wire is `module.<producer>.<output>` — plain Terraform, no new format, and
  the wrapper still runs without Atelier.

## 2. Status and scope

- **Status:** implemented on the product branches for COS and Cos Lite
  (canonical/observability-stack#557) and for the Charmed Spark consumer
  (canonical/spark-k8s-bundle#384). New producers and consumers adopt it by
  following §8.
- **In scope:** the shape of the producer catalog output and the consumer
  input, the key vocabulary, and the wire between them.
- **Out of scope:** how a producer builds its offers, which charms it backs them
  with, and any tool that consumes the catalog. The convention is Terraform and
  Juju only.

## 3. Terminology

| Term | Meaning |
| --- | --- |
| **Interface** | A Juju relation contract, named by the `interface:` field the charms declare in `charmcraft.yaml` (for example `prometheus_remote_write`, `loki_push_api`, `otlp`). |
| **Producer** | A product module that publishes an interface catalog — every relation it can integrate over, as offer URLs. COS and Cos Lite are producers. |
| **Consumer** | A product module that requires some of those interfaces and declares them in an input. Charmed Spark is a consumer. |
| **Catalog** | The producer's typed output: a name plus a set of interface keys, each value an offer URL. |
| **Key** | One attribute of the catalog: the interface name. |
| **Wire** | `input = module.<producer>.<output>` written into the consumer's `module` block. |

## 4. The convention

### 4.1 Producer: publish an interface catalog

A producer publishes one output per interface family, keyed by the interface
name and valued by the offer URL. COS and Cos Lite each publish `cos_offers`:

```hcl
# outputs.tf (cos-lite)
output "cos_offers" {
  description = "Every offer this product exposes, keyed by the Juju interface it satisfies, as offer URLs."
  type = object({
    grafana_dashboard       = string
    karma_dashboard         = string
    loki_push_api           = string
    otlp                    = string
    prometheus_remote_write = string
    prometheus_scrape       = string
  })
  value = {
    grafana_dashboard       = local.offers.grafana_dashboards.url
    karma_dashboard         = local.offers.alertmanager_karma_dashboard.url
    loki_push_api           = local.offers.loki_logging.url
    otlp                    = local.offers.otelcol_receive_otlp.url
    prometheus_remote_write = local.offers.prometheus_receive_remote_write.url
    prometheus_scrape       = local.offers.prometheus_metrics_endpoint.url
  }
}
```

Rules:

- **One key per relation.** The key is the interface name, never the endpoint
  or offer name (`grafana_dashboard`, not `grafana-dashboards` or
  `grafana_dashboards`). See §5.
- **Value is the offer URL string.** Not the `juju_offer` resource object. The
  catalog is a stable, typed surface; the producer maps offers to URLs
  internally.
- **Publish every interface the product participates in**, whether the product
  provides it or requires it. The catalog is the product's participation, not
  only what it serves. Cos Lite lists `prometheus_scrape` even though COS's
  Prometheus is the side that scrapes.
- **Do not tailor the catalog to a consumer.** A producer publishes its full
  set; a consumer narrows it. A key a product does not implement is simply
  absent (Cos Lite has no `tracing`, COS has no `prometheus_scrape`), and a
  consumer that requires it fails to type-check — correctly, and at plan time.
- **The output name is part of the contract.** Two producers that satisfy the
  same interfaces publish the *same* name and the *same* keys, so a consumer
  works against either. COS and Cos Lite are interchangeable for the interfaces
  they share precisely because both are called `cos_offers`.

### 4.2 Consumer: declare the interfaces it requires

A consumer declares an input with the producer's catalog name, typed as exactly
the interfaces it requires. The type *is* the declaration:

```hcl
# terraform/products/charmed-spark/variables.tf
variable "cos_offers" {
  description = "COS integration interface: the offer URLs Spark requires, keyed by the Juju interface each satisfies."
  type = object({
    grafana_dashboard       = string
    loki_push_api           = string
    prometheus_remote_write = string
  })
  default = null
}
```

The consumer integrates each declared interface and ignores the rest:

```hcl
# terraform/components/observability/integrations.tf
data "juju_offer" "grafana_dashboards" {
  url = var.offers.grafana_dashboard
}

data "juju_offer" "prometheus_receive_remote_write" {
  url = var.offers.prometheus_remote_write
}

data "juju_offer" "loki_logging" {
  url = var.offers.loki_push_api
}
```

### 4.3 The join: name and type

A producer output and a consumer input join when:

1. **Their names match.** `cos_offers` to `cos_offers`.
2. **The consumer's declared type is structurally compatible** with the value:
   every attribute the consumer names exists in the producer with a compatible
   type.

Terraform drops the producer's extra attributes on assignment, so a producer
may offer more interfaces than a consumer names and the same consumer works
against COS or Cos Lite.

### 4.4 Whole object or subset

- An **`object({ … })`-typed** input requires exactly the named interfaces and
  is wired **whole** — none or all. This is the recommended default: the
  requirement is readable from the input type, and Terraform enforces it.
- A **`map(string)`-typed** input can take an arbitrary subset. A consumer that
  needs this gates on the keys it was given:

  ```hcl
  # subset-capable consumer
  variable "cos_offers" { type = map(string) }

  module "observability" {
    count  = contains(keys(var.cos_offers), "loki_push_api") ? 1 : 0
    offers = var.cos_offers
  }
  ```

  This is the only case where a per-interface choice is possible, and it pays
  for itself in ceremony. Prefer the object type unless a product genuinely
  needs to accept a varying set.

### 4.5 Gate on structure, never on values

A wired value's **values** are unknown until the producer applies, but its
**structure** — whether it is null, and an object's or a map's keys — is known
at plan. `count` and `for_each` may therefore depend on structure and must
never depend on a value:

```hcl
# OK at plan: the object is known to be non-null even when its attributes are not
count = var.cos_offers == null ? 0 : 1

# OK at plan: a map's keys are known even when its values are not
count = contains(keys(var.cos_offers), "loki_push_api") ? 1 : 0

# NOT ok: fails when cos_offers is wired to another module's output
count = var.cos_offers.prometheus_remote_write != null ? 1 : 0
```

The third form fails because an attribute of a module output is unknown at
plan. The first two are safe because they read the type's shape.

### 4.6 What a wire looks like

```hcl
module "cos_lite" {
  source = "git::https://github.com/canonical/observability-stack//terraform/cos-lite?ref=<ref>"
}

module "charmed_spark" {
  source     = "git::https://github.com/canonical/spark-k8s-bundle//terraform/products/charmed-spark?ref=<ref>"
  cos_offers = module.cos_lite.cos_offers
}
```

Only the interfaces Spark requires are carried; the producer's other keys are
dropped by Terraform. `terraform apply` is the deployment mechanism — the wire
is data, not orchestration.

## 5. Key naming

**The key is the Juju interface name, exactly as the backing charms declare it
in `charmcraft.yaml`. It is never a product-defined category.**

### 5.1 The rule

Use the value of the `interface:` field on the relation the offer satisfies:

| Use | Do not use |
| --- | --- |
| `prometheus_remote_write` | `metrics`, `remote_write` |
| `prometheus_scrape` | `metrics` |
| `loki_push_api` | `logs` |
| `otlp` | `logs`, `metrics`, `traces` |
| `tracing` | `traces` |
| `grafana_dashboard` | `dashboard`, `dashboards` |

The endpoint and offer names are *not* the key: `grafana_dashboards` (offer) and
`grafana-dashboards` (endpoint) both name the `grafana_dashboard` relation.

### 5.2 Why not categories

A category names a payload, but the integration is a relation, and relations
that carry the same payload are not interchangeable:

- `metrics` conflates `prometheus_scrape` — where COS's Prometheus **pulls**
  from the consumer — with `prometheus_remote_write` and `otlp`, where the
  consumer **pushes** to COS. Opposite directions, different charms, different
  failure modes.
- `logs` cannot say whether the consumer pushes to Loki directly
  (`loki_push_api`) or through an OTel collector (`otlp`); those are different
  relations with different in-model topology.
- A category is redefined per product, so a `metrics` key in one catalog cannot
  be matched to a `metrics` key in another without a curated mapping that
  restates the contract and rots.

A category is also the wrong *join key*: it cannot tell a consumer that a
producer satisfies the exact interface it needs. `prometheus_remote_write` can,
which is what lets a consumer written for COS work unchanged against Cos Lite:
Mimir and Prometheus both satisfy `prometheus_remote_write`.

### 5.3 `prometheus_remote_write`, not `remote_write`

Keep the full interface name. `remote_write` drops the protocol's owner and is
ambiguous next to `otlp` (also a write path) and its sibling `prometheus_scrape`;
it is a second vocabulary that must be mapped back to the real one, and it
drifts from the charmcraft declaration. The verbosity is the precision, and the
name is already the thing Juju joins on.

### 5.4 Duplicate signals are the consumer's choice

Interface keys make the transport explicit, which is what lets a consumer avoid
sending the same signal twice. OTLP and Prometheus remote-write can both carry
metrics; if a consumer names both and sends metrics over both, COS stores the
series twice. The convention does not prevent this — it makes it visible. The
rule is:

- **Name exactly one transport per signal per producer.** A consumer that wants
  metrics over remote-write names `prometheus_remote_write` and not `otlp`; one
  that wants everything over OTLP names `otlp` alone.
- **Different signals may use different transports.** Logs and traces over
  `otlp` plus metrics over `prometheus_remote_write` is correct and not a
  duplicate: each interface carries a distinct signal.
- **Selecting is the consumer's job.** The producer publishes the whole set;
  the consumer's input type is where the selection is declared and reviewed.

### 5.5 Stability and breaking changes

- **Additive is safe.** A producer may add a key; consumers typed with a subset
  are unaffected because Terraform drops the extra attributes.
- **Removal or rename is breaking.** It changes the contract. A producer that
  drops or renames a key announces it, and consumers pinned to a ref keep
  working until they bump. The interface name is owned by the relation, not by
  a module, so this should be rare.
- **Interface spellings are not aliased in Terraform.** If two charms disagree
  on the interface name for one relation, that is a charm-side defect to fix
  upstream, not a translation to add to the catalog.

## 6. Catalog reference: `cos_offers`

COS and Cos Lite publish the same catalog name, `cos_offers`. The keys they
share are interchangeable; each adds the interfaces only it implements.

| Key (interface) | COS role | COS offer | Cos Lite offer |
| --- | --- | --- | --- |
| `grafana_dashboard` | requires | `grafana_dashboards` | `grafana_dashboards` |
| `karma_dashboard` | provides | `alertmanager_karma_dashboard` | `alertmanager_karma_dashboard` |
| `loki_push_api` | provides | `loki_logging` | `loki_logging` |
| `otlp` | provides | `otelcol_receive_otlp` | `otelcol_receive_otlp` |
| `prometheus_remote_write` | provides | `mimir_receive_remote_write` | `prometheus_receive_remote_write` |
| `prometheus_scrape` | requires | — | `prometheus_metrics_endpoint` |
| `tracing` | provides | `otelcol_receive_traces` | — |

"COS role" is which side of the relation COS is on: `provides` means COS serves
the endpoint a consumer integrates to; `requires` means COS consumes an endpoint
a consumer must supply. The data flow follows from it — `prometheus_scrape`
pulls from the consumer, `prometheus_remote_write`, `otlp`, `loki_push_api`, and
`tracing` push to COS — which is exactly the distinction a `metrics` key erases.

Note `prometheus_remote_write`: backed by Mimir in COS and by Prometheus in Cos
Lite. A consumer naming it works against either, because the key is the
interface, not the backing charm.

## 7. Other catalogs

The convention is not COS-specific. Any producer publishes a catalog named for
its interface family, with keys from its charms' `charmcraft.yaml`:

```hcl
# illustrative: a database product publishing its own catalog
output "postgresql_offers" {
  type = object({
    postgresql_client = string
  })
  value = { postgresql_client = juju_offer.client.url }
}
```

A consumer of that product declares a matching `postgresql_offers` input. Two
producers of the same interface family publish the same name so they stay
interchangeable, and Atelier — or any matcher — joins on name and type alone.
Atelier itself carries no interface vocabulary; the names are the products'
own.

### Producers

| Module | Catalog | Keys |
| --- | --- | --- |
| `canonical/observability-stack` `terraform/cos` | `cos_offers` | `grafana_dashboard`, `karma_dashboard`, `loki_push_api`, `otlp`, `prometheus_remote_write`, `tracing` |
| `canonical/observability-stack` `terraform/cos-lite` | `cos_offers` | `grafana_dashboard`, `karma_dashboard`, `loki_push_api`, `otlp`, `prometheus_remote_write`, `prometheus_scrape` |

### Consumers

| Module | Input | Required keys |
| --- | --- | --- |
| `canonical/spark-k8s-bundle` `terraform/products/charmed-spark` | `cos_offers` | `grafana_dashboard`, `loki_push_api`, `prometheus_remote_write` |

A consumer that predates the convention (for example, a product whose
`cos_offers` still keys on `dashboard`, `logging`, `metrics`, `tracing`) adopts
it by re-keying to the interface names and typing the required subset. The
catalog name does not change; only the keys do.

## 8. Adoption

### Producer checklist

1. Enumerate every relation the product can integrate over — both sides.
2. Resolve each relation's `interface:` name from the backing charm's
   `charmcraft.yaml`. Do not use the endpoint or offer name, and do not invent
   a category.
3. Publish one output named for the interface family (`cos_offers`), typed
   `object({ <interface> = string, … })`, each value the offer `.url`.
4. Publish the full set the product participates in; do not narrow it for a
   particular consumer.
5. Keep the output name and keys identical to other producers of the same
   family.
6. Gate CI with `atelier contract lint --module <dir> --output cos_offers`.
7. Treat a key removal or rename as a breaking change and announce it.

### Consumer checklist

1. Declare an input named exactly like the producer's catalog (`cos_offers`).
2. Type it `object({ <interface> = string, … })` naming exactly the interfaces
   the product requires — the type is the requirement, and the wire is
   whole-or-none. Use `map(string)` plus `contains(keys(…))` only if an
   arbitrary subset must be accepted.
3. Never gate `count`/`for_each` on a value; gate on null-ness or keys (§4.5).
4. Name at most one transport per signal per producer (§5.4).
5. Wire with `input = module.<producer>.<output>` and leave the producer's extra
   keys to Terraform.

## 9. Conformance

- **Shape.** `atelier contract lint --module <dir> --output <catalog>` verifies
  the named output exists, is an object of strings, and — with
  `--require <interface>` — contains the interfaces a consumer asserts.
- **Match.** Interface matching is over names and declared types only; no tool
  reads charm metadata, and no provider vocabulary is embedded anywhere.
- **Interchange.** A consumer's input type is the test: if it type-checks
  against two producers' catalogs, they are interchangeable for that consumer.

## 10. Open questions

- **Per-interface composition.** Sourcing different interfaces of one catalog
  from different producers is not specified; a catalog is wired whole (or, for
  a `map`, one producer's keys).
- **A catalog registry / compatibility matrix.** Deriving which consumer pairs
  which producer, per interface, from the catalogs alone is future work.
- **Splitting `otlp` by signal.** Today `otlp` is one interface; whether a
  product that must distinguish OTLP logs from OTLP traces needs finer keys is
  open, and would be a producer decision, not a category reintroduction.
- **Non-Juju producers.** The shape is provider-agnostic; a non-Juju producer
  publishes its own catalog under the same rules.
