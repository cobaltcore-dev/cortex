<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# History

`cortex.cloud/v1alpha1`, Kind `History`, cluster-scoped. A History records the recent scheduling
decisions for one resource, capped at 10 entries, for operations teams to troubleshoot why a host
was (or was not) selected. See the [overview](../../concepts/overview.md).

```bash
kubectl get histories
```

## Spec

| Field | Type | Description |
|---|---|---|
| `schedulingDomain` | string | Domain the resource belongs to. |
| `resourceID` | string | Resource ID (e.g. Nova instance UUID). |
| `availabilityZone` | string | AZ, when the domain provides it (e.g. Nova). |

## Status

| Field | Type | Description |
|---|---|---|
| `current` | CurrentDecision | Latest decision: `{ timestamp, pipelineRef, intent, successful, targetHost, explanation, orderedHosts (≤3) }`. |
| `history` | []SchedulingHistoryEntry | Past decisions (≤10): `{ timestamp, pipelineRef, intent, orderedHosts (≤3), successful }`. |
| `conditions` | []Condition | `Ready` with reason `SchedulingSucceeded`, `PipelineRunFailed`, or `NoHostFound`. |

> [!NOTE]
> In a multicluster setup, History is routed by availability zone and can be stored on a remote
> cluster. See [Multicluster](../../concepts/multicluster.md).

## Next steps

- Concept: [Overview](../../concepts/overview.md)
- Guide: [Monitor Cortex](../../guides/monitor-cortex.md)
