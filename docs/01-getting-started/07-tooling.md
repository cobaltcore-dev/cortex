<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Developer and operator tooling

The `tools/` directory holds standalone developer and operator utilities — not shipped in the Helm
charts, run by hand during development, debugging, or dashboard work. Each is a small Go program you
run with `go run` from the repository root (except the two dashboard directories, which hold static
definitions). This page documents each one in turn, with an illustrative slice of its output where the
tool prints a report. The example outputs are hand-authored to show the shape of what each tool prints;
they are not captured from a live run.

## `tools/spawner` — synthetic workload generator

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

## `tools/resdiff` — structural diff of two resources

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

Illustrative output — equal fields are shown dimmed, differing leaves show each item's value, and a
key present in only one item is called out (colors omitted here):

```
spec:
  schedulingDomain: nova
  flavorGroupName: hana-group
  amount:
    a: 4
    b: 6
  availabilityZone: only in a: az-1
status:
  ready: True
```

## `tools/mirror` — live one-way resource replicator

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

## `tools/visualize-committed-resources` — committed-resource report

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

The `summary` view prints counts by commitment state and Ready condition; `commitments` lists each
`CommittedResource` with its slots. Illustrative (colors omitted):

```
────────────────────────────────────────────────────────────────────────────────
▶ Summary
  CommittedResources : 3 total
    Confirmed:     2
    Pending:       1

  Ready conditions   : 2 accepted, 1 reserving, 0 rejected

  Reservation slots  : 5 total — 4 ready, 0 not-ready, 1 pending
```

## `tools/visualize-reservations` — reservation audit against Nova

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

Illustrative `summary` output — reservation slots cross-referenced with the VMs actually on the
hypervisors, plus the Postgres connection status (colors and emoji omitted):

```
==============================================
  Summary Statistics
==============================================
Hypervisor context:   (current context)
Reservation context:  (current context)
Postgres context:     (current context)

Database: connected (servers: 148, flavors: 32)

Total Hypervisors: 6
Total VMs (from hypervisors): 148
Total Failover Reservations: 9
Total All Reservations: 12
```

## `tools/logs` — scheduler log parser

Reads Nova external-scheduler logs from **stdin** (no flags) and prints a colorized per-request breakdown:
request id, flavor, the inferred pipeline, the per-filter and per-weigher host sets with weights, and the
final ordered output. It is the fastest way to see *why* a pipeline ordered hosts the way it did.

```
kubectl logs deploy/cortex-nova-scheduling-controller-manager -f | go run tools/logs/parser.go
```

Illustrative per-request breakdown — the input host set, each filter (with surviving host count) and
weigher (with per-host weights) in pipeline order, then the final ordering (colors omitted):

```
========================================
New Nova request with id: req-8f2c1a
========================================
Flavor           : m1.large
Inferred Pipeline : nova-default
Input hosts      : host-a, host-b, host-c
Filter filter_has_enough_capacity : host-a, host-b (2 hosts)
Weigher kvm_binpack : host-a: 0.9200, host-b: 0.4100
Output of pipeline : host-a, host-b
Final output     : host-a, host-b
```

## `tools/perses` and `tools/plutono` — dashboard definitions

These two are **not** runnable Go programs. `tools/perses` holds Perses dashboard definitions (JSON) and
`tools/plutono` is a Grafana-fork container image used to render them. They are covered in
[The infrastructure dashboard](../04-knowledge-database/05-infrastructure-dashboard.md).

These utilities sit beside Cortex rather than inside it: none is shipped in the Helm charts, and each
reads the same CRDs, logs, and [Postgres](06-postgres.md) datastore the rest of the book describes —
they are windows onto a running system, useful once you have one up. The next page gets you there,
running Cortex locally with Tilt.

## Next

[Prev: Postgres](06-postgres.md) · [Next: Local development with Tilt »](08-local-development-with-tilt.md)
