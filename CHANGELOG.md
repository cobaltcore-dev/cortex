# Changelog

## [v0.0.36] - Under development

- Removed the `prometheuscommunity/postgres-exporter` sidecar container for `cortex-postgres` due to security vulnerabilities. Metrics of the exporter are no longer in use as part of the migration away from PostgreSQL.
- Updated `cortex-postgres` to version `v0.5.10` which includes the above change and other minor updates to the postgres image.

## [v0.0.35] - 2026-02-16

- Bundle charts v0.0.35, Cortex core v0.0.22
- Fixed limes commitments sync failures caused by 404 status code responses.
- Performed code cleanup and refactoring.
- Upgraded to Go v1.26.
- Updated external dependencies.

## [v0.0.34] - 2026-02-09

- Bundle charts v0.0.34, Cortex core v0.0.21
- Fixed docker image tag release issue from previous version.

## [v0.0.33] - 2026-02-09

- Bundle charts v0.0.33, Cortex core v0.0.20
- Enhanced and expanded logging capabilities.

## [v0.0.32] - 2026-02-09

- Bundle charts v0.0.32, Cortex core v0.0.19
- Fixed bug in multicluster client helper where home cluster was indexed twice when no remote target was specified.

## [v0.0.31] - 2026-02-09

- Bundle charts v0.0.31, Cortex core v0.0.18
- Improved logging in nova filters.
- Added flavor usage dashboard.
- Fixed multicluster indexing.
- Refactored code for clarity and consistency.
- Added project labels to capacity usage KPIs.

## [v0.0.30] - 2026-02-06

- Bundle charts v0.0.30, Cortex core v0.0.17
- Updated external dependencies.
- Fixed workload spawner issues in local development environment.
- Added support for host exclusion filters.

## [v0.0.29] - 2026-02-05

- Bundle charts v0.0.29, Cortex core v0.0.16
- Improved multicluster client detection logic for remote cluster object maintenance.

## [v0.0.28] - 2026-02-04

- Bundle charts v0.0.28, Cortex core v0.0.15
- Fixed nova candidate gatherer bug that caused zero hypervisors to be returned due to improper multicluster client usage.

## [v0.0.27] - 2026-02-04

- Bundle charts v0.0.27, Cortex core v0.0.14
- Added support to override nova host preselection and gather all KVM hypervisors as candidates.
- Made nova host preselection configurable via pipeline CRD.

## [v0.0.26] - 2026-02-04

- Bundle charts v0.0.26, Cortex core v0.0.13
- Added capability to trigger experimental scheduler features for KVM filter pipeline validation.
- Updated bundle versions for consistency.

## [v0.0.25] - 2026-02-02

- Bundle charts v0.0.25, Cortex core v0.0.12
- Fixed cinder and manila alerts.
- Improved local development experience.
- Updated documentation.
- Upgraded external dependencies.

## [v0.0.24] - 2026-01-28

- Bundle charts v0.0.24, Dist chart v0.0.11, Cortex postgres v0.5.8
- Fixed security vulnerabilities in cortex-postgres image.
- Added project name label to `cortex_flavor_running_vms` KPI.

## [v0.0.23] - 2026-01-28

- Dist chart v0.0.10, Bundle charts v0.0.23
- Refined pipeline CRD and scheduling/lib module.
- Improved local development setup with convenience fixes.

## [v0.0.22] - 2026-01-26

- Fixed datasource and knowledge alerts.
- Fixed mismatch between docker image tag and chart appVersion.

## [v0.0.21] - 2026-01-26

- Split KVM-related pipelines, knowledges, datasources, and KPIs into separate files.
- Added ability to enable/disable KVM-related custom resources.
- Updated external dependencies.

## [v0.0.20] - 2026-01-22

- Fixed KPI unready alert.
- Removed step unready alert.
- Applied various bug fixes.

## [v0.0.19] - 2026-01-20

- Release Cortex chart v0.0.5
- Implemented work in progress on pod scheduling.
- Removed Cortex for KVM dependency on Placement service.
- Updated Filter, Weigher, KPIs, and Knowledge to work with hypervisor CRD.
- Added running VMs per flavor and project KPI.
- Merged Step with Pipeline CRD.
- Implemented [anti]affinity filter.
- Implemented requested destination filter.
- Improved alerts and dashboards.
- Added initial support for pod scheduling.
- Used hypervisor CRD for filtering.

