# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.0.36] - 2026-02-XX

- Removed the `prometheuscommunity/postgres-exporter` sidecar container for `cortex-postgres` due to security vulnerabilities. Metrics of the exporter are no longer in use as part of the migration away from PostgreSQL.
- Updated `cortex-postgres` to version `v0.5.10` which includes the above change and other minor updates to the postgres image.

## [v0.0.35] - 2026-02-16

_Bundle charts v0.0.35, Cortex core v0.0.22_

- Handle limes api 404 response
- Code cleanups
- Upgrade to Go v1.26
- External dependency upgrades

## [v0.0.34] - 2026-02-09

_Bundle charts v0.0.34, Cortex core v0.0.21_

- We made a mistake in the last release, leading to the updated docker image tag not being released.

## [v0.0.33] - 2026-02-09

_Bundle charts v0.0.33, Cortex core v0.0.20_

- Increasing and improving logging.

## [v0.0.32] - 2026-02-09

_Bundle charts v0.0.32, Cortex core v0.0.19_

- Fix bug in multicluster client helper for indexing where home cluster was indexed twice if no remote target for a resource is given

## [v0.0.31] - 2026-02-09

_Bundle charts v0.0.31, Cortex core v0.0.18_

- Improved logging in nova filters
- Add flavor usage dashboard
- Fix multicluster indexing
- Minor code wording refactoring
- Added project labels to capacity usage kpis

## [v0.0.30] - 2026-02-06

_Bundle charts v0.0.30, Cortex core v0.0.17_

- External dependency upgrades
- Workload spawner fixes (local development tooling)
- Add support for a filter to exclude a set of hosts

## [v0.0.29] - 2026-02-05

_Bundle charts v0.0.29, Cortex core v0.0.16_

- Change the way the multicluster client detects if a client object needs to be maintained in a remote cluster

## [v0.0.28] - 2026-02-04

_Bundle charts v0.0.28, Cortex core v0.0.15_

- Fix bug in nova candidate gatherer where multicluster client was not used properly, leading to 0 hypervisors being returned in the List function call

## [v0.0.27] - 2026-02-04

_Bundle charts v0.0.27, Cortex core v0.0.14_

- Add support to override the nova host preselection and gather all kvm hypervisors as candidates instead
- Make it configurable via the pipeline CRD

## [v0.0.26] - 2026-02-04

_Bundle charts v0.0.26, Cortex core v0.0.13_

- Add capability to trigger experimental scheduler features for kvm filter pipeline validation
- Bump other bundles to stay consistent with versioning

## [v0.0.25] - 2026-02-02

_Bundle charts v0.0.25, Cortex core v0.0.12_

- Fixed cinder and manila alerts
- Local development QoL improvements
- Documentation updates
- Upgrade external dependencies

## [v0.0.24] - 2026-01-28

_Bundle charts v0.0.24, Dist chart v0.0.11, Cortex postgres v0.5.8_

- Fix vulnerabilities in cortex-postgres image
- Add project name as label to `cortex_flavor_running_vms` kpi

## [v0.0.23] - 2026-01-28

_Dist chart v0.0.10, Bundle charts v0.0.23_

- Refined pipeline CRD and scheduling/lib module
- Convenience fixes for local development setup

## [v0.0.22] - 2026-01-26

- Fix datasource and knowledge alerts
- Fix mismatch between docker image tag and chart appversion

## [v0.0.21] - 2026-01-26

- Split kvm related pipelines, knowledges, datasources and kpis into separate files
- Enable/disable kvm related custom resources
- Minor dependency upgrades

## [v0.0.20] - 2026-01-22

- Bug fix for kpi unready alert
- Removed step unready alert
- Minor bugfixes

## [v0.0.19] - 2026-01-20

_Release Cortex chart v0.0.5_

- Work in progress on pod scheduling
- Cortex for KVM no longer depends on the Placement service
- Filter, Weigher, KPIs and Knowledge updated to work with hypervisor CRD
- Add running VMs per flavor and project KPI
- Merged Step with Pipeline CRD
- Implemented filter for [anti]affinity
- Implemented filter for requested destination
- Improved alerts and dashboards
- Initial support for pod scheduling
- Use hypervisor CRD for filtering

## [v0.0.18] - 2025-12-12

- Fix memory leak in clean up tasks and nova api
- Dependency upgrades
- KVM Infrastructure dashboard

## [v0.0.17] - 2025-12-08

- Update app version

## [v0.0.16] - 2025-12-08

- Removed database dependency from knowledge controller. Only datasources now require database connectivity
- New KPIs for KVM infrastructure monitoring:
  - `cortex_kvm_host_capacity_total` - Total physical capacity per compute host
  - `cortex_kvm_host_capacity_utilized` - Capacity consumed by running workloads per host
  - `cortex_kvm_host_capacity_payg` - Available pay-as-you-go capacity per host
  - `cortex_kvm_host_capacity_reserved` - Reserved capacity per host (placeholder implementation pending Cortex Reservation CRD integration)
  - `cortex_kvm_host_capacity_failover` - Failover-reserved capacity per host (placeholder implementation pending Cortex failover CRD integration)

