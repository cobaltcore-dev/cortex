<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Operate committed-resource and failover reservations

This guide covers running Cortex's reservation machinery: committed-resource reservations (from
Limes commitments), failover reservations, and the capacity/quota controllers that support them.
Reservations model capacity that is promised but not yet consumed, so the scheduler does not hand it
to someone else.

## Before you begin

- A domain bundle (typically `cortex-nova`) installed with a database — see
  [Install a domain bundle](install-a-domain-bundle.md).
- The relevant controllers enabled in `enabledControllers` (see
  [Configuration](../reference/configuration.md)):
  - `committed-resource-reservations-controller`
  - `inflight-reservation-controller`
  - `failover-reservations-controller` (needs a `datasourceName`)
  - `capacity-controller` (needs a scheduler URL)
  - `quota-controller`
- For committed resources, the `commitments-sync-task` in `enabledTasks`.

## Committed-resource reservations

Cortex syncs commitments from Limes into `CommittedResource` CRDs and reserves capacity for them so
scheduling honours the commitment.

### How a commitment becomes a reservation

Not every committed resource behaves the same way, and the difference is worth understanding before
you read the CRDs:

- **Memory commitments** create an actual reservation *slot* on a host. Because a slot pins memory,
  and CPU on that host is sized to the memory it accompanies, a memory slot carries an implicit
  guarantee of the CPU that goes with it — it reserves a place a workload can land.
- **CPU-only commitments** (where a flavor group is billed for cores separately) do not create a
  slot. They are checked arithmetically against available headroom — enough cores exist to honour the
  commitment — and drive billing, but they do not hold a specific place on a specific host.

This is also why a commitment's *billing* view and its *scheduling* view can drift apart over time:
billing counts what has been committed and confirmed, while scheduling counts the slots actually held
on hosts right now. The two are reconciled continuously rather than being the same number by
construction.

> [!NOTE]
> Cortex creates and tracks these reservations so the scheduler treats reserved space as unavailable.
> Hard *enforcement* of committed resources — refusing placements that would eat into another
> tenant's committed slot — is a planned direction; treat today's behaviour as reserving and
> reporting, not blocking.

### Verify the sync

```bash
kubectl get committedresources
```

Expected — one row per synced commitment, each reporting `Ready` (columns abridged):

```
NAME              PROJECT     FLAVORGROUP   RESOURCETYPE   AZ     AMOUNT   STATE      READY
hana-v2-az-a-01   proj-1234   hana-v2       instances      az-a   4        confirmed  True
```

Confirm the syncer is running and commitments are covered:

```
cortex_committed_resource_syncer_* increasing
cortex_committed_resource_unfulfilled == 0
```

`cortex_committed_resource_unfulfilled > 0` means some commitment lacks full reservation coverage —
capacity is short or reservations failed. See the [CommittedResource reference](../reference/crds/committed-resource.md).

### Watch for oversubscription

If reservations exceed a host's capacity, `cortex_committed_resource_host_oversubscribed` goes
positive and the `CortexNovaHostReservationsOversubscribed` alert fires; the controller may evict
reservation slots (`cortex_committed_resource_host_oversubscribed_evicted_reservations_total`).
Investigate capacity before commitments are lost.

## Capacity and quota

- The `capacity-controller` maintains `FlavorGroupCapacity` CRDs — pre-computed capacity per (flavor
  group × AZ) so the capacity API answers without probing the scheduler each time. See the
  [FlavorGroupCapacity reference](../reference/crds/flavor-group-capacity.md).
- The `quota-controller` maintains `ProjectQuota` CRDs from Limes' LIQUID quota endpoint, tracking
  total vs. pay-as-you-go usage. See the [ProjectQuota reference](../reference/crds/project-quota.md).

Verify:

```bash
kubectl get flavorgroupcapacities
kubectl get projectquotas
```

Expected — a `Ready` column of `True` and a recent reconcile timestamp on each (columns abridged):

```
# flavorgroupcapacities
NAME            GROUP     AZ     RUNNING   AVAIL   READY   RECONCILED
hana-v2-az-a    hana-v2   az-a   12        6       True    30s

# projectquotas
NAME            PROJECT     AZ     DOMAIN      READY   LASTRECONCILE
proj-1234-az-a  proj-1234   az-a   domain-42   True    30s
```

## Failover reservations

The `failover-reservations-controller` reserves headroom so workloads can be evacuated on host
failure. It needs a `datasourceName` pointing at a Datasource (for database access). Watch
`cortex_failover_*` for reconciliation health.

Two design choices shape how much headroom this actually holds. Failover headroom is reserved
*best-effort and after* the workloads it protects already exist — it does not block their initial
placement, it pre-clears somewhere for them to go later. And the reservation is *shared* across the
workloads it protects rather than duplicated per workload: reserving a full spare copy of every
workload would cost roughly `(m+1) × size` of capacity, so instead Cortex reserves only enough to
absorb `m` simultaneous host failures — a configurable tolerance — and lets the protected workloads
share that pool. Raising `m` buys resilience against more concurrent failures at the cost of more
idle reserved capacity.

> [!NOTE]
> Failover reservations are a planned/maturing capability; in the current scope the failure they plan
> around is host failure. Verify the controller is reserving what you expect before relying on it for
> capacity planning.

## In-flight reservations

The `inflight-reservation-controller` tracks reservations for placements that are decided but not
yet fully realized, preventing double-spend during the placement window.

The reasoning is pessimistic by necessity: many placement requests race for the same finite capacity
at once, so the moment Cortex recommends a host it must assume a parallel request could try to claim
the same slot. An in-flight reservation blocks that capacity for the duration of the window between
"decided" and "running", so a concurrent request is never offered space that is already spoken for.
Once the placement is realized (or abandoned) the block is released. This is the persistence layer
behind the concurrency reasoning described in the [delegation model](../concepts/delegation-model.md#concurrency-and-retries).

## Troubleshooting

- **`unfulfilled > 0`** — capacity shortfall or a failing reservation controller; check
  `cortex_reservations` and the controller logs.
- **Capacity/quota not reconciling** — confirm the controller is enabled and, for capacity, that its
  scheduler URL is set; for failover, that `datasourceName` resolves.
- **Oversubscription alert** — reduce commitments or add capacity; evictions are the last resort.

## Next steps

- Reference: [Reservation](../reference/crds/reservation.md), [CommittedResource](../reference/crds/committed-resource.md), [FlavorGroupCapacity](../reference/crds/flavor-group-capacity.md), [ProjectQuota](../reference/crds/project-quota.md)
- Reference: [Metrics and alerts](../reference/metrics.md), [Configuration](../reference/configuration.md)