## [v0.0.18] - 2025-12-12

- Fixed memory leak in cleanup tasks and nova API.
- Upgraded external dependencies.
- Added KVM infrastructure dashboard.

## [v0.0.17] - 2025-12-08

- Updated application version.

## [v0.0.16] - 2025-12-08

- Removed database dependency from knowledge controller (only datasources now require database connectivity).
- Added new KPIs for KVM infrastructure monitoring:
  - `cortex_kvm_host_capacity_total` - Total physical capacity per compute host.
  - `cortex_kvm_host_capacity_utilized` - Capacity consumed by running workloads per host.
  - `cortex_kvm_host_capacity_payg` - Available pay-as-you-go capacity per host.
  - `cortex_kvm_host_capacity_reserved` - Reserved capacity per host (placeholder implementation pending Cortex Reservation CRD integration).
  - `cortex_kvm_host_capacity_failover` - Failover-reserved capacity per host (placeholder implementation pending Cortex failover CRD integration).

## [v0.0.15] - 2025-12-04

- Fixed memory leak in datasource controller.
- Fixed periodic loops.
- Used correct cluster to index fields.

## [v0.0.14] - 2025-12-02

- Refactored metrics service to use HTTP instead of HTTPS.
- Adjusted ServiceMonitors to use HTTP endpoint.

## [v0.0.13] - 2025-12-01

- Bundle charts v0.0.13
- Fixed missing key.

## [v0.0.12] - 2025-12-01

- Bundle charts v0.0.12
- Updated alerting configurations.
- Upgraded external dependencies.

## [v0.0.11] - 2025-11-27

- Cortex chart v0.0.2, Cortex postgres v0.5.6
- Synchronized appVersion across bundles to `v0.0.11`.
- Synchronized apiVersion of charts to `v2`.
- Bumped cortex chart to v0.0.2.
- Bumped cortex-postgres to v0.5.6.
- Fixed push charts workflow.
- Rewrote Cortex based on Kubernetes controllers and provided new CRDs.
- Fixed syncer for Flavors, Server, and Migrations.
- Fixed Postgres image.
- Added commitments to Infrastructure Dashboard via Limes KPIs.

## [v0.0.10] - 2025-09-24

- Bundle charts v0.0.10
- Updated external dependencies and CI/CD configurations.
- Fixed ClusterRoleBinding namespace conflict on deployment.
- Refactored features and KPIs for infrastructure dashboard.
- Enhanced reservations operator with various improvements.
- Integrated reservations in nova filter and nova scheduler API.
- Added namespace alerts.

## [v0.0.9] - 2025-09-10

- Bundle charts v0.0.9, Cortex core v0.25.1
- Rejected `resize` requests from Nova.
- Added feature to allow multiple pipelines per scheduler.
- Created `cortex-core` alerts shared across all bundles.
- Created bundle-specific alerts.
- Refactored Host Utilization KPI to Host Capacity KPI.
- Implemented improvements for Cortex Infrastructure Dashboard:
  - All hosts without `SAPPHIRE_RAPIDS` trait now considered as `cascade-lakes` in infrastructure dashboard.
  - Added `project_id` filter.
  - Added feature for `pinned_projects` of a host.
- Updated Go to v1.25.
- Updated external dependencies.

## [v0.0.8] - 2025-08-19

- Added workload spawner as command.
- Added pipeline premodifier to test implemented nova filters.
- Modified and added nova filters.
- Added `cortex-cinder` chart.
- Added syncer for cinder storage pools.
- Added syncer for Prometheus `netapp_volume_aggregate_labels` metric.
- Added cinder scheduler API (pass-through mode).
- Cleaned up tests by replacing plain table names in SQL queries with `Insert()` or `TableName()` method calls.
- Added hypervisor status disabled as filter for `feature_sap_host_details`.
- Added `service_disabled_reason` comment from `openstack_hypervisors` to `disabled_reason` in `feature_sap_host_details`.

## [v0.0.7] - 2025-08-13

