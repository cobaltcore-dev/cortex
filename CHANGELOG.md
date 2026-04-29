# Changelog

## 2026-04-29 — [#779](https://github.com/cobaltcore-dev/cortex/pull/779)

### cortex v0.0.45 (sha-dc6bbe7c)

Non-breaking changes:
- Add CommittedResource CRD definition and controller that watches CommittedResource objects and manages child Reservation CRUD
- Add `AllowRejection` field to CommittedResourceSpec for controlling placement failure behavior
- Add vmware project utilization KPI tracking instances per project/flavor and capacity per host
- Move vmware resource commitments KPI to new infrastructure plugins package with shared utilities
- Move vmware host capacity KPI to infrastructure plugins package

### cortex-shim v0.1.0 (sha-17050b2f)

Breaking changes:
- Remove `traits.static` values.yaml key and Helm-managed static traits ConfigMap template — traits are now fully managed by the shim at runtime via a single ConfigMap

Non-breaking changes:
- Add per-request feature mode override via `X-Cortex-Feature-Mode` header
- Refactor /traits API to single-ConfigMap model with reusable Syncer interface pattern
- Implement feature-gated /resource_classes API with ConfigMap storage (passthrough, hybrid, crd modes)
- Add ResourceClassSyncer for periodic upstream sync into local ConfigMap
- Add `resourceClasses.configMapName` values.yaml key for configuring the resource classes ConfigMap name
- Exercise all three feature modes in placement shim e2e tests

### cortex-nova v0.0.58 (sha-dc6bbe7c)

Includes updated chart cortex v0.0.45.

Non-breaking changes:
- Reorganize KPI CRD templates for infrastructure dashboard metrics

### cortex-placement-shim v0.1.0 (sha-17050b2f)

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
