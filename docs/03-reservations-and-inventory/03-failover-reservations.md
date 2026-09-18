<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Failover reservations for KVM HA

Where committed resources reserve space a customer paid for, **failover reservations** reserve space
*nobody is using yet* — headroom kept clear so that when a host fails, the workloads on it have somewhere
to be evacuated. This page explains the two design choices that make failover headroom affordable: it is
reserved best-effort after the fact, and it is shared rather than duplicated.

## The problem: room to evacuate

If a KVM host dies, its VMs must restart elsewhere. That only works if the rest of the fleet has enough
free room to absorb them. Leaving that room to chance means an evacuation can fail exactly when it matters
most. Failover reservations pre-clear the room so the evacuation has somewhere to go.

The `failover-reservations-controller` maintains this headroom. It needs a `datasourceName` pointing at a
`Datasource` (for database access); watch `cortex_failover_*` for reconciliation health.

## Two design choices

### Best-effort, and after the fact

Failover headroom is reserved **best-effort and *after* the workloads it protects already exist.** It does
not block a workload's initial placement — it pre-clears somewhere for that workload to go *later*, if its
host fails. So a failover reservation is a soft, continuously-maintained target rather than a hard
precondition for scheduling.

### Shared, not duplicated

Reserving a full spare copy of every workload would cost roughly `(m+1) × size` of capacity — untenable.
Instead, the reservation is **shared** across the workloads it protects: Cortex reserves only enough to
absorb `m` simultaneous host failures — a configurable tolerance — and lets the protected workloads share
that pool.

```
Naive (per-workload spare):   (m + 1) × total workload size
Cortex (shared headroom):     enough to absorb m concurrent host failures
```

Raising `m` buys resilience against more concurrent failures at the cost of more idle reserved capacity.
This is the classic HA trade-off, made explicit and tunable.

#### Why sharing is safe

Sharing headroom across many workloads only works if the reserved space is not needed by all of them at
once — and under this failure model it is not. The model plans for **host** failure, and assumes at most
`m` hosts fail concurrently; the workloads that would need to evacuate are those on *those* hosts, not the
whole fleet at the same instant. So the pool can be a fraction of the fleet's total size and still cover
any single (or `m`-way) failure, because a given unit of headroom is only ever claimed by one failure at a
time. That is what turns the `(m+1) × size` bill into "enough to absorb `m` concurrent failures".

Cortex reserves that pool **flexibly**, letting any protected workload draw from it, rather than
partitioning it. Two narrower strategies were considered and rejected: reserving headroom *within each
flavor group* (simpler to reason about, but strands capacity a busy group cannot lend to a quiet one) and
reserving *within a size class* (finer-grained, but multiplies the number of separate pools and the idle
capacity each one strands). Flexible cross-group sharing keeps the total reserved capacity smallest for a
given tolerance, at the cost of more bookkeeping to decide what can land where.

#### Global versus per-group tolerance

The tolerance `m` can be expressed two ways, and they compose differently:

- A **global** budget caps the total concurrent host failures the whole fleet plans for — the pool is
  sized to the `max` demand any single failure could place on it.
- A **per-flavor (or per-group)** budget sets a tolerance for each group independently — the pool must
  cover the `sum` of those budgets, because each group's failures are planned for separately.

A global budget is cheapest and fits a "the cloud tolerates `m` dead hosts" statement; per-group budgets
cost more headroom but let a critical flavor group carry a higher tolerance than the rest.

Two Nova weighers cooperate with this headroom during scheduling
([Nova smart scheduling](../02-external-scheduler-api/02-nova-smart-scheduling.md)):
`kvm_failover_reservation_consolidation` consolidates placement to preserve the shared headroom, and
`kvm_failover_evacuation` biases placement during an evacuation.

> [!NOTE]
> Failover reservations are a planned/maturing capability; in the current scope the failure they plan
> around is **host** failure — the control plane and Cortex itself are assumed available to drive the
> evacuation. Reservations are created best-effort *after* a workload spawns, so they are a
> continuously-maintained target, not a precondition. Verify the controller is reserving what you expect
> before relying on it for capacity planning.

Failover reservations are the second term subtracted from usable capacity in
[Reservations overview](01-reservations-overview.md): like committed resources, they make a host look
fuller to the pipeline than its raw capacity suggests, but for resilience rather than billing. They read
datasource facts through their `datasourceName` ([Chapter 4](../04-knowledge-database/readme.md)) and are
honoured by the Nova failover weighers. The next page turns from the *kinds* of reserved capacity to the
concurrency problem all of them share — how the scheduler keeps parallel requests from double-spending the
same slot.

## Next

[Prev: Committed-resource reservations](02-committed-resource-reservations.md) · [Next: Concurrency and in-flight reservations »](04-concurrency-and-in-flight-reservations.md)
