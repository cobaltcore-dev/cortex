<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Reservation

`cortex.cloud/v1alpha1`, Kind `Reservation`, cluster-scoped. A Reservation holds capacity on a
specific host. See [Operate reservations](../../guides/operate-reservations.md).

```bash
kubectl get reservations
```

## Spec

| Field | Type | Description |
|---|---|---|
| `type` | string | `CommittedResourceReservation`, `FailoverReservation`, or `InFlightReservation`. |
| `schedulingDomain` | string | Domain (e.g. `nova`, `machines`). |
| `availabilityZone` | string | AZ, if restricted to one. Used for multicluster routing. |
| `resources` | map[ResourceName]Quantity | Resources to reserve. |
| `startTime` / `endTime` | time | Activation / expiry. |
| `targetHost` | string | Desired host (Nova hypervisor / Pod node). |
| `committedResourceReservation` | object | Set when `type: CommittedResourceReservation` (below). |
| `failoverReservation` | object | Set when `type: FailoverReservation`: `{ resourceGroup }`. |
| `inFlightReservation` | object | Set when `type: InFlightReservation`: `{ vmID, userID, projectID, intent }`. |

### `committedResourceReservation`

| Field | Type | Description |
|---|---|---|
| `resourceName` | string | Resource name (e.g. Nova flavor name). |
| `commitmentUUID` | string | Commitment this reservation corresponds to. |
| `resourceGroup` | string | Resource group (Nova flavor group). |
| `projectID` / `domainID` | string | Owning project / domain. |
| `creator` | string | Component that created it (e.g. `commitments-syncer`). |
| `parentGeneration` | int64 | CommittedResource generation echoed to status once processed. |
| `allocations` | map[string]object | Workload UUID → `{ creationTimestamp, resources }`. |

## Status

| Field | Type | Description |
|---|---|---|
| `host` | string | Actual placed host (set by the scheduler). |
| `committedResourceReservation` | object | `{ observedParentGeneration, allocations (VM→host) }`. |
| `failoverReservation` | object | `{ allocations (VM→host), lastChanged, acknowledgedAt }`. |
| `inFlightReservation` | object | No captured state currently. |
| `conditions` | []Condition | Includes `Ready`. |

Labels: `reservations.cortex.cloud/type` = `committed-resource` \| `failover` \| `in-flight`.
Annotation `reservations.cortex.cloud/creator-request-id` tracks the creating request.

## Next steps

- Concept: [Overview](../../concepts/overview.md)
- Guide: [Operate committed-resource and failover reservations](../../guides/operate-reservations.md)
- Reference: [CommittedResource](committed-resource.md)
