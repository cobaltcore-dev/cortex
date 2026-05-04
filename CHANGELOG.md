# Changelog

## 2026-05-04 — [#793](https://github.com/cobaltcore-dev/cortex/pull/793)

### cortex v0.0.45 (sha-1d4f049c)

Non-breaking changes:
- Fix capacity filter to correctly account for multi-VM CommittedResource reservation slots — confirmed VMs are now summed (not just the last one), blocks are clamped to zero when confirmed exceeds slot size, and spec-only VMs larger than remaining slot are fully covered
- Expose `prometheusDatasourceControllerParallelReconciles` config option to allow parallel reconciles in the Prometheus datasource controller, reducing initial sync latency
- Remove `Conf` field from PrometheusDatasourceReconciler — config is now loaded internally via `conf.GetConfig` during `SetupWithManager`

### cortex-nova v0.0.58 (sha-1d4f049c)

Non-breaking changes:
- Remove all committed resource related Prometheus alerts (info API, change API, usage API, capacity API, and syncer alerts)

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
