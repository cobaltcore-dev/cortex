<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Metrics and alerts

Cortex exports Prometheus metrics under the `cortex_` prefix and ships PrometheusRule alerts with
its Helm bundles. See [Monitor Cortex](../guides/monitor-cortex.md) for setup.

## Metrics

Metrics are registered through `pkg/monitoring` and served on the metrics bind address (see
[CLI flags](cli-flags.md)). The tables below group them by source.

### Knowledge and datasources

| Metric | Type | Measures |
|---|---|---|
| `cortex_db_connection_attempts_total` | Counter | Database connection attempts. |
| `cortex_db_select_duration_seconds` | Histogram | SELECT query duration. |
| `cortex_db_connections_max_open` / `_open` / `_in_use` / `_idle` | Gauge | Connection pool state. |
| `cortex_sync_objects` | Gauge | Objects synced per datasource. |
| `cortex_sync_request_duration_seconds` | Histogram | Datasource sync duration. |
| `cortex_sync_request_processed_total` | Counter | Datasource syncs processed. |
| `cortex_feature_pipeline_step_run_duration_seconds` | Histogram | Extractor step duration. |
| `cortex_feature_pipeline_step_features` | Gauge | Features produced per extractor. |
| `cortex_feature_pipeline_step_skipped` | Counter | Skipped extractor steps. |

### Scheduling

| Metric | Type | Measures |
|---|---|---|
| `cortex_scheduler_api_request_duration_seconds` | Histogram | External scheduler API request duration (labelled by status). |
| `cortex_filter_weigher_pipeline_run_duration_seconds` | Histogram | Full pipeline run duration. |
| `cortex_filter_weigher_pipeline_step_run_duration_seconds` | Histogram | Per-step duration. |
| `cortex_filter_weigher_pipeline_step_impact` / `_weight_modification` / `_shift_origin` | Histogram/Gauge | Per-step effect on host weights. |
| `cortex_filter_weigher_pipeline_step_removed_hosts` | Histogram | Hosts removed per filter. |
| `cortex_filter_weigher_pipeline_host_number_in` / `_out` | Histogram | Candidate count in/out. |
| `cortex_filter_weigher_pipeline_requests_total` | Counter | Pipeline runs. |
| `cortex_detector_pipeline_run_duration_seconds` | Histogram | Detector (descheduling) pipeline duration. |
| `cortex_detector_pipeline_step_detections` | Gauge | Detections per detector. |
| `cortex_nova_placement_total` / `cortex_nova_no_host_found_total` | Counter | Nova placement outcomes. |
| `cortex_nova_filter_quota_enforcement_decisions_total` | Counter | Quota-enforcement filter decisions. |

### Reservations, capacity, quota

| Metric | Type | Measures |
|---|---|---|
| `cortex_reservations` | Gauge | Number of reservations. |
| `cortex_reservations_allocated_resources` | Gauge | Reserved/allocated resources. |
| `cortex_committed_resource_unfulfilled` | Gauge | Commitments without full reservation coverage. |
| `cortex_committed_resource_host_oversubscribed` | Gauge | Oversubscribed hosts. |
| `cortex_committed_resource_host_oversubscribed_evicted_reservations_total` | Counter | Evicted reservation slots. |
| `cortex_committed_resource_syncer_*` | Counter/Gauge | Limes commitment syncer activity. |
| `cortex_committed_resource_usage_*` | Histogram/Gauge | CR usage reconciler. |
| `cortex_committed_resource_{change,quota,capacity,usage,info}_api_*` | Counter/Histogram | LIQUID API endpoints. |
| `cortex_committed_resource_capacity_*` / `cortex_committed_resource_*_gib` / `_slots` | Gauge | Capacity controller output. |
| `cortex_quota_total_usage` / `_payg_usage` / `_cr_usage` | Gauge | Project quota usage. |
| `cortex_quota_reconcile_duration_seconds` / `_total` | Histogram/Counter | Quota reconcile. |
| `cortex_failover_*` | Counter/Gauge/Histogram | Failover reservation reconciliation. |

### Platform

| Metric | Type | Measures |
|---|---|---|
| `cortex_cache_overlay_entries` / `_entries_max` | Gauge | Pending-cache overlay occupancy. |
| `cortex_multicluster_remote_apiserver_reachable` | Gauge | Per-remote reachability. |
| `cortex_multicluster_cross_cluster_name_conflicts_total` | Counter | Cross-cluster name conflicts. |
| `cortex_log_messages_total` | Counter | Log messages by level. |
| `cortex_placement_shim_downstream_request_duration_seconds` | Histogram | Shim request duration (client → shim). |
| `cortex_placement_shim_upstream_request_duration_seconds` | Histogram | Shim request duration (shim → Placement). |
| `cortex_placement_shim_manager_up` | Gauge | 1 while the shim's managed controller is running. |

### KPI-derived (`KPI` CRDs)

KPIs export gauges such as `cortex_datasource_state`, `cortex_knowledge_state`,
`cortex_pipeline_state`, `cortex_kpi_state`, `cortex_decision_state`, `cortex_reservation_state`,
`cortex_multicluster_object_count`, plus domain KPIs (`cortex_kvm_host_capacity_*`,
`cortex_vm_*`, `cortex_vmware_*`, `cortex_netapp_storage_pool_cpu_usage_*`, etc.). The exact set
depends on which `KPI` CRDs are installed. See [KPI](crds/kpi.md).

## Alerts

Alerts ship as `PrometheusRule` objects in the Nova, Cinder, Manila, and placement-shim bundles,
gated by `alerts.enabled` and labelled with `alerts.prometheus`. IronCore and Pods bundles carry
no alert rules.

| Bundle | Rule group | Notable alerts |
|---|---|---|
| `cortex-nova` | `cortex-nova-alerts` (≈43) | `CortexNovaSchedulingDown`, `CortexNovaKnowledgeDown`, `CortexNovaHttpRequest400sTooHigh`, `CortexNovaDatasourceUnready`, `CortexNovaPipelineUnready`, `CortexNovaCacheOverlayNotDraining`, `CortexNovaMulticlusterNameConflicts`, `CortexCommittedResourceHostOversubscribed`, and the committed-resource API alerts. |
| `cortex-cinder` | `cortex-cinder-alerts` (≈16) | Cinder equivalents (`domain="cinder"`). |
| `cortex-manila` | `cortex-manila-alerts` (≈15) | Manila equivalents (`domain="manila"`). |
| `cortex-placement-shim` | (≈11) | `CortexPlacementShimDown`, `CortexPlacementShimManagerLooping`, downstream/upstream 4xx/5xx and latency alerts, `CortexPlacementShimRemoteApiserverUnreachable`. |

`CortexNovaSchedulingDown` is `critical` when `kvm.enabled` and `kvm.criticalAlerts`, otherwise
`warning`. All alerts carry `support_group: workload-management`.

## Dashboards

Dashboards are not part of the Helm charts. They live under `tools/perses/dashboards/`
(Perses) and `tools/plutono/provisioning/dashboards/` (Plutono/Grafana), including a compute KVM
overview, a placement-shim status dashboard, and per-domain views.

## Next steps

- Guide: [Monitor Cortex](../guides/monitor-cortex.md)
- Reference: [Configuration](configuration.md) (`monitoring.*` keys), [Helm values](helm-values.md)
