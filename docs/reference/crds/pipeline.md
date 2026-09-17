<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Pipeline

`cortex.cloud/v1alpha1`, Kind `Pipeline`, cluster-scoped. A Pipeline is an ordered chain of
scheduling steps for one domain. See the [overview](../../concepts/overview.md) and [Extend
Cortex](../../guides/extend-cortex.md).

```bash
kubectl get pipelines
```

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `schedulingDomain` | string | — | Domain this pipeline serves. |
| `type` | string | — | `filter-weigher` (placement) or `detector` (descheduling). |
| `description` | string | — | Human-readable description. |
| `ignorePreselection` | bool | `false` | Gather all placement candidates and apply filters, instead of relying on a pre-filtered set and weights. |
| `filters` | []FilterSpec | — | Ordered filters; set only for `filter-weigher`. Filters remove candidates. |
| `weighers` | []WeigherSpec | — | Ordered weighers; set only for `filter-weigher`. Run after filters. |
| `detectors` | []DetectorSpec | — | Ordered detectors; set only for `detector`. |

### FilterSpec / DetectorSpec

| Field | Type | Description |
|---|---|---|
| `name` | string | Step name; must match a step implemented by the pipeline controller. |
| `params` | Parameters | Typed key/value list (see [Parameter](#parameter)). |
| `description` | string | Human-readable description. |

### WeigherSpec

Same as FilterSpec plus:

| Field | Type | Description |
|---|---|---|
| `multiplier` | float64 | Optional multiplier applied to this step's output relative to other steps. |

### Parameter

A `params` entry is a strongly-typed key/value pair (exactly one value field is set):
`key`, and one of `stringValue`, `boolValue`, `intValue`, `floatValue`, `stringListValue`,
`floatMapValue`.

## Status

| Field | Type | Description |
|---|---|---|
| `conditions` | []Condition | `Ready`, `AllStepsReady`, `AllStepsIndexed`. |

> [!NOTE]
> A defaulting/validating webhook rejects a Pipeline referencing an unknown filter/weigher/detector
> or malformed params. See [CRD & controller model](../../concepts/crd-controller-model.md).

## Next steps

- Concept: [Overview](../../concepts/overview.md)
- Reference: [Decision](decision.md), the transient result of a pipeline run.
