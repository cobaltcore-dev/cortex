<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Monitoring Cortex

The knowledge chapter ended with KPIs turning features into Prometheus metrics; this page is the
operational follow-through — scraping those metrics and wiring alerts for a running deployment. Cortex
exports metrics under the `cortex_` prefix and ships `PrometheusRule` alerts with its domain bundles.

## Before you begin

- A domain bundle installed — see [Installing a domain bundle](../01-getting-started/08-installing-a-domain-bundle.md).
- Prometheus (typically kube-prometheus-stack) with the Prometheus Operator, so `ServiceMonitor` and
  `PrometheusRule` objects are honoured.

## Step 1 — Enable the metrics endpoint

The manager's metrics bind address defaults to `0` (disabled). Enable it and, in most clusters, serve
it securely (the metrics flags live in `cmd/manager`). Through the chart, render a `ServiceMonitor` via
the library `prometheus.enable` key (on by default; set it under the bundle's subchart alias):

```yaml
# values.yaml
cortex:
  prometheus:
    enable: true
```

```bash
helm upgrade cortex-nova helm/bundles/cortex-nova --values values.yaml
```

Verify the endpoint is scraped (the bundle labels its `ServiceMonitor`
`app.kubernetes.io/instance: cortex-nova`):

```bash
kubectl get servicemonitor -l app.kubernetes.io/instance=cortex-nova
```

Then confirm metrics appear in Prometheus:

```
cortex_filter_weigher_pipeline_requests_total
cortex_scheduler_api_request_duration_seconds_count
```

## Step 2 — Enable alerts

Alerts ship as a `PrometheusRule` per domain bundle (Nova, Cinder, Manila) and the placement-shim
bundle. Enable them and label the rule for your Prometheus:

```yaml
# values.yaml
alerts:
  enabled: true
  prometheus: my-prometheus   # matches your Prometheus 'ruleSelector'
```

For Nova, choose severity for the scheduling-down alert:

```yaml
kvm:
  enabled: true
  criticalAlerts: true   # CortexNovaSchedulingDown becomes 'critical'
```

Verify the rule is loaded (the bundle names it `cortex-nova-alerts` and labels it `type: alerting-rules`
plus your `prometheus` value):

```bash
kubectl get prometheusrules cortex-nova-alerts
```

Expected — the `PrometheusRule` containing the `cortex-nova-alerts` group.

> [!NOTE]
> The IronCore and Pods bundles ship no alert rules. The placement-shim bundle has its own rule set
> (`CortexPlacementShimDown`, downstream/upstream error and latency alerts, remote-apiserver
> reachability). The alert definitions live in each bundle's `templates/alerts.yaml`.

## Step 3 — Add dashboards

Dashboards are not part of the Helm charts. Import them from the repository:

- Perses: `tools/perses/dashboards/`
- Plutono/Grafana: `tools/plutono/provisioning/dashboards/`

These include a compute KVM overview and a placement-shim status dashboard, and are covered in
[The infrastructure dashboard](05-infrastructure-dashboard.md).

## What to watch

| Signal | Metric / alert |
|---|---|
| Scheduling healthy | `cortex_scheduler_api_request_duration_seconds`, `CortexNovaSchedulingDown` |
| Knowledge fresh | `cortex_feature_pipeline_step_features`, `CortexNovaKnowledgeDown` |
| Datasources syncing | `cortex_sync_request_processed_total`, `CortexNovaDatasourceUnready` |
| Overlay draining | `cortex_cache_overlay_entries`, `CortexNovaCacheOverlayNotDraining` |
| Remotes reachable | `cortex_multicluster_remote_apiserver_reachable` |
| Commitments covered | `cortex_committed_resource_unfulfilled`, oversubscription alert |

## How this relates to Cortex

Monitoring is where the whole book's machinery becomes observable: the `cortex_` metrics come from
`pkg/monitoring` ([The monitoring package](../06-cortex-library/06-monitoring-package.md)), the KPIs
from the [knowledge database](04-kpis-and-metrics-api.md), and the multicluster and overlay signals
from the [library](../06-cortex-library/readme.md). The alerts encode the failure surfaces each chapter
described. With observability in place, the next chapter looks at a lifecycle action Cortex takes on the
fleet itself: automated overcommit.

## Next

[Prev: The infrastructure dashboard](05-infrastructure-dashboard.md) · [Next: Chapter 5 — Hypervisor lifecycle management »](../05-hypervisor-lifecycle/readme.md)
