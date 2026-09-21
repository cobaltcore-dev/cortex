<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Committed-resource reservations and Limes quota/capacity

Committed resources are the richest kind of reservation, because they bridge two worlds: a *billing* view
(what a customer has committed to and been confirmed for, tracked in Limes) and a *scheduling* view (slots
actually held on hosts right now). This page explains how a Limes commitment becomes a Cortex reservation,
why memory and CPU commitments behave differently, and the capacity and quota controllers that support the
whole arrangement.

## From a Limes commitment to a reservation

Cortex syncs commitments from Limes into `CommittedResource` resources (the `commitments-sync-task`) and
reserves capacity for each so scheduling honours it. Not every committed resource behaves the same way,
and the difference follows from how its **flavor group** is defined.

### Flavor groups and their two shapes

A commitment is not made against a single flavor but against a **flavor group** — a set of flavors that
share commitment semantics, so a customer who committed to "large-memory capacity" can be served by any
flavor in that group. Groups come in two shapes, and the shape decides whether a commitment pins a place
on a host:

- **Fixed-ratio group (memory-only, slotted).** Every flavor in the group has the same memory-to-CPU
  ratio, so committed capacity can be expressed as whole *slots*. Cortex sizes a slot to the largest
  flavor in the group and reserves that slot on a host. Because the ratio is fixed, pinning the memory
  pins the CPU that goes with it — the slot is a real place a workload can land.
- **Variable-ratio group (memory-slotted + CPU-arithmetic).** Flavors in the group vary in how much CPU
  accompanies a unit of memory. Memory is still reserved as a slot, but the CPU side cannot be expressed
  as a fixed slot; it is tracked *arithmetically* against available headroom (enough cores exist to honour
  the commitment) and drives billing, without holding a specific place on a specific host.

That split is why memory and CPU commitments behave differently at scheduling time:

- **Memory commitments create an actual reservation *slot* on a host.** A memory slot carries an implicit
  guarantee of the CPU that accompanies it — it reserves a real place a workload can land.
- **CPU-only commitments do not create a slot.** They are checked arithmetically against headroom and
  drive billing, but do not hold a specific place on a specific host.

### Nova as the source of truth for group membership

Which flavors belong to which group is defined in the platform (a shared flavor extra-spec), not in a
Cortex custom resource, and Cortex reads it from there. That keeps a single source of truth — operators
define groups where they already define flavors — but it has honestly-acknowledged downsides: the grouping
is **not Kubernetes-native** (you cannot `kubectl get` it as a first-class Cortex object), it is picked up
by **polling**, so a group change lags until the next sync, and the entity that *defines* a group is not
the one that *maintains* the reservations derived from it. Cortex treats the platform's view as
authoritative and reconciles toward it rather than trying to own group definitions itself.

### Why billing and scheduling drift

This is also why the billing and scheduling views can drift apart: billing counts what has been committed
and confirmed, while scheduling counts the slots actually held on hosts right now. Cortex reconciles the
two continuously rather than assuming they are equal by construction.

When Nova places a VM against a committed slot, the `crs` recorder writes the VM into the matching
reservation slot (see
[Nova smart scheduling](../02-external-scheduler-api/02-nova-smart-scheduling.md)) — that is the moment a
reservation goes from "promised" to "consumed." A flavor that belongs to no flavor group is a
pay-as-you-go placement and is not slotted.

A `CommittedResource` moves through a small state machine as this happens — **planned** (Cortex intends to
reserve for it) → **pending** (reservation attempted, not yet fully covered) → **confirmed** (capacity is
held) — which is what the `STATE` column below reports.

> [!NOTE]
> As with all reservations, this is *reserving and reporting*, not hard enforcement — Cortex does not yet
> refuse a placement that would eat into another tenant's committed slot. See
> [Reservations overview](01-reservations-overview.md).

## Verifying the sync

```bash
kubectl get committedresources
```

Expected — one row per synced commitment, each reporting `Ready` (columns abridged):

