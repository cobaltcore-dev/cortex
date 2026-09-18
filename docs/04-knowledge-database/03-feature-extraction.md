<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Feature extraction

Raw datasource rows are facts, not insight. Feature extraction is the middle stage of the knowledge flow: a
`Knowledge` resource names an *extractor* that reads the cached datasource rows, does the expensive
joining and aggregation once, and writes the result back as a **feature** the rest of Cortex can read
cheaply. This page covers the extractor model, how a `Knowledge` resource declares its inputs, and the
recency mechanism that keeps features fresh without recomputing them on every request.

## Extractors: read rows, write features

An extractor is a small plugin implementing the `FeatureExtractor` interface
(`internal/knowledge/extractor/plugins`): `Init(datasourceDB, client, spec)` wires it to the datasource
Postgres database and the `Knowledge` spec, and `Extract() ([]Feature, error)` runs the derivation. Most
compute extractors pair a `.go` file with a `.sql` file — the base helper runs the SQL query against the
datasource database and boxes the rows into typed features.

Extractors are dispatched by a hand-maintained map, `supportedExtractors`, in
`internal/knowledge/extractor/supported_extractors.go` — the same static-registration pattern the KPIs use.
There are two plugin families:

- **compute** (`plugins/compute/`) — the bulk of them: `host_utilization_extractor` (joins a hypervisor's
  inventory with its usage into a utilization percentage), `host_capabilities_extractor`, `host_az_extractor`,
  `flavor_groups`, `vm_host_residency_extractor`, `vm_life_span_histogram_extractor`, the vROps resolvers and
  contention extractors, and `kvm_libvirt_domain_cpu_steal_pct_extractor` (the feature the
  [`avoid_high_steal_pct` detector](../02-external-scheduler-api/02-nova-smart-scheduling.md) acts on).
- **storage** (`plugins/storage/`) — currently `netapp_storage_pool_cpu_usage_extractor`, the feature behind
  Manila's one weigher.

To add an extractor, implement the interface and register it in `supportedExtractors` under a new name — the
process in [Extend Cortex](../02-external-scheduler-api/07-extending-cortex.md).

## Declaring inputs and where features live

A `Knowledge` resource names its inputs explicitly in `spec.dependencies`:

```yaml
apiVersion: cortex.cloud/v1alpha1
kind: Knowledge
metadata:
  name: host-utilization
spec:
  extractor: host_utilization_extractor
  recency: 60s
  dependencies:
    datasources:
      - name: nova-hypervisors
      - name: placement-usages
```

`dependencies.datasources` lists the `Datasource` resources the extractor reads (a `Knowledge` may also
depend on other `Knowledge` resources via `dependencies.knowledges`). All referenced datasources must share
the same database secret — the extractor joins across their tables, so they have to live in one Postgres
database. The controller enforces this: if the referenced datasources disagree on `databaseSecretRef`, the
`Knowledge` reports the condition reason `InconsistentDatabaseSecretRefs` and does not run.

The extracted features are written to the resource's own status, not to a separate table: `status.raw` holds
the boxed feature list under a `{"features": [...]}` envelope, alongside `rawLength`, `lastExtracted`, and
`lastContentChange` (which advances only when the features actually change, so consumers can tell a real
update from a no-op re-run).

## Recency: fresh enough, not recomputed constantly

`spec.recency` (default `60s`) is the contract for staleness: the controller re-runs the extractor when the
features are older than the recency window, comparing `status.lastExtracted` against `spec.recency`. This is
the knob that makes the intermediate feature step pay off — a `host_utilization` feature is derived at most
once per recency window and then read by every weigher for free, rather than recomputed on each placement
request.

> [!NOTE]
> A tighter `recency` gives fresher features at the cost of more extraction work against Postgres. Match it
> to how fast the underlying signal actually moves — utilization drifts slowly; a per-second value gains
> nothing from a per-second re-extraction.

## Inspecting knowledge resources

`Knowledge` is cluster-scoped. List what is deployed and see extraction state at a glance:

```bash
kubectl get knowledges
```

```
NAME               DOMAIN   CREATED   EXTRACTED   CHANGED   RECENCY   FEATURES   READY
host-utilization   nova     3d        45s         12m       60s       1240       True
```

`Extracted` is the last run (`.status.lastExtracted`), `Changed` the last time the features actually
differed (`.status.lastContentChange`), and `Features` the number produced (`.status.rawLength`) — so a
`Changed` timestamp far older than `Extracted` means the extractor is re-running but the signal is stable.
Read the conditions (including `InconsistentDatabaseSecretRefs`) and the extracted feature envelope with:

```bash
kubectl get knowledge host-utilization -o yaml
```

## How this relates to Cortex

Feature extraction is the value-add stage of the [knowledge flow](01-knowledge-flow-overview.md): it turns
the datasource rows of the [previous page](02-datasources.md) into the query-ready features that every
weigher in [Chapter 2](../02-external-scheduler-api/readme.md) and every reservation controller in
[Chapter 3](../03-reservations-and-inventory/readme.md) reads. The `Knowledge` CRD is documented
field-by-field on the `Knowledge` type in `api/v1alpha1/knowledge_types.go`. The next page turns features into
metrics — the observability half of the flow.

## Next

[Prev: Datasources](02-datasources.md) · [Next: KPIs and the metrics endpoint »](04-kpis-and-metrics-api.md)
