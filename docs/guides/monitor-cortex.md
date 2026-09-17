<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Monitor Cortex

This guide sets up metrics scraping and alerting for a Cortex deployment. Cortex exports Prometheus
metrics under the `cortex_` prefix and ships `PrometheusRule` alerts with its domain bundles. For
the full metric and alert catalog, see [Metrics and alerts](../reference/metrics.md).

## Before you begin

- A domain bundle installed — see [Install a domain bundle](install-a-domain-bundle.md).
- Prometheus (typically kube-prometheus-stack) with the Prometheus Operator, so `ServiceMonitor` and
  `PrometheusRule` objects are honoured.

## Step 1 — Enable the metrics endpoint

The manager's metrics bind address defaults to `0` (disabled). Enable it and, in most clusters,
serve it securely (see [CLI flags](../reference/cli-flags.md)). Through the chart, render a
`ServiceMonitor` via the library `prometheus.enable` key (on by default; set it under the bundle's
subchart alias):

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

Verify the rule is loaded (the bundle names it `cortex-nova-alerts` and labels it
`type: alerting-rules` plus your `prometheus` value):

```bash
kubectl get prometheusrules cortex-nova-alerts
```

Expected — the `PrometheusRule` containing the `cortex-nova-alerts` group.

> [!NOTE]
> The IronCore and Pods bundles ship no alert rules. The placement-shim bundle has its own rule set
> (`CortexPlacementShimDown`, downstream/upstream error and latency alerts, remote-apiserver
> reachability). See [Metrics and alerts](../reference/metrics.md).

## Step 3 — Add dashboards

Dashboards are not part of the Helm charts. Import them from the repository:

- Perses: `tools/perses/dashboards/`
- Plutono/Grafana: `tools/plutono/provisioning/dashboards/`

These include a compute KVM overview and a placement-shim status dashboard.

## What to watch

| Signal | Metric / alert |
|---|---|
| Scheduling healthy | `cortex_scheduler_api_request_duration_seconds`, `CortexNovaSchedulingDown` |
| Knowledge fresh | `cortex_feature_pipeline_step_features`, `CortexNovaKnowledgeDown` |
| Datasources syncing | `cortex_sync_request_processed_total`, `CortexNovaDatasourceUnready` |
| Overlay draining | `cortex_cache_overlay_entries`, `CortexNovaCacheOverlayNotDraining` |
| Remotes reachable | `cortex_multicluster_remote_apiserver_reachable` |
| Commitments covered | `cortex_committed_resource_unfulfilled`, oversubscription alert |

## Next steps

- Reference: [Metrics and alerts](../reference/metrics.md), [Configuration](../reference/configuration.md) (`monitoring.*`)
- Reference: [Helm values](../reference/helm-values.md)