- Cortex core v0.24.6, Bundle charts v0.0.7
- Added all ported nova filters and set them to sandboxed mode (not executed until validated).
- Implemented nova spec for use in replay command to validate filters.
- Added sandbox flag to scheduler request to enable execution of sandboxed filters.
- Refactored safety mechanism to consider deduplication and added safety mechanism for filters.
- Vastly improved end-to-end test for nova using real project/domain/hypervisor/flavors.

## [v0.0.6] - 2025-08-11

- Rolled back `cortex-postgres` chart to appVersion `sha-6482610` to mitigate CVE-2025-6965 (Integer Truncation in SQLite).

## [v0.0.5] - 2025-08-11

- Cortex bundle chart v0.0.5, Postgres chart v0.5.2
- Set severity of `CortexNovaHost<Resource>UtilizationAbove100Percent` alerts back to `info`.
- Bumped Go version to v1.24.6.
- Fixed PostgreSQL Dockerfile by updating base image to Debian Trixie.
- Restructured capacity dashboard layout and organization.
- Added `sap_host_details_extractor` that maps workload type (general-purpose, hana), CPU architecture (cascade-lake, sapphire-rapids), hypervisor family (vmware, kvm), and availability zone to hypervisors.
- Added `host_az_extractor` that maps availability zones to hypervisors (the `aggregates` openstack syncer now also stores aggregates without assigned hosts).
- Added `sap_host_running_vms_kpi` metric to track the number of running VMs per host.
- Renamed `host_utilization_kpi` → `sap_host_utilization_kpi` and used data from `sap_host_details_extractor`.
- Renamed `total_capacity_kpi` → `sap_total_capacity_kpi` and used data from `sap_host_details_extractor`.
- Capacity dashboard now uses adjusted metrics (`sap_host_utilization_kpi`, `sap_total_capacity_kpi`, `sap_host_running_vms_kpi`).

## [v0.0.4] - 2025-08-06

- Cortex core v0.24.3, Cortex postgres v0.5.1
- Included minor fixes and a patch that rejects Ironic schedulings.
- Used custom Postgres image containing newer base Debian.
- Increased alert timeout for sync objects dropped to zero (flapped for Limes objects).
- Fixed templating in alerts from last Helm chart refactoring.
- Added VM commitments KPI for dashboard.

## [v0.0.3] - 2025-08-01

- Bundle charts v0.0.3, Cortex core v0.24.2
- Performed cleanup work and applied minor fixes.
- Increased alert severity to enable notifications in Slack.
- Added status field to commitments syncer.

## [v0.0.2] - 2025-07-31

- Added commitments syncer for Limes.
- Removed Bitnami charts and their dependencies.
- Updated documentation and capacity dashboard.

## [v0.0.1] - 2025-07-28

- Restructured Cortex Helm charts into library, dev, and bundle charts.
- Added datacenter dashboard to upstream codebase with trend analysis.
- Used VerticalPodAutoscaler to set resource requests and limits.
- Regular maintenance: dependency updates, cleanup.

---

Below are older releases which are kept for historical reference but may not be complete or follow the same format as the newer releases.

## Cortex Core [v0.23.4] - 2025-07-17

- Published different telemetry so the visualizer displays correct host capacities.
- Used host utilization feature from resource provider inventory usage (Placement) instead of hypervisor stats.
- Updated README wording and added logo.

## Cortex Core [v0.23.3] - 2025-07-16

- Cortex chart v0.23.3, Cortex prometheus chart v0.4.2
- Added alert for failed deschedulings.
- Fixed multiple aliases for same step overriding config (leading to only one of HANA binpacking or resource balancing being initialized).

## Cortex Core [v0.23.2] - 2025-07-15

- Adds support for configuring aliases for scheduler steps.

## Cortex Core [v0.23.1] - 2025-07-14

- Release the new resource balancing weigher.

## Cortex Core [v0.23.0] - 2025-07-14

- Cortex chart v0.23.0 and associated charts
- Updated minor dependencies (e.g., go-bits).
- Improved local dev setup (disabled postgres upgrade in Tilt).
- Improved pre-upgrade hook handling of cortex and cortex-postgres Helm charts.
- Removed default resource limits as they are handled by vpa_butler in production (can still be configured).
- Detected when hypervisor resource usage exceeds 100% and throws an alert.
- Synced domains and projects from Keystone for better datacenter dashboard filtering + KPI and extractor.
- Added CPU balancing weigher for Manila based on NetApp metrics (can be enabled through config).
- Visualizer now supports Manila; upgrade tooling supports Manila replaying.
- Added serializer for storage pools needed by Manila visualizer.
- Added basic descheduler for virtual machines with demo step.

