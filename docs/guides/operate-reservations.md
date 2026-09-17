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

### Verify the sync

```bash
kubectl get committedresources
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
positive and the `CortexCommittedResourceHostOversubscribed` alert fires; the controller may evict
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

Both should report a `Ready` condition and a recent `lastReconcileAt`.

## Failover reservations

The `failover-reservations-controller` reserves headroom so workloads can be evacuated on host
failure. It needs a `datasourceName` pointing at a Datasource (for database access). Watch
`cortex_failover_*` for reconciliation health.

## In-flight reservations

The `inflight-reservation-controller` tracks reservations for placements that are decided but not
yet fully realized, preventing double-spend during the placement window.

## Troubleshooting

- **`unfulfilled > 0`** — capacity shortfall or a failing reservation controller; check
  `cortex_reservations` and the controller logs.
- **Capacity/quota not reconciling** — confirm the controller is enabled and, for capacity, that its
  scheduler URL is set; for failover, that `datasourceName` resolves.
- **Oversubscription alert** — reduce commitments or add capacity; evictions are the last resort.

## Next steps

- Reference: [Reservation](../reference/crds/reservation.md), [CommittedResource](../reference/crds/committed-resource.md), [FlavorGroupCapacity](../reference/crds/flavor-group-capacity.md), [ProjectQuota](../reference/crds/project-quota.md)
- Reference: [Metrics and alerts](../reference/metrics.md), [Configuration](../reference/configuration.md)
