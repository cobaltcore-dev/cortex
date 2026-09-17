<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Datasource

`cortex.cloud/v1alpha1`, Kind `Datasource`, cluster-scoped. A Datasource describes an external
source that Cortex periodically syncs into its Postgres store. See [Configure
datasources](../../guides/configure-knowledge.md).

```bash
kubectl get datasources
```

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `schedulingDomain` | string | — | Domain this datasource serves: `nova`, `cinder`, `manila`, `machines`, `pods`. |
| `type` | string | — | `prometheus` or `openstack`. |
| `prometheus` | object | — | Prometheus datasource config; set when `type: prometheus`. |
| `openstack` | object | — | OpenStack datasource config; set when `type: openstack`. |
| `databaseSecretRef` | SecretReference | — | Secret with keys `username`, `password`, `host`, `port`, `database`. |
| `ssoSecretRef` | SecretReference | — | Optional secret with keys `cert` and `key` for SSO access to the host. |

### `prometheus`

| Field | Type | Default | Description |
|---|---|---|---|
| `query` | string | — | PromQL query fetched as a time series (not instant). |
| `alias` | string | — | Table name the result is stored under; referenced by extractors as a dependency. |
| `type` | string | — | Metric type, mapping to a Cortex metric model. |
| `timeRange` | duration | `2419200s` | Time range to query. |
| `interval` | duration | `86400s` | Query interval. |
| `resolution` | duration | `43200s` | Data resolution. |
| `secretRef` | SecretReference | — | Secret with key `url` (the Prometheus URL). |

### `openstack`

| Field | Type | Default | Description |
|---|---|---|---|
| `type` | string | — | One of `nova`, `placement`, `manila`, `identity`, `limes`, `cinder`. |
| `nova` | object | — | `{ type, deletedServersChangesSinceMinutes }`. `type` ∈ `servers`, `deletedServers`, `hypervisors`, `flavors`, `migrations`, `aggregates`. |
| `placement` | object | — | `{ type }` ∈ `resourceProviders`, `resourceProviderInventoryUsages`, `resourceProviderTraits`. |
| `manila` | object | — | `{ type }` ∈ `storagePools`. |
| `identity` | object | — | `{ type }` ∈ `projects`, `domains`. |
| `limes` | object | — | `{ type }` ∈ `projectCommitments`. |
| `cinder` | object | — | `{ type }` ∈ `storagePools`. |
| `syncInterval` | duration | `600s` | How often to sync. |
| `secretRef` | SecretReference | — | Keystone credentials secret; keys `availability`, `url`, `username`, `password`, `userDomainName`, `projectName`, `projectDomainName`. |

## Status

| Field | Type | Description |
|---|---|---|
| `lastSynced` | time | Last successful sync. |
| `numberOfObjects` | int64 | Objects currently stored for this datasource. |
| `nextSyncTime` | time | Planned next sync. |
| `conditions` | []Condition | Includes `Ready`. |

> [!NOTE]
> A datasource may depend on another; while a dependency is unavailable the controller reports
> the "waiting for dependency datasource to become available" error and retries.

## Next steps

- Concept: [The knowledge flow](../../concepts/overview.md)
- Guide: [Configure datasources, knowledge, and KPIs](../../guides/configure-knowledge.md)