## Cortex Core [v0.22.4] - 2025-07-03

- Fixed erroneous migration 003.
- Fixed improper hypervisor pagination.

## Cortex Postgres [v0.3.1] - 2025-07-02

- After the temporary downgrade, the deployments now support upgrading.

## Cortex Postgres [v0.3.0] - 2025-07-02

- This is required since our services are currently running 15.5.27 in prod regions. We need to rollout the new jobs with this version *once* and can then upgrade. I missed that in the last release.

## Cortex Chart [v0.22.2] - 2025-07-02

- Missing version bump in the chart.

## Cortex Postgres [v0.2.0] - 2025-07-02

- Other minor changes for metrics are included.

## Cortex Chart [v0.22.1] - 2025-06-30

- With v0.22.0, migration 003 can fail if it is not skipped and the table does not yet exist. This PR releases a new version of cortex to fix the issue.

## Cortex Chart [v0.22.0] - 2025-06-30

- Added generic scope for scheduler steps (e.g., execute step only for HANA flavors).
- Checked step scope before step execution instead of after to save time.
- Synced Nova aggregates to resolve availability zone of hypervisors.
- Fixed VM host residency panel/metric which utilized the wrong server field.
- Various dashboard improvements; split dashboard into smaller sub-dashboards.
- Added new host capacity dashboard and various metrics supporting this dashboard.
- Added database monitoring (metrics + dashboard).
- Made end-to-end tests configurable so we can disable specific tests.

## Cortex Chart [v0.21.0] - 2025-06-16

- Cortex chart v0.21.0, Cortex prometheus chart v0.3.0
- Added Manila API and pipeline including a simple capacity balancing weigher (can bin-pack if configured inversely).
- Synced Manila storage pools needed for Manila scheduling; extracted resource utilization percentage as feature.
- Added a down alert for the new Manila scheduler and set them to info level for now.
- Stopped using deprecated Nova metadata for capacity calculation; instead used Placement inventories/usages and fixed the formula.
- Optimized the vROPS hostsystem resolver feature.
- Stopped using the flavor_binpacking weigher - it's replaced by the multifunctional resource_balancing weigher.
- Used the host_capacity feature in the corresponding host_utilization KPI.
- Updated dependencies and upgraded Go to v1.24.4.
- Minor dev workspace and documentation changes.

## Cortex Chart [v0.20.0] - 2025-06-10

- Renamed the cortex-scheduler Kubernetes service to cortex-scheduler-nova to prepare for cortex-scheduler-manila.
- Performed service code restructuring to prepare for the future.
- Avoided burst calculation of features with recency flag.
- Monitored skipped feature extractions.
- Prevented skipping feature extractors until they have provided > 0 features.
- Added impact metric in Nova external scheduler.
- Supported hypervisor/trait/flavor scoping and added resource balancing scheduler step.
- Wrapped CLI pod in deployment to avoid unmanaged pod alerts and profit from lifecycle management.
- Rewrote vm_life_span extractor to store only its histogram in the database.
- Recorded in dashboard when feature extractors take >10s to execute.
- Polished Tilt setup, updated dependencies, made minor changes, and updated dashboards.

## Cortex Chart [v0.16.3] - 2025-05-22

- Maintenance release

## Cortex Chart [v0.16.2] - 2025-05-22

- Chart updates for cortex v0.16.2 and prometheus v0.2.2

## Cortex Chart [v0.16.0] - 2025-05-19

## Cortex Chart [v0.14.1] - 2025-05-13

- Release a new version of Cortex which includes maintenance updates and minor fixes for the prometheus metrics.

## Cortex Alerts Chart [v0.2.1] - 2025-05-09

## Cortex Chart [v0.13.0] - 2025-05-08

## Cortex Chart [v0.12.0] - 2025-04-30

## Cortex Chart [v0.11.2] - 2025-04-25

## Cortex Chart [v0.11.0] - 2025-04-23

## Cortex Chart [v0.10.0] - 2025-04-17

## Cortex Chart [v0.9.0] - 2025-04-09
