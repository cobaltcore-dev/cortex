<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Datasources

A `Datasource` is the first stage of the knowledge flow: it declares where raw facts come from and caches
them into Postgres so the rest of Cortex can work from a local, query-fast copy rather than hammering the
source. This page covers the two source families — OpenStack and Prometheus — the sync model, and how a
datasource is authored.

## Caching the world into Postgres

Cortex does not query OpenStack or Prometheus during a placement request; that would put a slow, external
dependency on the hot path. Instead, each `Datasource` periodically pulls its source and writes the results
into Postgres (via `internal/knowledge/db`), and everything downstream reads from there. The `Datasource`
CRD sets a `syncInterval` controlling how often the pull runs.

There are two source families, dispatched by the datasource `type`:

- **OpenStack** — pulls objects from an OpenStack service through Keystone-authenticated calls (using
  [`pkg/keystone`](../06-cortex-library/05-keystone.md)). The `openstack.type` selects the
  service (Nova, Cinder, Manila, Placement …) and a sub-type selects the object kind (for example Nova
  `servers` or `hypervisors`).
- **Prometheus** — pulls metric series from a Prometheus endpoint, caching time-series-derived facts.

## Authoring a datasource

A minimal Nova-servers datasource:

```yaml
apiVersion: cortex.cloud/v1alpha1
kind: Datasource
metadata:
  name: nova-servers
spec:
  schedulingDomain: nova
  databaseSecretRef:
    name: cortex-nova-postgres
    namespace: cortex
  type: openstack
  openstack:
    syncInterval: 600s
    secretRef:
      name: cortex-nova-openstack-keystone
      namespace: cortex
    type: nova
    nova:
      type: servers
```

Enable the `datasource-controllers` in `enabledControllers`, and supply the source credentials as a Secret.
Verify it becomes Ready and reports synced objects:

```bash
kubectl get datasource nova-servers -o jsonpath='{.status.conditions}'
```

You should also see the sync metrics climb:

```
cortex_sync_objects{...} > 0
cortex_sync_request_processed_total{...} increasing
```

Every field is defined on the `Datasource` type in `api/v1alpha1/datasource_types.go`.

> [!NOTE]
> If a datasource never becomes Ready, check the referenced Secret and — for OpenStack — that the Keystone
> credentials in `secrets.json` are correct. `cortex_sync_request_processed_total` staying flat means the
> pull is failing. The credentials are supplied through the `secrets.json` overlay loaded via
> [the `conf` package](../06-cortex-library/04-conf.md).

## Inspecting datasources

`Datasource` is cluster-scoped, so no namespace is needed. List what is deployed and see sync state at a
glance:

```bash
kubectl get datasources
```

```
NAME           TYPE        DOMAIN   CREATED   SYNCED   NEXT   OBJECTS   READY
nova-servers   openstack   nova     3d        30s      570s   1240      True
```

The columns come straight from the resource's status: `Synced` is the last successful pull
(`.status.lastSynced`), `Next` the time until the next one, and `Objects` the number of objects cached on
the last sync (`.status.numberOfObjects`). A `Ready` of `True` with `Objects` climbing over time is a
healthy datasource. Read the full status — including the `conditions` that explain a `False` — with:

```bash
kubectl get datasource nova-servers -o yaml
```

Unlike `Pipeline`, a `Datasource` is not admission-webhook-validated; a misconfigured source surfaces as a
non-`Ready` condition and flat sync metrics rather than a rejected apply.

## Adding a new datasource kind

Datasources are dispatched by a typed switch/map on kind. Adding a new source kind means adding it to that
dispatch and implementing its ingestion, then referencing it from a `Datasource` — the process in
[Extend Cortex](../02-external-scheduler-api/07-extending-cortex.md).

## How this relates to Cortex

Datasources are the intake of the [knowledge flow](01-knowledge-flow-overview.md): they populate the
Postgres rows that [feature extraction](03-feature-extraction.md) reads, which in turn feed every pipeline
and reservation. They are also what the failover controller points its `datasourceName` at
([Failover reservations](../03-reservations-and-inventory/03-failover-reservations.md)). The next page turns
those raw rows into features.

## Next

[Prev: The knowledge flow](01-knowledge-flow-overview.md) · [Next: Feature extraction »](03-feature-extraction.md)
