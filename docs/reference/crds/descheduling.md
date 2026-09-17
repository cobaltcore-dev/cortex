<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Descheduling

`cortex.cloud/v1alpha1`, Kind `Descheduling`, cluster-scoped. A Descheduling requests that a
running VM be moved off its current host. See the [overview](../../concepts/overview.md).

```bash
kubectl get deschedulings
```

## Spec

| Field | Type | Description |
|---|---|---|
| `ref` | string | Reference to the VM to deschedule. |
| `refType` | string | Reference type; currently `novaServerUUID`. |
| `prevHost` | string | Host to deschedule from. |
| `prevHostType` | string | Host type; currently `novaComputeHostName`. |
| `reason` | string | Human-readable reason. |

## Status

| Field | Type | Description |
|---|---|---|
| `newHost` | string | Host the VM was rescheduled to. |
| `newHostType` | string | Type of the new host. |
| `conditions` | []Condition | `Ready`, `InProgress`. |

## Next steps

- Concept: [Overview](../../concepts/overview.md) — detector pipelines produce descheduling recommendations.
