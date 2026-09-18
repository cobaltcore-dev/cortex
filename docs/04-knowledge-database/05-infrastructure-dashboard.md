<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# The infrastructure dashboard

The `infrastructure` KPI family exists for a specific audience: capacity planners and operators who need to
*see* the fleet — where headroom is, how projects stack up, whether HANA workloads are placed the way policy
wants. This page covers those KPIs and the dashboards that render them, which — unlike most Cortex config —
live in `tools/`, not in the Helm charts.

## Infrastructure KPIs: fleet-level planning signals

Where the `compute` KPIs measure individual workloads and the `deployment` KPIs measure Cortex's own health,
the `infrastructure` KPIs (`internal/knowledge/kpis/plugins/infrastructure/`) measure the *fleet* for smart
workload planning:

- **`kvm_host_capacity_kpi` / `vmware_host_capacity_kpi`** — total and remaining capacity per hypervisor,
  emitted per host so a dashboard can show where room is and where it is not.
- **`kvm_project_utilization_kpi` / `vmware_project_utilization_kpi`** — how much each project is actually
  using, for spotting imbalance.
- **`vmware_project_commitments_kpi`** — committed versus used, the billing-vs-scheduling gap made visible
  ([committed-resource reservations](../03-reservations-and-inventory/02-committed-resource-reservations.md)).
- **`kvm_hana_stacking_kpi`** — how HANA workloads are stacked across hosts, for the bin-packing policy.

These read the knowledge database and the `Hypervisor` custom resources, classifying hosts by naming
convention (KVM compute, VMware compute, VMware Ironic) so the metrics carry the right labels for grouping.
Being ordinary Prometheus metrics on the manager `/metrics` endpoint ([previous page](04-kpis-and-metrics-api.md)),
they need no special plumbing to reach a dashboard.

## Dashboards live in `tools/`, not in Helm

The dashboard definitions are development/operations tooling, deliberately kept out of the deployable charts:

- **Perses** — `tools/perses/dashboards/`, including `cortex-compute-kvm-overview.json` (the compute KVM
  overview) and `project.json`.
- **Plutono** — `tools/plutono/provisioning/dashboards/`, including `cortex-placement-shim-status.json`
  (the [shim](../03-reservations-and-inventory/05-placement-api-shim.md) status board), `cortex-status.json`,
  `cortex.json`, and a `compute/` subtree (`kvm-overview.json`, `kvm-details.json`, `vmware-overview.json`,
  `vmware-details.json`, `flavors.json`, `global.json`).

> [!NOTE]
> There are no dashboard JSON files under `helm/`. The only `dashboard:` strings in the charts are
> alert-rule annotations pointing at dashboards by name — the definitions themselves ship in `tools/`. To
> use one, import it into your own Perses or Plutono instance; Cortex does not deploy a dashboard server for
> you.

This page is the visual read-out of the whole [knowledge flow](01-knowledge-flow-overview.md): datasources
cache the world, extractors derive features, the `infrastructure` KPIs turn those features into fleet-level
gauges, and these dashboards render them for planning. It is also where the two halves of the book meet — the
capacity these dashboards show is the capacity the [reservations](../03-reservations-and-inventory/readme.md)
subtract from and the [scheduler](../02-external-scheduler-api/readme.md) weighs against. Every metric is
registered through [the `monitoring` package](../06-cortex-library/06-monitoring-package.md), next to the
subsystem that emits it. The next chapter acts on this same fleet picture:
adjusting hypervisor overcommit automatically.

## Next

[Prev: KPIs and the metrics endpoint](04-kpis-and-metrics-api.md) · [Next: Monitoring Cortex »](06-monitoring.md)
