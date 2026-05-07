# Changelog

## 2026-05-07 — [#814](https://github.com/cobaltcore-dev/cortex/pull/814)

### cortex v0.0.47 (sha-b8cecd0c)

Non-breaking changes:
- Add `ProjectQuota` CRD with per-resource, per-AZ quota breakdown and PAYG (pay-as-you-go) calculation support ([#796](https://github.com/cobaltcore-dev/cortex/pull/796))
- Add `FlavorGroupCapacity` CRD and background capacity controller that pre-computes per-flavor VM slot capacity for each (flavor group × AZ) pair on a configurable interval ([#728](https://github.com/cobaltcore-dev/cortex/pull/728))
- Report capacity from `FlavorGroupCapacity` CRDs in `POST /commitments/v1/report-capacity` — replaces placeholder zeros with real values; stale CRDs report last-known capacity
- Move CommittedResource usage computation from the API handler into a dedicated reconciler that persists results in CRD status, making usage data available to both the LIQUID API and quota controller ([#800](https://github.com/cobaltcore-dev/cortex/pull/800))
- Add KVM OS version as a label to KVM host capacity metrics ([#810](https://github.com/cobaltcore-dev/cortex/pull/810))
- Add KVM project usage metrics (running VMs and resource usage per project/flavor) ([#803](https://github.com/cobaltcore-dev/cortex/pull/803))
- Add `domain_id` and name to vmware project capacity metrics ([#802](https://github.com/cobaltcore-dev/cortex/pull/802))
- Include `domain_id` in vmware project commitment KPI ([#806](https://github.com/cobaltcore-dev/cortex/pull/806))
- Add weighing explainer for scheduling decisions, surfacing per-host scoring rationale ([#808](https://github.com/cobaltcore-dev/cortex/pull/808))
- Move KVM host capacity metric into infrastructure plugins package ([#809](https://github.com/cobaltcore-dev/cortex/pull/809))
- Remove deprecated per-compute infrastructure KPIs (`flavor_running_vms`, `host_running_vms`, `resource_capacity_kvm`) ([#807](https://github.com/cobaltcore-dev/cortex/pull/807))
- Rename hypervisor `ClusterRoleBinding` objects to avoid `roleRef` conflicts on redeploy ([#804](https://github.com/cobaltcore-dev/cortex/pull/804))
- Move bundle-specific RBAC templates from the library chart into individual bundle charts (`cortex-ironcore`, `cortex-pods`) ([#797](https://github.com/cobaltcore-dev/cortex/pull/797))
- Move webhook templates from library chart back into `cortex-nova` bundle (reverts earlier move) ([#805](https://github.com/cobaltcore-dev/cortex/pull/805))
- Fix: add `identity-domains` as a KPI dependency
- Fix: remove `ignoreAllocations` from kvm-report-capacity pipeline to unblock deployment against older admission webhook ([#812](https://github.com/cobaltcore-dev/cortex/pull/812))
- Fix: suppress nova scheduling alerts on transient `no such host` DNS errors
- Replace `testlib.Ptr` helper with native `new()` across test files ([#801](https://github.com/cobaltcore-dev/cortex/pull/801))

### cortex-nova v0.0.60 (sha-b8cecd0c)

Includes updated chart cortex v0.0.47.

Non-breaking changes:
- Add Prometheus datasource for KVM project usage metrics
- Add KVM project usage KPI CRD templates
- Add KVM project utilization KPI CRD templates
- Update `cortex-nova` RBAC to grant permissions for `FlavorGroupCapacity` and `ProjectQuota` CRDs

## 2026-05-04 — [#793](https://github.com/cobaltcore-dev/cortex/pull/793)

### cortex v0.0.46 (sha-ab6eb45d)

Non-breaking changes:
- Fix capacity filter to correctly account for multi-VM CommittedResource reservation slots — confirmed VMs are now summed (not just the last one), blocks are clamped to zero when confirmed exceeds slot size, and spec-only VMs larger than remaining slot are fully covered
- Expose `prometheusDatasourceControllerParallelReconciles` config option to allow parallel reconciles in the Prometheus datasource controller, reducing initial sync latency
- Remove `Conf` field from PrometheusDatasourceReconciler — config is now loaded internally via `conf.GetConfig` during `SetupWithManager`
- Add operator-controlled per-resource-type config (`flavorGroupResourceConfig`) for committed resources, replacing runtime derivation from flavor group metadata; supports wildcard (`*`) catch-all for unknown groups
- Propagate `AnnotationCreatorRequestID` from the change-commitments API to the CommittedResource CRD and through the reservation controller for end-to-end request tracing

### cortex-nova v0.0.59 (sha-ab6eb45d)

Includes updated chart cortex v0.0.46.

Non-breaking changes:
- Remove all committed resource related Prometheus alerts (info API, change API, usage API, capacity API, and syncer alerts)
- Add `flavorGroupResourceConfig` to cortex-nova values.yaml with a wildcard default that sets `hasCapacity: true` for ram, cores, and instances

## 2026-05-04 — [#779](https://github.com/cobaltcore-dev/cortex/pull/779)

### cortex v0.0.45 (sha-1fb35660)

Non-breaking changes:
- Add CommittedResource CRD definition and controller that watches CommittedResource objects and manages child Reservation CRUD
- Add `AllowRejection` field to CommittedResourceSpec for controlling placement failure behavior
- Add vmware project utilization KPI tracking instances per project/flavor and capacity per host
- Move vmware resource commitments KPI to new infrastructure plugins package with shared utilities
- Move vmware host capacity KPI to infrastructure plugins package
- Add basic support for flavor groups for failover reservation with consolidation weigher
- Add `useFlavorGroupResources` values.yaml key for cortex-nova (default: false)
- Update external dependencies (controller-runtime v0.24.0, go-sqlite3 v1.14.44, zap v1.28.0)
- Alert only on new vm faults (avoid re-alerting on historical faults)

### cortex-shim v0.1.0 (sha-d8bb12ef)

Breaking changes:
- Remove `traits.static` values.yaml key and Helm-managed static traits ConfigMap template — traits are now fully managed by the shim at runtime via a single ConfigMap

Non-breaking changes:
- Add per-request feature mode override via `X-Cortex-Feature-Mode` header
- Refactor /traits API to single-ConfigMap model with reusable Syncer interface pattern
- Implement feature-gated /resource_classes API with ConfigMap storage (passthrough, hybrid, crd modes)
- Add ResourceClassSyncer for periodic upstream sync into local ConfigMap
- Add `resourceClasses.configMapName` values.yaml key for configuring the resource classes ConfigMap name
- Support traits and aggregates endpoints per resource provider with three feature modes (passthrough, hybrid, crd)
- Exercise all three feature modes in placement shim e2e tests
- Fix nil pointer panic in feature mode override guard

### cortex-postgres v0.6.0 (sha-88f03a41)

Breaking changes:
- Upgrade PostgreSQL from 17.9 to 18.3 — resource names now include a `-v{major}` suffix for zero-downtime upgrades (e.g., `cortex-nova-postgresql-v18`). After deploy, operators must remove old StatefulSets and PVCs manually.

Non-breaking changes:
- Add versioned resource naming with `cortex-postgres.versionedFullname` helper for zero-downtime PG major upgrades
- Add `major` values.yaml key (default: "18") to control version suffix
- Set PGDATA to subdirectory to avoid lost+found conflict

### cortex-nova v0.0.58 (sha-1fb35660)

Includes updated charts cortex v0.0.45 and cortex-postgres v0.6.0.

Non-breaking changes:
- Reorganize KPI CRD templates for infrastructure dashboard metrics
- Add `useFlavorGroupResources` values.yaml key for failover reservations (default: false)
- Restructure committedResource config keys into nested objects (`committedResourceReservationController`, `committedResourceController`, `committedResourceAPI`)
- Add `committedResourceSyncInterval` config key for syncer reconciliation interval

### cortex-placement-shim v0.1.0 (sha-d8bb12ef)

Includes updated chart cortex-shim v0.1.0.

Breaking changes:
- Remove `traits.static` values.yaml key (inherited from cortex-shim breaking change)

Non-breaking changes:
- Add `resourceClasses.configMapName` values.yaml key

### General

Non-breaking changes:
- Fix bump-artifact workflow to handle concurrent changes on main with concurrency groups and freshness checks
- Add reusable `bump-chart.sh` script for CI chart version bumps
- Add pull-request-creator Claude agent
- Add changelog update command and workflow for release PRs
- Add linting workflow for scaffold completeness checks
- Make /release claude command idempotent
- Don't run helm-lint workflow when release PR is in draft
- Update actions/setup-python action to v6
- Fix stale documentation: traits model, pipeline name, and API path
