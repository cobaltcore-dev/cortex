<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Pod gang-scheduling for inference workloads

With pods, Cortex breaks from the HTTP pattern of the OpenStack domains. There is no external scheduler
call; instead Cortex acts as a **Kubernetes scheduler**, watching for pods that name it and binding each
to a node. The motivating use case is placement for inference and other batch-like workloads — hence
"gang-scheduling" in the name — though in the current code the mechanism is per-pod binding built on the
same filter/weigher engine. This page explains the controller, how a pod opts in, and the steps it ships.

## Controller-driven, not HTTP

The pod scheduler is a controller (`cortex-pod-scheduler`, in
`internal/scheduling/pods/filter_weigher_pipeline_controller.go`). It watches three resources:

- **Pods** — but only those that opt in: a pod is considered when its `spec.schedulerName` is `pods`
  *and* it has no `nodeName` yet (i.e. it is unscheduled and asking for Cortex).
- **`Pipeline`** and **`Decision`** — so pipeline changes and decision state are reconciled like any other
  Cortex resource.

When an eligible pod appears, the controller builds a request from the pod plus the cluster's nodes, runs
the `pods-scheduler` pipeline, creates a `Decision` (generated name `pod-…`, resource id
`<namespace>--<name>`), and then **binds** the pod to the chosen node by creating a `corev1.Binding`. That
binding is what actually places the pod — Cortex is the scheduler, so it commits the placement rather than
advising another one.

> [!NOTE]
> This is the one domain where Cortex *does* place the workload directly, because in Kubernetes the
> scheduler's job is to bind. The "advise, don't replace" principle still holds for the OpenStack domains,
> where Cortex only reorders candidates; here Cortex *is* the platform scheduler for the pods that select
> it.

## The steps it ships

Registered under `internal/scheduling/pods/plugins/`:

**Filters** (remove unsuitable nodes):

| Filter | Removes nodes that… |
|---|---|
| `nodeaffinity` | do not satisfy the pod's node affinity |
| `nodeavailable` | are not available/schedulable |
| `nodecapacity` | lack capacity for the pod's requests |
| `taint` | carry a taint the pod does not tolerate |
| `noop` | (pass-through; kept for pipelines that need a no-op step) |

**Weigher:**

- `binpack` — pack pods onto fuller nodes to consolidate utilization.

So a default pod pipeline filters nodes down to the feasible set, then binpacks — the pod lands on the
most-utilized node that still fits.

## How this relates to Cortex

The pod scheduler reuses the exact `lib` filter/weigher engine of
[The scheduling engine](01-the-scheduling-engine.md), producing the same `Decision`/`History` records as
every other domain — only the trigger (a watch on pods, not an HTTP call) and the final action (a
`Binding`, not a returned ordering) differ. IronCore machine scheduling, next, is the other watch-driven
domain, and it commits its choice by setting a field on a Machine rather than binding a pod.

## Next

[Prev: Cinder smart scheduling](04-cinder-smart-scheduling.md) · [Next: IronCore machine scheduling »](06-ironcore-machine-scheduling.md)
