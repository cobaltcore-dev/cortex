<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Configure datasources, knowledge, and KPIs

This guide wires up the data side of Cortex: a `Datasource` to ingest raw facts, a `Knowledge`
resource to distill them into features, and a `KPI` to publish features as metrics. These three
kinds feed every pipeline. For the concept, see the [CRD/controller model](../concepts/crd-controller-model.md).

## Before you begin

- A domain bundle installed and its manager running — see [Install a domain bundle](install-a-domain-bundle.md).
- The datasource, knowledge, and KPI controllers enabled (`enabledControllers` includes
  `datasource-controllers`, `knowledge-controllers`, `kpis-controller`) — see
  [Configuration](../reference/configuration.md).
- Credentials for the source (OpenStack Keystone or Prometheus) available as a Secret.

## Step 1 — Create a datasource

A `Datasource` declares where raw facts come from and lands them in Postgres. See the
[Datasource reference](../reference/crds/datasource.md) for all fields.

```bash
kubectl apply -f - <<'EOF'
apiVersion: cortex.cloud/v1alpha1
kind: Datasource
metadata:
  name: nova-servers
spec:
  # fields per your source; see the Datasource reference
EOF
```

Verify it becomes Ready and reports synced objects:

```bash
kubectl get datasource nova-servers -o jsonpath='{.status.conditions}'
```

You should also see the sync metrics climb:

```
cortex_sync_objects{...} > 0
cortex_sync_request_processed_total{...} increasing
```

## Step 2 — Extract knowledge

A `Knowledge` resource declares a named extractor that reads raw facts and writes features. The
extractor name must be registered in the binary (extractors live in a hand-maintained map — see
[Extend Cortex](extend-cortex.md)).

```bash
kubectl apply -f - <<'EOF'
apiVersion: cortex.cloud/v1alpha1
kind: Knowledge
metadata:
  name: host-utilization
spec:
  # extractor name + inputs; see the Knowledge reference
EOF
```

Verify features are produced:

```bash
kubectl get knowledge host-utilization -o jsonpath='{.status}'
```

Expected — the extractor step reports features and no skips:

```
cortex_feature_pipeline_step_features{...} > 0
cortex_feature_pipeline_step_skipped{...} == 0
```

See the [Knowledge reference](../reference/crds/knowledge.md).

## Step 3 — Publish a KPI (optional)

A `KPI` turns features into Prometheus gauges for dashboards and alerts:

```bash
kubectl apply -f - <<'EOF'
apiVersion: cortex.cloud/v1alpha1
kind: KPI
metadata:
  name: host-utilization-kpi
spec:
  # KPI name + inputs; see the KPI reference
EOF
```

Verify the metric appears on the metrics endpoint (see [Metrics and alerts](../reference/metrics.md)):

```
cortex_kpi_state{name="host-utilization-kpi"} 1
```

## Troubleshooting

- **Datasource not Ready** — check the referenced Secret and, for OpenStack, that the Keystone
  credentials in `secrets.json` are correct. See [Configuration](../reference/configuration.md).
- **Knowledge skipped** — the extractor name is not registered, or its input features are missing.
  `cortex_feature_pipeline_step_skipped` increments.
- **No KPI metric** — confirm `kpis-controller` is in `enabledControllers`.

## Next steps

- Guide: [Extend Cortex](extend-cortex.md) to add a new extractor or KPI.
- Reference: [Datasource](../reference/crds/datasource.md), [Knowledge](../reference/crds/knowledge.md), [KPI](../reference/crds/kpi.md)
- Concept: [CRD/controller model](../concepts/crd-controller-model.md)
