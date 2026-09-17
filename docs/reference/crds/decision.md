<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Decision

`cortex.cloud/v1alpha1`, Kind `Decision`, cluster-scoped.

> [!NOTE]
> The Decision is a **transient, in-memory** carrier used by the external scheduler API and
> filter-weigher pipelines to compute a placement. It is **not persisted to etcd** today — the
> outcome is recorded in [History](history.md) instead. Its long-term shape is under discussion.

## Spec

| Field | Type | Description |
|---|---|---|
| `schedulingDomain` | string | Domain this decision was processed in. |
| `pipelineRef` | ObjectReference | Pipeline used for this decision. |
| `resourceID` | string | Identifier of the scheduled resource (e.g. Nova instance UUID). Selectable field. |
| `novaRaw` | RawExtension | Raw Nova request; set for `nova`. |
| `cinderRaw` | RawExtension | Raw Cinder request; set for `cinder`. |
| `manilaRaw` | RawExtension | Raw Manila request; set for `manila`. |
| `machineRef` | ObjectReference | Machine reference; set for `machines`. |
| `podRef` | ObjectReference | Pod reference; set for `pods`. |
| `intent` | string | Scheduling intent (default `Unknown`). |

## Status

| Field | Type | Description |
|---|---|---|
| `result.rawInWeights` | map[string]float64 | Raw input weights per host. |
| `result.normalizedInWeights` | map[string]float64 | Normalized input weights. |
| `result.stepResults` | []StepResult | Per-step `{ stepName, activations{host→float} }`. |
| `result.aggregatedOutWeights` | map[string]float64 | Aggregated output weights. |
| `result.orderedHosts` | []string | Hosts most→least preferred. |
| `result.targetHost` | string | First ordered host. |
| `history` | []ObjectReference | Prior decisions for the same resource. |
| `precedence` | int | Number of decisions preceding this one. |
| `explanation` | string | Human-readable explanation. |
| `conditions` | []Condition | Includes `Ready`. |

## Next steps

- Concept: [The delegation model](../../concepts/delegation-model.md)
- Reference: [History](history.md), where outcomes are persisted.