```
NAME              PROJECT     FLAVORGROUP   RESOURCETYPE   AZ     AMOUNT   STATE      READY
mem-large-az-a-1  proj-1234   mem-large     instances      az-a   4        confirmed  True
```

Confirm the syncer is running and commitments are covered:

```
cortex_committed_resource_syncer_* increasing
cortex_committed_resource_unfulfilled == 0
```

`cortex_committed_resource_unfulfilled > 0` means some commitment lacks full reservation coverage —
capacity is short or reservations failed. See the `CommittedResource` type in
`api/v1alpha1/committed_resource_types.go`.

### Watch for oversubscription

If reservations exceed a host's capacity, `cortex_committed_resource_host_oversubscribed` goes positive and
the `CortexNovaHostReservationsOversubscribed` alert fires; the controller may evict reservation slots
(`cortex_committed_resource_host_oversubscribed_evicted_reservations_total`). Investigate capacity before
commitments are lost.

## The supporting controllers: capacity and quota

A commitment guarantees a customer *quota* — permission to consume so much — but quota alone cannot
guarantee the compute will be *there* when the customer asks for it. The two are different questions:
Limes answers "is this project allowed this much?" from a billing ledger, while scheduling has to answer
"is there a host that can actually take this workload right now?". Quota can be granted against capacity
that is only available *disjunctively* — a pool that can satisfy request A *or* request B but not both —
so a project can be fully within quota and still find nowhere to land. Cortex therefore keeps its own
capacity picture rather than trusting quota to imply it, and delegates cleanly:

- **Limes owns billing and quota** — it is the source of truth for what a project has committed to and is
  allowed to use.
- **Cortex owns capacity and enforcement** — it tracks the real slots and headroom and reserves against
  them so the promise is physically honourable.

Two controllers keep that split answerable without probing the scheduler on every request:

- **`capacity-controller`** maintains `FlavorGroupCapacity` resources — pre-computed capacity per
  (flavor group × AZ) — so the capacity API answers immediately. It needs a scheduler URL. See the
  `FlavorGroupCapacity` type in `api/v1alpha1/flavor_group_capacity_types.go`.
- **`quota-controller`** maintains `ProjectQuota` resources from Limes' **LIQUID** quota endpoint, tracking
  total versus pay-as-you-go usage. See the `ProjectQuota` type in `api/v1alpha1/project_quota_types.go`.

> [!NOTE]
> LIQUID is the Limes quota/usage interface Cortex reads. The quota controller consumes it over HTTP to
> keep `ProjectQuota` in sync with what Limes believes each project may use. Memory-slot commitments carry
> an implicit CPU guarantee (the slot pins both); CPU-arithmetic commitments are billing-only and are
> reconciled against headroom rather than pinned to a slot.

Verify:

```bash
kubectl get flavorgroupcapacities
kubectl get projectquotas
```

Expected — a `Ready` column of `True` and a recent reconcile timestamp on each (columns abridged):

```
# flavorgroupcapacities
NAME            GROUP       AZ     RUNNING   AVAIL   READY   RECONCILED
mem-large-az-a  mem-large   az-a   12        6       True    30s

# projectquotas
NAME            PROJECT     AZ     DOMAIN      READY   LASTRECONCILE
proj-1234-az-a  proj-1234   az-a   domain-42   True    30s
```

Committed resources tie the knowledge database, the reservations, and the Nova pipeline together:
`FlavorGroupCapacity` and `ProjectQuota` are derived from Limes and datasource facts
([Chapter 4](../04-knowledge-database/readme.md)), the reservations subtract from usable capacity
([Reservations overview](01-reservations-overview.md)), and Nova's `kvm_committed_resource_reservation`
weigher plus the `crs` recorder read and update them during scheduling. The next page covers a different
promise — not a customer commitment but spare capacity kept in reserve for failure.

## Next

[Prev: Reservations overview](01-reservations-overview.md) · [Next: Failover reservations for KVM HA »](03-failover-reservations.md)
