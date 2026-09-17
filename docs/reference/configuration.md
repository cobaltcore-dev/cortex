<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Configuration

Cortex is configured through two JSON files mounted into the pod, plus CRDs. This page documents
the file-based configuration surface. For CRD-authored configuration see the [CRD
reference](crds/readme.md); for command-line flags see [CLI flags](cli-flags.md).

## How configuration is loaded

At runtime, `pkg/conf` reads two fixed paths and merges them, with secrets overriding the base:

1. `/etc/config/conf.json` — base config (from a ConfigMap).
2. `/etc/secrets/secrets.json` — secrets (from a Kubernetes Secret).

The merge is recursive; **secret values win** over base values. Both files are re-read on every
config load, so the shim's self-heal rebuilds pick up current config. The paths are not
configurable via env var. Under Helm these files are populated from `global.conf` and the secrets
values (see [chart values](#helm-values)).

> [!NOTE]
> Configuration is decentralized: each component loads its own typed struct from the merged
> config, so top-level keys are additive across components. The keys below are the ones a
> deployment sets.

## Manager top-level keys

Consumed by the manager's main config:

| Key | Type | Description |
|---|---|---|
| `leaderElectionID` | string | Leader-election lock name. |
| `enabledControllers` | []string | Which controllers to run (see below). |
| `enabledTasks` | []string | Which periodic tasks to run (see below). |
| `userAgent` | object | `{ component, version }` for outbound OpenStack calls. |

### `enabledControllers` values

| Value | Runs |
|---|---|
| `datasource-controllers` | OpenStack and Prometheus datasource ingestion. |
| `knowledge-controllers` | Knowledge extraction and triggers. |
| `kpis-controller` | KPI computation. |
| `nova-pipeline-controllers` | Nova filter-weigher and detector pipelines, external scheduler API, deschedulings cleanup, pipeline webhook. |
| `manila-decisions-pipeline-controller` | Manila pipeline, API, webhook. |
| `cinder-decisions-pipeline-controller` | Cinder pipeline, API, webhook. |
| `ironcore-decisions-pipeline-controller` | IronCore (machines) pipeline and webhook. |
| `pods-decisions-pipeline-controller` | Pods pipeline and webhook. |
| `nova-deschedulings-executor` | Executes Nova descheduling recommendations. |
| `hypervisor-overcommit-controller` | Reconciles hypervisor overcommit ratios. |
| `committed-resource-reservations-controller` | Committed-resource reservation controllers, commitments LIQUID API, usage reconciler, host oversubscription (when enabled). |
| `inflight-reservation-controller` | In-flight reservation controller. |
| `failover-reservations-controller` | Failover reservation controller (needs a `datasourceName`). |
| `capacity-controller` | FlavorGroupCapacity controller (needs a scheduler URL). |
| `quota-controller` | ProjectQuota controller. |

### `enabledTasks` values

| Value | Interval |
|---|---|
| `commitments-sync-task` | `committedResourceSyncInterval`. |
| `nova-history-cleanup-task` | 1h. |
| `manila-history-cleanup-task` | 1h. |
| `cinder-history-cleanup-task` | 1h. |

## Feature toggles

Cortex has two independent toggle sets.

**Nova scheduler** (`featureGates` key):

| Key | Type | Default | Effect |
|---|---|---|---|
| `committedResourceTracking` | bool | `false` | Track committed-resource usage during Nova scheduling. |

The Nova external scheduler API also reads `forcedDestinationEnabled` (default `true`),
`evacuationShuffleK`, and `novaLimitHostsToRequest`. See the [delegation
model](../concepts/delegation-model.md).

**Placement API shim** (`features` key) — each toggle switches an endpoint group from passthrough
to the KVM backend. All default `false` (pure passthrough):

`resourceProviders`, `root`, `traits`, `resourceProviderTraits`, `resourceClasses`, `inventories`,
`aggregates`, `allocations`, `usages`, `allocationCandidates`, `reshaper`.

> [!NOTE]
> The KVM-backend path is not yet implemented; a toggle set to `true` currently returns `501 Not
> Implemented`. See [Placement API shim](../concepts/placement-api-shim.md).

## Placement shim config

Loaded by the shim (`internal/shim/placement`):

| Key | Type | Description |
|---|---|---|
| `placementURL` | string | **Required.** Upstream Placement API base URL. |
| `keystoneURL` | string | Keystone URL; required when `auth` is set. |
| `osUsername` / `osPassword` / `osProjectName` / `osUserDomainName` / `osProjectDomainName` | string | Service credentials for Keystone. |
| `sso` | object | Optional `{ cert, certKey, selfSigned }` for the upstream SSO transport. |
| `auth` | object | `{ tokenCacheTTL, policies[] }`. When absent, auth is disabled (all requests pass through). |
| `maxBodyLogSize` | quantity | Max request body logged. Default `4Ki`. |
| `features` | object | Endpoint toggles (above). |

Auth policies: each `{ pattern: "METHOD /path", roles: [{ name, projectScope }] }`. First match
wins; no match denies (403). Empty `roles` means public. Otherwise the `X-Auth-Token` header is
introspected against Keystone and the caller's roles (and optional project scope) are checked.

## Secrets shape

Secrets are supplied either in `secrets.json` or via `SecretReference` fields on CRDs.

**Keystone / OpenStack credentials** (secret keys): `url`, `availability` (`public`/`internal`/
`admin`), `username`, `password`, `projectName`, `userDomainName`, `projectDomainName`. Components
reference this secret via a `keystoneSecretRef` config field.

**Database credentials** live on the [Datasource](crds/datasource.md) CRD's `databaseSecretRef`
(keys `username`, `password`, `host`, `port`, `database`), not in `conf.json`. Controllers that
need Postgres (failover, quota, commitments) reference a Datasource by `datasourceName`.

**Prometheus** datasource secret: key `url`. **SSO** secret: keys `cert`, `key`.

## Multicluster (`apiservers` key)

| Key | Type | Description |
|---|---|---|
| `apiservers.home.gvks` | []string | GVKs served by the home cluster, as `group/version/Kind`. |
| `apiservers.remotes[]` | object | `{ host, caCert, insecureSkipTLSVerify, gvks[], labels{} }` per remote. |

See [Set up multicluster](../guides/set-up-multicluster.md).

## Pending-cache overlay (`cache` key)

| Key | Type | Default | Description |
|---|---|---|---|
| `cache.enabled` | bool | `false` | Enable the write-through overlay. |
| `cache.gvks` | []string | — | GVKs to overlay. |
| `cache.ttl` | duration | `2m` | Backstop eviction TTL. |

See [Pending-cache overlay](../concepts/pending-cache-overlay.md).

## Monitoring (`monitoring` key)

| Key | Type | Description |
|---|---|---|
| `monitoring.labels` | map[string]string | Extra labels added to Cortex metrics. |
| `monitoring.port` | int | Metrics port. |

## Helm values

Charts render `conf.json`/`secrets.json` from Helm values. The most-set keys per chart are in the
[per-chart values reference](helm-values.md).

## Next steps

- Reference: [CLI flags](cli-flags.md), [Helm values](helm-values.md), [Metrics and alerts](metrics.md)
- Guide: [Configure datasources, knowledge, and KPIs](../guides/configure-knowledge.md)
