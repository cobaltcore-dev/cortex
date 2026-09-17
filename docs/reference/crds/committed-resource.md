<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# CommittedResource

`cortex.cloud/v1alpha1`, Kind `CommittedResource`, cluster-scoped. A CommittedResource represents
a customer capacity commitment (sourced from Limes) for one flavor group in one AZ. Its controller
reconciles it into concrete [Reservation](reservation.md) slots. See [Operate
reservations](../../guides/operate-reservations.md).

```bash
kubectl get committedresources
```

## Spec

| Field | Type | Description |
|---|---|---|
| `commitmentUUID` | string | UUID of the commitment. |
| `schedulingDomain` | string | Domain (e.g. `nova`). |
| `flavorGroupName` | string | Flavor group targeted (e.g. `kvm_v2_hana_s`). |
| `resourceType` | string | `memory` (drives Reservation slots) or `cores` (arithmetic headroom check only, no slots). |
| `amount` | Quantity | Total committed quantity. `memory` in binary SI MiB (e.g. `1280Gi`); `cores` as an integer. |
| `availabilityZone` | string | AZ. |
| `projectID` / `domainID` | string | Owning project / domain. |
| `startTime` / `endTime` / `confirmedAt` | time | Activation / expiry / confirmation. |
| `state` | string | `planned`, `pending`, `guaranteed`, `confirmed`, `superseded`, `expired`. Only `guaranteed`/`confirmed` create slots. |
| `allowRejection` | bool | `true`: on placement failure the controller may reject (roll back slots, mark `Rejected`) — used by the API. `false`: the controller retries and keeps slots — used by the syncer. |

## Status

| Field | Type | Description |
|---|---|---|
| `acceptedSpec` | CommittedResourceSpec | Snapshot of the last accepted spec, used for rollback. |
| `acceptedAt` | time | When the spec was last reconciled into slots. |
| `lastReconcileAt` | time | Last reconcile. |
| `assignedInstances` | []string | VM UUIDs deterministically assigned to this commitment. |
| `usedResources` | map[string]Quantity | Consumption of assigned VMs (e.g. `memory`, `cpu`). |
| `lastUsageReconcileAt` | time | Last usage reconcile. |
| `usageObservedGeneration` | int64 | CR generation the usage reconciler last processed. |
| `statusSummary` | string | Compact summary for `kubectl -o wide`. |
| `conditions` | []Condition | `Ready` with reasons `Accepted`, `Planned`, `Reserving`, `Rejected`. |

## Next steps

- Guide: [Operate committed-resource and failover reservations](../../guides/operate-reservations.md)
- Reference: [Reservation](reservation.md), [FlavorGroupCapacity](flavor-group-capacity.md)
