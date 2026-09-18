<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# IronCore machine scheduling

IronCore models bare-metal compute as Kubernetes resources: a `Machine` is a server to be provisioned,
and a `MachinePool` is a pool it can be assigned to. Cortex schedules bare metal by watching for
unassigned `Machine`s and choosing a pool for each — the second watch-driven domain, alongside pods. This
page covers the controller, the gate that decides which machines Cortex handles, and how it commits its
choice.

## Controller-driven, watch on Machines

The scheduler is a controller (`cortex-machine-scheduler`, in
`internal/scheduling/machines/filter_weigher_pipeline_controller.go`). It watches IronCore `Machine`
resources and lists `MachinePool`s as the placement candidates. There is no HTTP API.

A machine is eligible when **both**:

- `spec.machinePoolRef == nil` — it has not been assigned a pool yet, and
- `spec.scheduler == ""` — no scheduler has claimed it.

> [!NOTE]
> The design intent is to schedule machines whose scheduler is `cortex`. The IronCore `Machine` spec does
> not yet carry that field upstream, so it deserializes to the empty string, and Cortex currently
> subscribes to *all* machines that have no scheduler and no pool. Treat the `scheduler == ""` gate as a
> stand-in for `scheduler == cortex` until the upstream field lands — this is current behaviour, not the
> final design.

## How it commits a decision

When an eligible machine appears, the controller builds a request from the machine plus the available
pools, runs the `machines-scheduler` pipeline, and creates a `Decision` (generated name `machine-…`,
resource id = the machine name). It then commits the choice by patching the machine:

```go
machine.Spec.MachinePoolRef = &corev1.LocalObjectReference{Name: *result.TargetHost}
```

Setting `MachinePoolRef` is what assigns the machine to a pool — IronCore takes over provisioning from
there. As with pods, Cortex commits the placement directly (there is no external scheduler to advise).

## The steps it ships

Registered under `internal/scheduling/machines/plugins/`:

- **Filters:** one — `noop`, a pass-through that assigns every pool an activation of `1.0`.
- **Weighers:** none.

With only a no-op filter and no weighers, machine scheduling is minimal today: it selects a pool from the
candidates without domain-specific scoring. Like Cinder, the integration is fully wired — the intelligence
is added later by implementing and registering steps
([Extend Cortex](07-extending-cortex.md)).

IronCore machine scheduling completes the chapter's pattern: five domains, one shared engine, differing
only in trigger and commit action. Nova, Manila, and Cinder answer HTTP calls and *advise*; pods and
machines watch Kubernetes resources and *commit* (a pod `Binding`, a machine `MachinePoolRef`). All five
produce `Decision` and `History` records through the same `lib` pipeline. With the scheduler API covered,
the next chapter turns to the capacity that a pipeline must respect: reservations and inventory.

## Next

[Prev: Pod gang-scheduling](05-pod-gang-scheduling.md) · [Next: Extend Cortex »](07-extending-cortex.md)
