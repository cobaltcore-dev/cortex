<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Valkey

Cortex caches everything its datasources ingest in [Valkey](https://valkey.io), an in-memory key-value
store that each domain bundle deploys next to its managers. This page explains why a cache is all Cortex
needs, how datasource objects are laid out in Valkey, what happens when Valkey restarts, and how the
bundles configure it.

## A cache, not a system of record

Everything in Valkey is re-derivable: datasources pull it from OpenStack and Prometheus on their sync
interval, and the features extracted from it live in the `status` of each `Knowledge` resource, not in
Valkey. Cortex therefore runs Valkey without persistence. When the store is empty, the datasources simply
sync again — the restart is disruptive, not destructive.

The rest of Cortex never talks to Valkey directly. Syncers, extractors, KPIs and the scheduling code go
through the storage abstraction in `pkg/knowledge/store`, which only knows three operations per
datasource: list all objects, replace all objects, and check whether objects exist. The backend is chosen
by a datasource's `spec.storage.type`; `valkey` (`pkg/knowledge/store/valkey`) is the only one, and
`pkg/knowledge/store/memory` provides the same semantics in process for tests.

## How objects are stored

Each datasource owns one *scope*: the table name of its Go model, plus the metric alias for Prometheus
datasources, which share one model per metric type. A scope is stored as one hash per *generation* and a
pointer to the current one:

```
cortex/{openstack_servers_v4}/current          "<generation>/<number of objects>"
cortex/{openstack_servers_v4}/gen/<generation>  <server uuid> -> <server json>
```

Every object is its own small hash field. The field is the object's primary key — the fields tagged
`db:"…,primarykey"` on the model, for example the server UUID — and the value is the object as compact
JSON keyed by its `db` column names. Composite keys are encoded as a JSON array; models without a primary
key, such as Prometheus metrics, are keyed by a hash of their content. The `{…}` hash tag keeps all keys of
a scope in the same slot.

A sync writes the new objects into a fresh generation and then switches `current` to it in one atomic
step, so readers never see a half-written scope. The previous generation stays readable for five minutes
to let in-flight reads finish, and then expires. Single objects stay addressable, which is handy for
debugging:

```bash
kubectl exec deploy/cortex-nova-valkey -- valkey-cli -a <password> --no-auth-warning \
  GET 'cortex/{openstack_servers_v4}/current'
kubectl exec deploy/cortex-nova-valkey -- valkey-cli -a <password> --no-auth-warning \
  HGET 'cortex/{openstack_servers_v4}/gen/<generation>' <server uuid>
```

Each manager also keeps the decoded objects of every scope it reads in memory, and reuses them for as long
as `current` points to the same generation. Generations are random identifiers, so a restarted Valkey
never matches a stale in-memory copy.

## Restarts and resync

When Valkey restarts, every scope is gone. Readers then get "no objects stored for this scope yet":
knowledges report the `InputFetchFailed` reason until their inputs are back. The datasource controllers
check every minute whether the objects of each datasource are still stored, and sync a datasource right
away if they are not — regardless of the `.status.nextSyncTime` it reported before. A full resync therefore
follows a Valkey restart within about a minute, at the cost of one burst of requests against OpenStack and
Prometheus.

## Configuring the bundles

The `cortex-nova`, `cortex-manila` and `cortex-cinder` bundles depend on the upstream
[valkey chart](https://valkey.io/valkey-helm/) and render a Secret `cortex-<bundle>-valkey` from the
`store` values. The datasources reference that Secret in `spec.storage`, and the valkey chart reads the
password of its `default` user from it:

```yaml
store:
  host: cortex-nova-valkey
  port: "6379"
  password: secret

valkey:
  fullnameOverride: cortex-nova-valkey
  auth:
    enabled: true
    usersExistingSecret: cortex-nova-valkey
    aclUsers:
      default:
        permissions: "~* &* +@all"
        passwordKey: password
  persistence:
    enabled: false
```

Besides `host`, `port` and `password`, the Secret may carry `username` for a dedicated ACL user and `db`
for a database number other than `0`. The managers expose `cortex_valkey_operation_duration_seconds`,
`cortex_valkey_objects_total`, `cortex_valkey_list_cache_total` and
`cortex_valkey_connection_attempts_total`.

Valkey is the hinge in the [end-to-end flow](01-what-is-cortex.md): datasources write raw facts into it,
and extractors and KPIs read those facts back as typed lists. Because every object in it can be fetched
again, a cache without persistence is enough, and a clean restart is a supported recovery path. The next
page tours the developer and operator utilities under `tools/`.

## Next

[Prev: Make targets](05-make-targets.md) · [Next: Developer and operator tooling »](07-tooling.md)
