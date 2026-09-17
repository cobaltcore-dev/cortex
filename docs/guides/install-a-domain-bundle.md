<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Install a domain bundle

This guide installs Cortex for one platform domain (Nova, Cinder, Manila, IronCore, or Pods) into a
Kubernetes cluster with Helm. You will install the CRDs, then a domain bundle, and verify the
manager is running.

If you are new to Cortex, read the [overview](../concepts/overview.md) first.

## Before you begin

- A Kubernetes cluster and `kubectl` pointed at it.
- Helm 3.
- Access to the Cortex chart directory (`helm/`) or a chart repository hosting the bundles.
- A reachable Postgres, or plan to let the bundle deploy one (`postgres.enabled`).

## Step 1 — Install the CRDs first

The `cortex.cloud/v1alpha1` CRDs must exist before any manager starts. Install them once per
cluster:

```bash
helm install cortex-crds helm/bundles/cortex-crds
```

Expected output:

```
NAME: cortex-crds
STATUS: deployed
```

Verify:

```bash
kubectl get crds | grep cortex.cloud
```

Expected output (abridged) — the eleven owned kinds:

```
datasources.cortex.cloud
knowledges.cortex.cloud
kpis.cortex.cloud
pipelines.cortex.cloud
decisions.cortex.cloud
descheduling.cortex.cloud
...
```

## Step 2 — Choose and install a domain bundle

Each domain has its own bundle. Pick the one for your platform:

| Bundle | Domain |
|---|---|
| `cortex-nova` | Compute (Nova) |
| `cortex-cinder` | Block storage (Cinder) |
| `cortex-manila` | Shared file storage (Manila) |
| `cortex-ironcore` | Bare metal (IronCore) |
| `cortex-pods` | Kubernetes pods |

Provide your configuration through `global.conf` (see [Configuration](../reference/configuration.md))
and, if you have no external database, let the bundle deploy Postgres:

```bash
helm install cortex-nova helm/bundles/cortex-nova \
  --set postgres.enabled=true \
  --values my-values.yaml
```

Where `my-values.yaml` supplies at minimum the datasource credentials and the controllers/tasks you
want enabled. See [Helm values](../reference/helm-values.md).

Expected output:

```
NAME: cortex-nova
STATUS: deployed
```

## Step 3 — Verify the manager is running

```bash
kubectl get pods -l app.kubernetes.io/name=cortex-nova
```

Expected output:

```
NAME                          READY   STATUS    RESTARTS   AGE
cortex-nova-xxxxxxxxxx-xxxxx   1/1     Running   0          30s
```

Check the readiness probe is passing (the manager serves it on `:8081` — see
[CLI flags](../reference/cli-flags.md)):

```bash
kubectl logs deploy/cortex-nova | grep -i "starting manager"
```

## Deploy order recap

1. `cortex-crds` (once per cluster).
2. Postgres (bundled via `postgres.enabled`, or external).
3. The domain bundle(s).

Install additional domains by repeating step 2 with a different bundle in the same cluster; the CRDs
are shared.

## Next steps

- Guide: [Configure datasources, knowledge, and KPIs](configure-knowledge.md)
- Guide: [Monitor Cortex](monitor-cortex.md)
- Reference: [Helm values](../reference/helm-values.md), [Configuration](../reference/configuration.md)
