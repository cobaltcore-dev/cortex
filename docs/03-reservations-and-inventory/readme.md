<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Chapter 3 — Reservations and inventory management

Placement is not only about *where a workload could go right now* — it is also about *space that is
promised but not yet consumed*. This chapter covers the machinery that models that promised space, so
the scheduler never hands out capacity that is already spoken for, and the component through which
Cortex intends to become the authority for inventory itself.

The chapter opens with an overview of the three reservation kinds, then goes deep on committed
resources (customer commitments synced from Limes, plus the quota and capacity controllers that
support them) and failover reservations (headroom for evacuating workloads when a host fails). It
then covers the concurrency problem all reservations share — keeping parallel scheduling requests from
double-spending the same slot — and closes with the **Placement API shim**, the
OpenStack-Placement-compatible front through which Cortex will eventually serve inventory for the hosts
it understands.

## In this chapter

1. [Reservations overview](01-reservations-overview.md) — committed, failover, and in-flight reservations.
2. [Committed-resource reservations and Limes quota/capacity](02-committed-resource-reservations.md) — commitments, `FlavorGroupCapacity`, `ProjectQuota`, and the LIQUID API.
3. [Failover reservations for KVM HA](03-failover-reservations.md) — shared, best-effort evacuation headroom.
4. [Concurrency and in-flight reservations](04-concurrency-and-in-flight-reservations.md) — how parallel requests avoid double-spending capacity.
5. [The Placement API shim](05-placement-api-shim.md) — passthrough today, KVM-backed inventory planned.

## Next

[Prev: Chapter 2 — The external scheduler API](../02-external-scheduler-api/readme.md) · [Next: Reservations overview »](01-reservations-overview.md)