## [v0.0.15] - 2025-12-04

_Multiple charts released with bugfixes_

- Fix memory leak in datasource controller
- Fix periodic loops
- Use correct cluster to index fields

## [v0.0.14] - 2025-12-02

- Refactored metrics service to use http instead of https- Adjusted ServiceMonitors to use http endpoint

## [v0.0.13] - 2025-12-01

_Bundle charts v0.0.13_

- Fix missing key
- Bump to v0.0.13

## [v0.0.12] - 2025-12-01

_Bundle charts v0.0.12_

- Alerting changes
- Dependency bumps

## [v0.0.11] - 2025-11-27

_Cortex chart v0.0.2, Cortex postgres v0.5.6_

- Sync appVersion across bundles to `v0.0.11`- Synced apiVersion of charts to `v2`
- Bump cortex chart to v0.0.2
- Bump cortex-postgres to v0.5.6
- Fix push charts workflow
- Rewrite cortex based on kubernetes controllers and provide new CRDs.
- Fix Syncer of Flavors, Server, Migrations
- Fix Postgres Image
- Add commitments to Infrastructure Dashboard via Limes KPIs

## [v0.0.10] - 2025-09-24

_Bundle charts v0.0.10_

- Dependency updates and ci/cd changes
- Fix crb namespace conflict on deployment
- Refactor features and kpis for infrastructure dashboard
- Various reservations operator enhancements
- Reservations inclusion in nova filter and nova scheduler api
- Namespace alerts

## [v0.0.9] - 2025-09-10

_Bundle charts v0.0.9, Cortex core v0.25.1_

- Reject `resize` requests from nova- Added feature to allow multiple pipelines per scheduler- Created `cortex-core` alerts that are being shared across all the bundles- Created bundle specific alerts- Host Utilization KPI was refactored to Host Capacity KPI- Implemented improvements for Cortex Infrastructure Dashboard  - All hosts without a `SAPPHIRE_RAPIDS` trait are now being considered as `cascade-lakes` in the infrastructure dashboard  - Added `project_id` filter  - Added feature for `pinned_projects` of a host- Updated `go` to `1.25`- Updated external dependencies

## [v0.0.8] - 2025-08-19

- Added workload spawner as command- Added pipeline premodifier to test the implemented nova filters- Modified and added nova filters- Added `cortex-cinder` chart- Added syncer for cinder storage pools- Added syncer for prometheus `netapp_volume_aggregate_labels` metric- Added cinder scheduler api (pass-through mode)- Clean up tests by replacing plain table names in sql queries with `Insert()` or `TableName()` method calls- Added hypervisor status disabled as filter for the `feature_sap_host_details`- Added `service_disabled_reason` comment from `openstack_hypervisors` to  `disabled_reason` in `feature_sap_host_details`

## [v0.0.7] - 2025-08-13

_Cortex core v0.24.6, Bundle charts v0.0.7_

- Add all ported nova filters and set them to sandboxed so they are not executed until validated
- Implement nova spec for use in replay command to validate filters
- Add sandbox flag to scheduler request to enable execution of sandboxed filters
- Refactor safety mechanism to consider deduplication and add safety mechanism for filters
- Vastly improved e2e test for nova using real project/domain/hypervisor/flavors

## [v0.0.6] - 2025-08-11

- Rollback `cortex-postgres` chart to appVersion `sha-6482610` to mitigate CVE-2025-6965 (Integer Truncation in SQLite)

## [v0.0.5] - 2025-08-11

_Cortex nova chart v0.0.5, Postgres chart v0.5.2_

- Set severity of `CortexNovaHost<Resource>UtilizationAbove100Percent` alerts back to `info`
- Bumped Go version to `1.24.6`
- Fixed PostgreSQL Dockerfile by updating base image to Debian Trixie
- Restructured capacity dashboard layout and organization
- Added `sap_host_details_extractor` that maps workload type (general-purpose, hana), CPU architecture (cascade-lake, sapphire-rapids), hypervisor family (vmware, kvm), and availability zone to hypervisors
- Added `host_az_extractor` that maps availability zones to hypervisors
  - The `aggregates` openstack syncer now also stores aggregates without assigned hosts
- Added `sap_host_running_vms_kpi` metric to track the number of running VMs per host
- Renamed `host_utilization_kpi` → `sap_host_utilization_kpi` and use data from `sap_host_details_extractor`
- Renamed `total_capacity_kpi` → `sap_total_capacity_kpi` and use data from `sap_host_details_extractor`
- Capacity dashboard now uses adjusted metrics (`sap_host_utilization_kpi`, `sap_total_capacity_kpi`, `sap_host_running_vms_kpi`)

## [v0.0.4] - 2025-08-06

