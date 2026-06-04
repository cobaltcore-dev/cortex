# Changelog

## 2026-06-01 — [#901](https://github.com/cobaltcore-dev/cortex/pull/901)

### cortex v0.0.60 (sha-12c6f24d)

Non-breaking changes:
- Fix quota filter to use Knowledge CRD flavor groups for Limes summary RAM conversion ([#898](https://github.com/cobaltcore-dev/cortex/pull/898))
- Refactor reservations: extract shared `ResourcesToBlock` to replace duplicated reservation blocking logic ([#896](https://github.com/cobaltcore-dev/cortex/pull/896))
- Update `github.com/sapcc/go-bits` ([#894](https://github.com/cobaltcore-dev/cortex/pull/894), [#897](https://github.com/cobaltcore-dev/cortex/pull/897), [#899](https://github.com/cobaltcore-dev/cortex/pull/899))
- Update `github.com/cobaltcore-dev/openstack-hypervisor-operator` to v1.2.3 ([#900](https://github.com/cobaltcore-dev/cortex/pull/900))

### cortex-nova v0.0.73 (sha-12c6f24d)

Includes updated chart cortex v0.0.60.

## 2026-05-27 — [#893](https://github.com/cobaltcore-dev/cortex/pull/893)

### cortex v0.0.59 (sha-6bc914a6)

Non-breaking changes:
- Probe `os_type` for KVM servers during server sync ([#886](https://github.com/cobaltcore-dev/cortex/pull/886))
- Fix: subtract VM allocations when counting placeable slots ([#891](https://github.com/cobaltcore-dev/cortex/pull/891))
- Fix: kvm-report-capacity to ignore VM allocations ([#885](https://github.com/cobaltcore-dev/cortex/pull/885))
- Add CR safeguards, throttle CRD creation, adding limit ([#884](https://github.com/cobaltcore-dev/cortex/pull/884))
- Fix postgres: rebuild image to resolve CVEs ([#888](https://github.com/cobaltcore-dev/cortex/pull/888))
- Update `github.com/sapcc/go-bits` ([#889](https://github.com/cobaltcore-dev/cortex/pull/889))
- Update `peter-evans/create-pull-request` action to v8 ([#890](https://github.com/cobaltcore-dev/cortex/pull/890))

### cortex-postgres v0.6.4 (sha-8cc792c5)

Non-breaking changes:
- Rebuild image to resolve CVEs ([#888](https://github.com/cobaltcore-dev/cortex/pull/888))

### cortex-nova v0.0.72 (sha-6bc914a6)

Includes updated charts cortex v0.0.59 and cortex-postgres v0.6.4.

## 2026-05-22 — [#883](https://github.com/cobaltcore-dev/cortex/pull/883)

### cortex v0.0.58 (sha-97506e29)

Non-breaking changes:
- Fix: pipeline validating webhook prevented updates ([#882](https://github.com/cobaltcore-dev/cortex/pull/882))
- Fix: use pipelines for CR scheduling requests that don't write history ([#881](https://github.com/cobaltcore-dev/cortex/pull/881))
- Fix: capacity filter considers also reservations that are placed but waiting for 2nd reconcile cycle for status update ([#880](https://github.com/cobaltcore-dev/cortex/pull/880))
- Add shadow mode and decision metric to quota filter ([#876](https://github.com/cobaltcore-dev/cortex/pull/876))

### cortex-nova v0.0.71 (sha-97506e29)

Includes updated charts cortex v0.0.58 and cortex-postgres v0.6.3.

## 2026-05-22 — [#877](https://github.com/cobaltcore-dev/cortex/pull/877)

### cortex v0.0.57 (sha-110712de)

Non-breaking changes:
- Fix: CR alerts fixed ([#870](https://github.com/cobaltcore-dev/cortex/pull/870))
- Enrich CommittedResource kubectl wide view with status summary ([#868](https://github.com/cobaltcore-dev/cortex/pull/868))
- Fix: update for flavors with memory amount without vram ([#869](https://github.com/cobaltcore-dev/cortex/pull/869))
- Add `kvm_committed_resource_reservation` weigher ([#854](https://github.com/cobaltcore-dev/cortex/pull/854))
- Fix(scheduling): skip weigher validation when no hosts to weigh ([#865](https://github.com/cobaltcore-dev/cortex/pull/865))
- Add greq logger context and cycle status log to capacity controller ([#864](https://github.com/cobaltcore-dev/cortex/pull/864))
- Fix: update KVM host OS version retrieval to use status field ([#872](https://github.com/cobaltcore-dev/cortex/pull/872))
- Update `github.com/sapcc/go-bits` ([#878](https://github.com/cobaltcore-dev/cortex/pull/878))

### cortex-postgres v0.6.2 (sha-b012ae82)

Non-breaking changes:
- Bump to PostgreSQL 18.4 and apply base image security upgrades ([#863](https://github.com/cobaltcore-dev/cortex/pull/863))

### cortex-nova v0.0.70 (sha-110712de)

Includes updated charts cortex v0.0.57 and cortex-postgres v0.6.2.

## 2026-05-20 — [#866](https://github.com/cobaltcore-dev/cortex/pull/866)

### cortex v0.0.56 (sha-83b608ea)

Non-breaking changes:
- Simplify dry run logic for committed resources ([#862](https://github.com/cobaltcore-dev/cortex/pull/862))

### cortex-nova v0.0.69 (sha-83b608ea)

Includes updated chart cortex v0.0.56.

## 2026-05-18 — [#861](https://github.com/cobaltcore-dev/cortex/pull/861)

### cortex v0.0.55 (sha-3ec99921)

Non-breaking changes:
- Quota: improve status completeness and observability ([#858](https://github.com/cobaltcore-dev/cortex/pull/858))
- Fix: CR syncer uses units correctly ([#859](https://github.com/cobaltcore-dev/cortex/pull/859))
- Make RAM unit per flavor group operator-configurable ([#860](https://github.com/cobaltcore-dev/cortex/pull/860))

### cortex-nova v0.0.68 (sha-3ec99921)

Includes updated chart cortex v0.0.55.

## 2026-05-18 — [#857](https://github.com/cobaltcore-dev/cortex/pull/857)

### cortex v0.0.54 (sha-3981e731)

Non-breaking changes:
- Add quota enforcement filter ([#855](https://github.com/cobaltcore-dev/cortex/pull/855))
- Quota endpoint handles AZ without lighthouse cluster ([#856](https://github.com/cobaltcore-dev/cortex/pull/856))
- Update external dependencies ([#852](https://github.com/cobaltcore-dev/cortex/pull/852))
- Update `github.com/sapcc/go-bits` ([#851](https://github.com/cobaltcore-dev/cortex/pull/851))

### cortex-nova v0.0.67 (sha-3981e731)

Includes updated chart cortex v0.0.54.

## 2026-05-18 — [#850](https://github.com/cobaltcore-dev/cortex/pull/850)

### cortex v0.0.53 (sha-aa518eb5)

Non-breaking changes:
- LIQUID info: set `QuotaUpdateNeedsProjectMetadata` to true ([#849](https://github.com/cobaltcore-dev/cortex/pull/849))

### General

Non-breaking changes:
- Remove non-docker tests from CI ([#834](https://github.com/cobaltcore-dev/cortex/pull/834))

### cortex-nova v0.0.66 (sha-aa518eb5)

Includes updated chart cortex v0.0.53.

## 2026-05-13 — [#848](https://github.com/cobaltcore-dev/cortex/pull/848)

### cortex v0.0.52 (sha-8dd7100b)

Non-breaking changes:
- Fix: tolerate unreachable remote clusters during field index setup ([#844](https://github.com/cobaltcore-dev/cortex/pull/844))
- Fix: CR API responses matching Limes validations ([#843](https://github.com/cobaltcore-dev/cortex/pull/843))
- Fix: start API server only after cache sync to prevent startup race ([#836](https://github.com/cobaltcore-dev/cortex/pull/836))
- Add Prometheus metrics and alerting for the committed resource ([#840](https://github.com/cobaltcore-dev/cortex/pull/840))

### cortex-nova v0.0.65 (sha-8dd7100b)

Includes updated chart cortex v0.0.52.

## 2026-05-12 — [#845](https://github.com/cobaltcore-dev/cortex/pull/845)

### cortex v0.0.51 (sha-98597910)

Non-breaking changes:
- Fix: CR API responses matching Limes validations ([#843](https://github.com/cobaltcore-dev/cortex/pull/843))
- Fix: start API server only after cache sync to prevent startup race ([#836](https://github.com/cobaltcore-dev/cortex/pull/836))
- Add Prometheus metrics and alerting for the committed resource ([#840](https://github.com/cobaltcore-dev/cortex/pull/840))

### cortex-nova v0.0.64 (sha-98597910)

Includes updated chart cortex v0.0.51.

## 2026-05-12 — [#842](https://github.com/cobaltcore-dev/cortex/pull/842)

### cortex v0.0.50 (sha-c8663afb)

Non-breaking changes:
- Fix(CR): align the RAM resource unit exposed to Limes for CR/quota based on fixed/varying ram/core ratio ([#841](https://github.com/cobaltcore-dev/cortex/pull/841))

### cortex-nova v0.0.63 (sha-c8663afb)

Includes updated chart cortex v0.0.50.

## 2026-05-11 — [#839](https://github.com/cobaltcore-dev/cortex/pull/839)

### cortex v0.0.49 (sha-b570ae10)

Non-breaking changes:
- Track instance count (VM count) per project/AZ in quota ([#837](https://github.com/cobaltcore-dev/cortex/pull/837))
- Fix: LIQUID API info and capacity endpoint bugs ([#838](https://github.com/cobaltcore-dev/cortex/pull/838))
- Register ProjectQuota multicluster router in main ([#831](https://github.com/cobaltcore-dev/cortex/pull/831))

### cortex-nova v0.0.62 (sha-b570ae10)

Includes updated chart cortex v0.0.49.

## 2026-05-11 — [#830](https://github.com/cobaltcore-dev/cortex/pull/830)

### cortex v0.0.48 (sha-86af7a6e)

Non-breaking changes:
- Add CPU core committed resources — cores commitments use arithmetic headroom checks against `FlavorGroupCapacity.Status.TotalCapacity` instead of creating Reservation CRDs ([#826](https://github.com/cobaltcore-dev/cortex/pull/826))
- Split `ProjectQuota` CRD into per-AZ CRDs (one CRD per project+AZ), add `AvailabilityZone` field to spec, and flatten status fields to per-AZ `map[string]int64` ([#827](https://github.com/cobaltcore-dev/cortex/pull/827))
- Add `ProjectQuotaResourceRouter` for multicluster routing of `ProjectQuota` CRDs by availability zone ([#827](https://github.com/cobaltcore-dev/cortex/pull/827))
- Add `FlavorGroupCapacityResourceRouter` for multicluster routing of `FlavorGroupCapacity` CRDs by availability zone ([#824](https://github.com/cobaltcore-dev/cortex/pull/824))
- Register `FlavorGroupCapacity` router in manager's multicluster client ([#828](https://github.com/cobaltcore-dev/cortex/pull/828))
- Add `TotalCapacity` field to `FlavorGroupCapacityStatus` for tracking total capacity of eligible hosts in an empty-datacenter scenario ([#826](https://github.com/cobaltcore-dev/cortex/pull/826))
- Report capacity per resource type (RAM, cores, instances) in `report-capacity` endpoint instead of flat slot count ([#826](https://github.com/cobaltcore-dev/cortex/pull/826))
- Switch LIQUID API commitment unit from multiples of the smallest flavor's RAM to a fixed 1 GiB per unit ([#822](https://github.com/cobaltcore-dev/cortex/pull/822))
- Rename resource types in project capacity metrics: `vcpu` → `cpu`, `memory` → `ram` to align with host capacity metrics ([#823](https://github.com/cobaltcore-dev/cortex/pull/823))
- Rename metrics for unused VMware commitments to clarify they represent the unused portion, not total ([#820](https://github.com/cobaltcore-dev/cortex/pull/820))
- Disable weighers in `vmware-hana-bin-packing` pipeline (filters-only mode) ([#816](https://github.com/cobaltcore-dev/cortex/pull/816))
- Fix concurrency issue in CommittedResource CRD updates ([#825](https://github.com/cobaltcore-dev/cortex/pull/825))
- Update `go.xyrillian.de/gg` v1.7.0 (renamed from `github.com/majewsky/gg`), `sapcc/go-api-declarations` v1.22.0, `sapcc/go-bits`, `openstack-hypervisor-operator` v1.2.2 ([#817](https://github.com/cobaltcore-dev/cortex/pull/817), [#818](https://github.com/cobaltcore-dev/cortex/pull/818))
- Update `controller-gen` to v0.21.0 (CRD annotation bump)
- Update `actions/create-github-app-token` to v3 ([#819](https://github.com/cobaltcore-dev/cortex/pull/819))
- Use beefy runner for CodeQL workflow

### cortex-nova v0.0.61 (sha-86af7a6e)

Includes updated chart cortex v0.0.48.

Non-breaking changes:
- Remove all weighers from `vmware-hana-bin-packing` pipeline template ([#816](https://github.com/cobaltcore-dev/cortex/pull/816))

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
