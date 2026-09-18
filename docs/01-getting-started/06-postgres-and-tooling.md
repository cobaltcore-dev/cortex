<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Postgres and tooling

Cortex stores everything it ingests and everything it derives in Postgres — the `cortex-postgres`
component introduced in [What is Cortex?](01-what-is-cortex.md). This page explains why Cortex ships
its *own* Postgres image, how the schema is created without a migration tool, and how to safely change
storage-affecting settings. It closes with a tour of the developer utilities under `tools/`, which you
will meet again in later chapters.

## Why a custom Postgres image

Rather than depend on an off-the-shelf Postgres, Cortex builds its own image from `postgres/Dockerfile`
so it can pin the major version and the extensions Cortex needs. The current major is **Postgres 18**
(`PG_MAJOR` in the Dockerfile). Building the image in-repo also lets CI keep it patched: the
`rebuild-postgres.yaml` workflow (see [CI/CD and packaging](03-cicd-and-packaging.md)) rebuilds it
daily and opens a PR when doing so lowers the CVE count.

The image is deployed as a **StatefulSet** by the `cortex-postgres` library chart
(`helm/library/cortex-postgres/templates/statefulset.yaml`), so the database gets stable network
identity and a persistent volume.

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
the datasources re-ingest and the extractors re-run. It is disruptive, not destructive.

## The `tools/` utilities

The `tools/` directory holds standalone developer and operator utilities — not shipped in the Helm
charts, run by hand during development, debugging, or dashboard work. Each is a small Go program you
run with `go run` from the repository root (except the two dashboard directories, which hold static
definitions). The rest of this page documents each one in turn.

### `tools/spawner` — synthetic workload generator

Boots a fleet of real VMs that immediately put themselves under CPU and RAM load, so you can exercise
a scheduler against genuine, moving inventory rather than empty hosts.

```
go run tools/spawner/main.go
```

It takes **no flags** — the whole run is interactive and prompt-driven. It authenticates to OpenStack
as a Keystone admin, reading its scope from the standard `OS_*` environment variables:

| Variable | Meaning |
|---|---|
| `OS_AUTH_URL` | Keystone endpoint. |
| `OS_USERNAME`, `OS_USER_DOMAIN_NAME` | Admin user and its domain. |
| `OS_PROJECT_NAME`, `OS_PROJECT_DOMAIN_NAME` | Target project and its domain. |
| `OS_REGION_NAME` | Region to spawn in. |
| `OS_PASSWORD` *or* `OS_PW_CMD` | Password directly, or a command that prints it. |
| `OS_PREFIX` (optional) | Name prefix for spawned resources (default `cortex-workload-spawner`). |

At each run it prompts for the VM count, AZ/host, flavor, image, and server-group policy. Your answers
are remembered as `WS_*` keys in `tools/spawner/defaults.json` (these are JSON keys used to prefill the
next prompt — **not** environment variables). The spawner first deletes any resources it previously
created under the same prefix, then boots N volume-backed VMs that each run a `stress-ng` CPU+RAM load
rendered from `script.sh.tpl`, and writes the generated SSH private key to `tools/spawner/ssh.pem`.

```
# Load an OpenStack rc file, then run interactively.
source ~/my-openstack.rc
go run tools/spawner/main.go
```

### `tools/resdiff` — structural diff of two resources

Reads a Kubernetes **List** (YAML, e.g. `kubectl get … -o yaml`) from **stdin** and prints a colorized,
recursive structural diff between items — useful for spotting where an expected resource state diverges
from the actual one.

```
kubectl get committedresources -o yaml | go run tools/resdiff/main.go -diff a,b
```

| Flag | Meaning |
|---|---|
| `-diff` | Comma-separated resource names to compare; empty compares all items. |
| `-no-color` | Disable ANSI coloring (for piping/logging). |

The input list must contain at least two items.

### `tools/mirror` — live one-way resource replicator

Replicates custom resources from one cluster into another: an initial sync followed by a watch, mirroring
creates, updates (including status), and deletes, while stripping server-managed metadata. Handy for
reproducing a live environment locally. The target CRDs must already exist. It runs until interrupted
(SIGINT).