_Cortex core v0.24.3, Cortex postgres v0.5.1_

- Includes minor fixes and a patch that rejects ironic schedulings.
- Use custom postgres image containing newer base debian
- Increased alert timeout for sync objects dropped to zero (flapped for limes objects)
- Fix templating in alerts from last helm chart refactoring
- Add vm commitments kpi for dashboard

## [v0.0.3] - 2025-08-01

_Bundle charts v0.0.3, Cortex core v0.24.2_

- Cleanup work and minor fixes
- Increase alert severity to see them in slack
- Add status field to commitments syncer

## [v0.0.2] - 2025-07-31

- Add commitments syncer for limes
- Remove bitnami charts and their dependencies
- Docs and capacity dashboard updates

## [v0.0.1] - 2025-07-28

_Major restructuring_

- Restructure cortex helm charts into library, dev, and bundle charts
- Add dc dashboard to upstream code base + trend analysis
- Use VerticalPodAutoscaler to set resource requests and limits
- Regular maintenance: dependency updates, cleanup

---

Below are older releases which are kept for historical reference but may not be complete or follow the same format as the newer releases.

## Cortex Core [v0.23.4] - 2025-07-17

- Publish different telemetry so the visualizer displays correct host capacities
- Use host utilization feature from resource provider inventory usage (placement) instead of hypervisor stats
- Update readme wording and add logo

## Cortex Core [v0.23.3] - 2025-07-16

_Cortex chart v0.23.3, Cortex prometheus chart v0.4.2_

- Add alert for failed deschedulings
- Fix multiple aliases for same step overriding config (leading to only one of hana binpacking or resource balancing being initialized)

## Cortex Core [v0.23.2] - 2025-07-15

- Adds support for configuring aliases for scheduler steps.

## Cortex Core [v0.23.1] - 2025-07-14

- Release the new resource balancing weigher.

## Cortex Core [v0.23.0] - 2025-07-14

_Cortex chart v0.23.0 and associated charts_

- Minor dependency updates (e.g. go-bits)
- Local dev setup improvements (disable postgres upgrade in tilt)
- Improve pre-upgrade hook handling of cortex and cortex-postgres helm chart
- Remove default resource limits as they are handled by vpa_butler in production (can still be configured)
- Detect when hypervisor resource usage gets above 100% and throw an alert
- Sync domains and projects from keystone for better datacenter dashboard filtering + kpi and extractor
- Add cpu balancing weigher for manila based on NetApp metrics (can be enabled through config)
- Visualizer now supports manila, upgrade tooling to support manila replaying
- Add serializer for storage pools needed by manila visualizer
- Add basic descheduler for virtual machines with demo step

## Cortex Core [v0.22.4] - 2025-07-03

- Fix erroneous migration 003
- Fix improper hypervisor pagination

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

_Associated charts included_

- Add generic scope for scheduler steps (e.g. execute step only for hana flavors)
- Check step scope before step execution instead of after to save time
- Sync nova aggregates to resolve availability zone of hypervisors
- Fix vm host residency panel/metric which utilized the wrong server field
- Various dashboard improvements, split dashboard into smaller sub-dashboards
- Add new host capacity dashboard and various metrics supporting this dashboard
- Add database monitoring (metrics + dashboard)
- Make e2e tests configurable so we can disable specific tests

## Cortex Chart [v0.21.0] - 2025-06-16

_Cortex chart v0.21.0, Cortex prometheus chart v0.3.0_

- Add Manila api and pipeline including a simple capacity balancing weigher (can bin-pack if configured inversely)
- Sync manila storage pools needed for manila scheduling, extract resource utilization percentage as feature
- Add a down alert for the new manila scheduler and set them to info level for now
- Don't use deprecated Nova metadata anymore for capacity calculation, instead use Placement inventories/usages and fix the formula
- Optimize the vrops hostsystem resolver feature
- Don't use the flavor_binpacking weigher anymore - it's replaced by the multifunctional resource_balancing weigher
- Use the host_capacity feature in the corresponding host_utilization KPI
- Dependency updates and go upgrade to v1.24.4
- Minor dev workspace and documentation changes

## Cortex Chart [v0.20.0] - 2025-06-10

_Associated charts included_

- We renamed the cortex-scheduler kubernetes service to cortex-scheduler-nova to prepare for cortex-scheduler-manila
- Service code restructurings to prepare for the future
- Avoid burst calculation of features with recency flag
- Monitor skipped feature extractions
- Don't skip feature extractors until they have provided > 0 features
- Add impact metric in nova external scheduler
- Support hypervisor/trait/flavor scoping and add resource balancing scheduler step
- Wrap cli pod in deployment to avoid unmanaged pod alerts and profit from lifecycle management
- Rewrite vm_life_span extractor to store only its histogram in the db
- Record in dashboard when feature extractors take >10s to execute
- Tilt setup polishing / dependency updates / minor changes / dashboard updates

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