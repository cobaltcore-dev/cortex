<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# KPIs and the metrics endpoint

The third stage of the knowledge flow turns features into observability. A **KPI** is a plugin that reads
features (and the knowledge database, and Hypervisor CRs) and publishes them as Prometheus metrics for
dashboards and alerts. This page covers the KPI model, the four plugin families, and — importantly — where
those metrics actually surface, because it is *not* a separate API server.

## KPIs are Prometheus collectors, not a service

A KPI implements the `KPI` interface (`internal/knowledge/kpis/plugins`): `Init(db, client, opts)`,
`Describe(ch)`, and `Collect(ch)` — the standard Prometheus collector shape — plus `GetName()`. The KPI
controller (`internal/knowledge/kpis/controller.go`) registers each active KPI directly on
controller-runtime's shared metrics registry (`sigs.k8s.io/controller-runtime/pkg/metrics`) and unregisters
it when the resource goes away.

> [!IMPORTANT]
> There is no dedicated "Metrics API" HTTP server for KPIs. Because the controller registers them on the
> manager's shared registry, KPI metrics are served from the **controller-manager's own `/metrics`
> endpoint** — the same endpoint that carries the sync and reconcile metrics. Scrape that, and the KPI
> gauges are alongside everything else.

Like extractors, KPIs are dispatched by a hand-maintained map — `supportedKPIs` in
`internal/knowledge/kpis/supported_kpis.go`. Registering a new KPI means adding it there; see
[Extend Cortex](../02-external-scheduler-api/07-extending-cortex.md).

## Four plugin families

KPIs are grouped into four plugin directories by what they measure:

- **compute** (`plugins/compute/`) — per-workload and per-host compute signals: `vmware_host_contention_kpi`,
  `vmware_project_noisiness_kpi`, `vm_migration_statistics_kpi`, `vm_life_span_kpi`, `vm_commitments_kpi`,
  `vm_faults_kpi`.
- **storage** (`plugins/storage/`) — `netapp_storage_pool_cpu_usage_kpi`.
- **deployment** (`plugins/deployment/`) — Cortex's *own* health, one KPI per resource kind:
  `datasource_state_kpi`, `knowledge_state_kpi`, `decision_state_kpi`, `kpi_state_kpi`,
  `pipeline_state_kpi`, `reservation_state_kpi`, and `multicluster_object_count_kpi`. These are how you
  monitor Cortex itself (see [Monitoring](06-monitoring.md)).
- **infrastructure** (`plugins/infrastructure/`) — fleet-level capacity and placement-planning signals,
  covered on the [next page](05-infrastructure-dashboard.md): `kvm_host_capacity_kpi`,
  `kvm_project_utilization_kpi`, `kvm_hana_stacking_kpi`, `vmware_host_capacity_kpi`,
  `vmware_project_utilization_kpi`, `vmware_project_commitments_kpi`.

A KPI resource names its extractor-produced inputs the same way a `Knowledge` resource names its datasources,
and the controller re-collects when those inputs change.

## Inspecting KPIs

`KPI` is cluster-scoped. List the deployed KPIs and whether they are collecting:

```bash
kubectl get kpis
```

```
NAME                     CREATED   DOMAIN   READY   DEPENDENCIES   READY
vm_migration_statistics  3d        nova     true    3/3            True
```

`Dependencies` (`.status.dependenciesReadyFrac`) shows how many of the KPI's inputs are ready — a KPI stuck
below `n/n` is waiting on a `Knowledge` or datasource, not on itself. Read the full status with `-o yaml`:

```bash
kubectl get kpi vm_migration_statistics -o yaml
```

Because KPIs publish onto the controller-manager's `/metrics` endpoint (above), confirm one is actually
emitting by scraping that endpoint and looking for its gauge — for the `deployment` family, the
`*_state_kpi` metrics report the health of each Cortex resource kind.

## How this relates to Cortex

KPIs are the read-out end of the [knowledge flow](01-knowledge-flow-overview.md): they consume the features
that [feature extraction](03-feature-extraction.md) writes and expose them where operators and dashboards can
see them. The `deployment` family closes the loop by making Cortex's own resources observable — the metrics
the [Monitoring](06-monitoring.md) page alerts on. The `KPI` CRD is defined by the `KPI` Go type in
`api/v1alpha1/kpi_types.go`, and every metric is registered through
[the `monitoring` package](../06-cortex-library/06-monitoring-package.md). The next page follows the
`infrastructure` KPIs out to
the dashboards they feed.

## Next

[Prev: Feature extraction](03-feature-extraction.md) · [Next: The infrastructure dashboard »](05-infrastructure-dashboard.md)
