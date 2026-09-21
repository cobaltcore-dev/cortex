<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Postgres

Cortex stores everything it ingests and everything it derives in Postgres — the `cortex-postgres`
component introduced in [What is Cortex?](01-what-is-cortex.md). This page explains why Cortex ships
its *own* Postgres image, how the schema is created without a migration tool, and how to safely change
storage-affecting settings.

## Why a custom Postgres image

Rather than depend on an off-the-shelf Postgres, Cortex builds its own image from `postgres/Dockerfile`
so it can pin the major version and the extensions Cortex needs. The current major is **Postgres 18**
(`PG_MAJOR` in the Dockerfile). Building the image in-repo also lets CI keep it patched: the
`rebuild-postgres.yaml` workflow (see [CI/CD and packaging](03-cicd-and-packaging.md)) rebuilds it
daily and opens a PR when doing so lowers the CVE count.

The image is deployed as a **StatefulSet** by the `cortex-postgres` library chart
(`helm/library/cortex-postgres/templates/statefulset.yaml`), so the database gets stable network
identity and a persistent volume.

> [!NOTE]
> Cortex is tightly coupled to Postgres today, but this is a direction rather than a fixed commitment.
> The plan is to **abstract the storage layer** behind a dedicated kind/interface so the rest of Cortex
> no longer depends on Postgres specifics, and — for the knowledge that is derived and re-derivable
> rather than authoritative — to move toward **cache-driven backends such as Redis** in the future.
> The pages that follow describe the current Postgres-backed model; treat the storage engine as an
> implementation detail Cortex intends to make swappable, not a permanent part of the contract.

## No migration tool — Go creates the schema

There is deliberately **no SQL migration framework**. The schema is created by the Go code at startup:
`internal/knowledge/db` maps structs to tables through `gorp`, and a sync plugin calls
`DB.CreateTable` (backed by `DB.AddTable`) for the tables it owns. A datasource or extractor that needs
a table ensures it on startup; there is no separate `migrate` step to run.

> [!NOTE]
> Because the tables follow the Go structs, a schema change is a *code* change: edit the struct, and
> the table it maps to is (re)created by the owning plugin. Keep this in mind when reading
> [Datasources](../04-knowledge-database/02-datasources.md) and
> [Feature extraction](../04-knowledge-database/03-feature-extraction.md) — the "schema" is the set of
> ingested and derived row types those chapters describe.

## Rotating the instance with `instanceSuffix`

The Postgres resources are named `…-v<major>-<instanceSuffix>`, where `instanceSuffix` defaults to
`"g0"` (`helm/library/cortex-postgres/values.yaml`). Because the suffix is part of the StatefulSet
name and therefore its PersistentVolumeClaim, **changing it provisions a brand-new StatefulSet and a
brand-new empty volume** rather than reusing the old data:

```yaml
cortex-postgres:
  instanceSuffix: "g1"   # was "g0" → new StatefulSet + new PVC, old data left behind
```

> [!WARNING]
> Bumping `instanceSuffix` (g0 → g1 → g2 …) is the intended way to roll to a fresh database instance —
> for a major-version move or to abandon a corrupted volume. It does **not** migrate data; the old PVC
> is left in place untouched. Do it only when you mean to start clean, and clean up the orphaned PVC
> afterwards.

Since Cortex re-derives all knowledge from its datasources, starting a fresh instance is recoverable:
the datasources re-ingest and the extractors re-run. It is disruptive, not destructive. In effect the
Postgres database is a **cache**, not a system of record — when it is empty, Cortex simply syncs the
data in again from the datasources, so the instance can be replaced without losing any authoritative
state. That property is exactly why a caching backend such as Redis is being considered in place of
strong Postgres persistence (see the note under [Why a custom Postgres image](#why-a-custom-postgres-image)):
if the store holds only re-derivable data, a lighter cache serves the purpose without the persistence
guarantees a system of record would need.

Postgres is the hinge in the [end-to-end flow](01-what-is-cortex.md): datasources write raw facts into
it, extractors read those facts and write features back, and KPIs and pipelines read the features. The
custom image and the code-owned schema keep that hinge versioned with the rest of the source tree —
which is why there is no migration tool to coordinate and why a clean restart is a supported recovery
path. The next page tours the developer and operator utilities under `tools/`.

## Next

[Prev: Make targets](05-make-targets.md) · [Next: Developer and operator tooling »](07-tooling.md)
