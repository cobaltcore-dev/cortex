<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Helm values

Cortex ships as a set of Helm charts under `helm/`. This page lists the values keys a deployment
sets, per chart. For the file-based configuration these charts render, see
[Configuration](configuration.md).

## Chart layout

| Chart | Type | Purpose |
|---|---|---|
| `cortex` | library | Shared templates and defaults for the manager (imported by domain bundles). |
| `cortex-shim` | library | Shared templates for the shim binary. |
| `cortex-crds` | bundle | The `cortex.cloud/v1alpha1` CRDs. Install first. |
| `cortex-nova` | bundle | Nova (compute) manager deployment, pipelines, alerts. |
| `cortex-cinder` | bundle | Cinder (block storage) manager deployment, pipelines, alerts. |
| `cortex-manila` | bundle | Manila (shared file storage) manager deployment, pipelines, alerts. |
| `cortex-ironcore` | bundle | IronCore (bare metal) manager deployment and pipelines. |
| `cortex-pods` | bundle | Kubernetes Pods scheduling manager deployment and pipelines. |
| `cortex-placement-shim` | bundle | Placement API shim deployment (imports `cortex-shim`). |
| `cortex-postgres` | library | Postgres StatefulSet templates used by bundles that need a database. |

> [!NOTE]
> `helm/README.md` is out of date (it lists five bundles and calls `cortex-postgres` a bundle).
> The list above reflects the actual chart directories.

## Common values (manager bundles)

Domain bundles import the `cortex` library and share these keys:

| Key | Type | Description |
|---|---|---|
| `global.conf` | object | Merged verbatim into `/etc/config/conf.json`. This is where [configuration](configuration.md) keys go. |
| `global.registry` / `image.repository` / `image.tag` | string | Manager image. |
| `replicaCount` | int | Manager replicas. |
| `resources` | object | Pod resource requests/limits. |
| `enabledControllers` / `enabledTasks` | []string | Surfaced into `global.conf` (see [Configuration](configuration.md)). |
| `postgres.enabled` | bool | Deploy a bundled Postgres via the `cortex-postgres` library. |
| `secrets` | object | Rendered into the Secret backing `/etc/secrets/secrets.json`. |
| `alerts.enabled` | bool | Render the bundle's `PrometheusRule`. |
| `alerts.prometheus` | string | `prometheus` label selector for the rule. |
| `serviceMonitor.enabled` | bool | Render a `ServiceMonitor` for metrics scraping. |

### Nova-specific (`cortex-nova`)

| Key | Type | Description |
|---|---|---|
| `kvm.enabled` | bool | Mark this deployment as serving KVM; drives `CortexNovaSchedulingDown` severity. |
| `kvm.criticalAlerts` | bool | Escalate scheduling-down to `critical` when `kvm.enabled`. |

## Shim values (`cortex-placement-shim`)

Imports the `cortex-shim` library:

| Key | Type | Description |
|---|---|---|
| `global.conf` | object | Rendered into `conf.json`; holds `placementURL`, `keystoneURL`, `features`, `auth` (see [Configuration](configuration.md)). |
| `args.placementShim` | bool | Pass `--placement-shim`. |
| `args.selfHeal` | bool | Pass `--self-heal` (default on). |
| `secrets` | object | Service credentials and SSO cert/key rendered into `secrets.json`. |
| `alerts.enabled` / `alerts.prometheus` | bool/string | Render the shim `PrometheusRule`. |

> [!NOTE]
> Leader election must stay disabled for the shim (the binary errors out otherwise); the chart
> does not expose a leader-election toggle.

## CRDs (`cortex-crds`)

No functional values — install this chart before any bundle so the `cortex.cloud/v1alpha1` CRDs
exist. See [Install a domain bundle](../guides/install-a-domain-bundle.md).

## Next steps

- Guide: [Install a domain bundle](../guides/install-a-domain-bundle.md)
- Reference: [Configuration](configuration.md), [Metrics and alerts](metrics.md)