```
go run tools/mirror/mirror.go \
  --source-kubeconfig ~/.kube/prod.yaml \
  --target-kubeconfig ~/.kube/local.yaml \
  --gvr committedresources.cortex.cloud/v1alpha1
```

| Flag | Required | Meaning |
|---|---|---|
| `--source-kubeconfig`, `--target-kubeconfig` | yes | Kubeconfig for each side. |
| `--gvr` | yes | `resource.group/version` to mirror (comma-separated for several). |
| `--source-context`, `--target-context` | no | Context to select within each kubeconfig. |
| `--namespace` | no | Restrict to one namespace. |

### `tools/visualize-committed-resources` — committed-resource report

Reads `CommittedResource` CRs and their child `Reservation` slots from one or more clusters and prints a
colorized report. Pairs with [Chapter 3](../03-reservations-and-inventory/readme.md).

```
go run tools/visualize-committed-resources/main.go --views summary,commitments --watch 5s
```

| Flag | Meaning |
|---|---|
| `--contexts` | Comma-separated kube contexts; empty uses the current one. Suffix a name with `@ctx` to disambiguate across clusters. |
| `--filter-project`, `--filter-az`, `--filter-group`, `--filter-state` | Narrow the rows shown. |
| `--active` | Show only active entries. |
| `--views` | Which sections to render: `summary`, `commitments`, `reservations`, `allocations`, `usage`. |
| `--hide` | Sections to suppress. |
| `--watch <dur>` | Redraw on change every interval; `0` renders once and exits. |
| `--limit` | Max rows (default 200). |

### `tools/visualize-reservations` — reservation audit against Nova

Audits failover/committed `Reservation`s against the VMs actually present on hypervisors, cross-referenced
with Nova's Postgres. It renders once and exits. Postgres features degrade gracefully if the database is
unreachable — you still get the cluster-side view.

```
go run tools/visualize-reservations/main.go --sort res-host --views summary
```

| Flag | Meaning |
|---|---|
| `--config` | Path to a JSON config file. |
| `--sort` | Row order: `vm`, `vm-host`, or `res-host`. |
| `--postgres-secret` | Secret holding Nova Postgres credentials (default `cortex-nova-postgres`). |
| `--namespace` | Namespace of that secret. |
| `--postgres-host`, `--postgres-port` | Override the DB endpoint (e.g. when port-forwarding). |
| `--views`, `--hide` | Select/suppress report sections. |
| `--filter-name`, `--filter-trait` | Narrow the rows shown. |
| `--hypervisor-context(s)`, `--reservation-context(s)`, `--postgres-context` | Kube contexts for each data source. |
| `--postgres-port-forward`, `--postgres-port-forward-service`, `--postgres-port-forward-local-port`, `--postgres-port-forward-remote-port` | Set up a port-forward to reach Postgres. |

### `tools/logs` — scheduler log parser

Reads Nova external-scheduler logs from **stdin** (no flags) and prints a colorized per-request breakdown:
request id, flavor, the inferred pipeline, the per-filter and per-weigher host sets with weights, and the
final ordered output. It is the fastest way to see *why* a pipeline ordered hosts the way it did.

```
kubectl logs deploy/cortex-nova-scheduling-controller-manager -f | go run tools/logs/parser.go
```

### `tools/perses` and `tools/plutono` — dashboard definitions

These two are **not** runnable Go programs. `tools/perses` holds Perses dashboard definitions (JSON) and
`tools/plutono` is a Grafana-fork container image used to render them. They are covered in
[The infrastructure dashboard](../04-knowledge-database/05-infrastructure-dashboard.md).

Postgres is the hinge in the [end-to-end flow](01-what-is-cortex.md): datasources write raw facts into
it, extractors read those facts and write features back, and KPIs and pipelines read the features. The
custom image and the code-owned schema keep that hinge versioned with the rest of the source tree —
which is why there is no migration tool to coordinate and why a clean restart is a supported recovery
path.

## Next

[Prev: Make targets](05-make-targets.md) · [Next: Local development with Tilt »](07-local-development-with-tilt.md)
