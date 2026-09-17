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

A domain bundle imports the `cortex` library under a subchart alias (e.g. `cortex`,
`cortex-scheduling-controllers`, `cortex-knowledge-controllers`) and sets bundle-level credential
blocks at the top level. The most-used keys:

| Key | Type | Default | Description |
|---|---|---|---|
| `<subchart>.conf` | object | — | Merged into the manager's `conf.json`. Holds `schedulingDomain`, `enabledControllers`, `enabledTasks`, pipeline definitions (see [Configuration](configuration.md)). |
| `<subchart>.namePrefix` | string | `cortex` | Prefix for the deployment and its resources (e.g. `cortex-nova-scheduling`). |
| `<subchart>.controllerManager.replicas` | int | `1` | Manager replicas. |
| `<subchart>.controllerManager.container.image.repository` | string | `ghcr.io/cobaltcore-dev/cortex` | Manager image (tag defaults to the chart `appVersion`). |
| `<subchart>.controllerManager.container.resources` | object | see values | Pod resource requests/limits. |
| `<subchart>.controllerManager.container.logLevel` | string | `info` | `debug` / `info` / `warn` / `error`. |
| `openstack` | object | — | Keystone credentials (`url`, `username`, `password`, `projectName`, `userDomainName`, `projectDomainName`, `availability`, optional `sso`) rendered into the datasource secret. |
| `postgres` | object | — | Datastore connection (`host`, `user`, `password`, `database`, `port`). |
| `prometheus` | object | — | Prometheus datasource (`url`, optional `sso`). |
| `alerts.enabled` | bool | `true` | Render the bundle's `PrometheusRule`. |
| `alerts.prometheus` | string | `openstack` | `prometheus` label selector for the rule. |
| `serviceMonitor.extraLabels` | object | `{}` | Extra labels on the `ServiceMonitor`. |

The `ServiceMonitor` itself is toggled by the library key `<subchart>.prometheus.enable` (default
`true`); metrics generation by `<subchart>.metrics.enable`. A bundled Postgres is deployed by the
`cortex-postgres` subchart block (e.g. `cortex-postgres.fullnameOverride`), not a `postgres.enabled`
flag.

> [!NOTE]
> `enabledControllers` and `enabledTasks` live inside `<subchart>.conf`, e.g.
> `cortex-scheduling-controllers.conf.enabledControllers`. There is no top-level
> `enabledControllers` key.

### Nova-specific (`cortex-nova`)

| Key | Type | Description |
|---|---|---|
| `kvm.enabled` | bool | Mark this deployment as serving KVM; drives `CortexNovaSchedulingDown` severity. |
| `kvm.criticalAlerts` | bool | Escalate scheduling-down to `critical` when `kvm.enabled`. |

## Shim values (`cortex-placement-shim`)

The bundle imports the `cortex-shim` library under the `cortex-shim` alias:

| Key | Type | Description |
|---|---|---|
| `cortex-shim.conf` | object | Rendered into the shim's `conf.json`; holds `placementURL`, `keystoneURL`, `features`, `auth` (see [Configuration](configuration.md)). |
| `cortex-shim.namePrefix` | string | Resource name prefix (e.g. `cortex-placement`). |
| `cortex-shim.deployment.container.extraArgs` | []string | Binary flags. The bundle sets `["--placement-shim=true"]`; add `--self-heal=true` here to change supervisor mode. |
| `cortex-shim.prometheus` | object | ServiceMonitor / metrics toggles. |
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
