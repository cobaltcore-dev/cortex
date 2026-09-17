<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# FlavorGroupCapacity

`cortex.cloud/v1alpha1`, Kind `FlavorGroupCapacity`, cluster-scoped. One CRD per (flavor group ×
AZ) caches pre-computed capacity so the capacity API can answer without probing the scheduler on
every request. See [Operate reservations](../../guides/operate-reservations.md).

```bash
kubectl get flavorgroupcapacities
```

## Spec

| Field | Type | Description |
|---|---|---|
| `flavorGroup` | string | Flavor group name (e.g. `hana-v2`). |
| `availabilityZone` | string | AZ this capacity covers. |

## Status

| Field | Type | Description |
|---|---|---|
| `flavors` | []FlavorCapacityStatus | Per-flavor probe results: `{ flavorName, placeableHosts, placeableVms, totalCapacityHosts, totalCapacityVmSlots }`. |
| `committedCapacity` | int64 | Sum of accepted amounts across active CommittedResources, in smallest-flavor memory units. |
| `committedCapacityBytes` | int64 | `committedCapacity` in raw bytes. |
| `smallestFlavorName` | string | Smallest flavor in the group (the slot unit). |
| `totalCapacity` | map[string]Quantity | Installed capacity across eligible hosts (empty-datacenter). |
| `freeCapacity` | map[string]Quantity | Per-group remaining capacity (groups may share hosts, so cross-group sums can exceed installed). |
| `exclusivelyFreeCapacity` | map[string]Quantity | Fair per-group share from the round-robin split; cross-group sum never exceeds installed. |
| `exclusivelyFreeSlots` | int64 | Smallest-flavor slots available from `exclusivelyFreeCapacity`. |
| `runningInstances` | int64 | VMs running in this (group × AZ). |
| `runningResources` | map[string]Quantity | Consumption of running VMs. |
| `lastReconcileAt` | time | Last successful reconcile. |
| `conditions` | []Condition | Includes `Ready`. |

## Next steps

- Guide: [Operate committed-resource and failover reservations](../../guides/operate-reservations.md)
- Reference: [CommittedResource](committed-resource.md), [ProjectQuota](project-quota.md)
