<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Extend Cortex

This guide adds a new pipeline step (filter, weigher, or detector), knowledge extractor, or KPI to
Cortex. All three are *named* plugins: you register the implementation in code, then reference it by
name from a CRD. For the concept, see the [CRD/controller model](../concepts/crd-controller-model.md).

## Before you begin

- A local checkout and the Go toolchain — see the [Tilt tutorial](../tutorials/local-development-with-tilt.md).
- Familiarity with which index your plugin belongs to (below).

## Choose the right registration style

Cortex uses three registration mechanisms depending on the plugin category:

| Plugin | Registration | Referenced by |
|---|---|---|
| Filter / weigher / detector | Self-register via `init()` into a package-level `Index` map of factory functions | `Pipeline` CRD step name |
| Knowledge extractor | Entry in a hand-maintained static map | `Knowledge` CRD |
| KPI | Entry in a hand-maintained static map | `KPI` CRD |
| Datasource | Typed switch / map on datasource kind | `Datasource` CRD |

## Add a scheduling step (filter / weigher / detector)

1. Implement the step interface in the appropriate scheduling package.
2. Register it in that package's `init()` by adding a factory function to the `Index` map under a
   unique name. Because registration happens in `init()`, importing the package is enough — no
   central list to edit.
3. Reference the step by that name in a `Pipeline` resource.

Filters run sequentially and remove hosts; weighers run in parallel and emit an activation that is
aggregated as `weight + multiplier * tanh(activation)`; detectors emit descheduling detections. See
the [overview](../concepts/overview.md) for the pipeline model.

Verify the step is registered by applying a `Pipeline` that references it — the pipeline admission
webhook rejects unknown step names, so a successful apply confirms registration:

```bash
kubectl apply -f my-pipeline.yaml
```

If the name is unknown, the apply is rejected with a validation error naming the missing step.

## Add a knowledge extractor or KPI

1. Implement the extractor/KPI.
2. Add its name to the hand-maintained static map (unlike scheduling steps, these are **not**
   self-registering — a missing map entry means the name is unavailable).
3. Reference it from a `Knowledge` or `KPI` resource.

Verify with the feature/KPI metrics:

```
cortex_feature_pipeline_step_features{extractor="my-extractor"} > 0
cortex_kpi_state{name="my-kpi"} 1
```

## Add a datasource kind

Datasources are dispatched by a typed switch/map on kind. Add the new kind to that dispatch and
implement its ingestion, then reference it from a `Datasource` resource. See the
[Datasource reference](../reference/crds/datasource.md).

## Build and test locally

```bash
go build ./...
```

Run the change under Tilt to see it reconcile against a live cluster — see the
[Tilt tutorial](../tutorials/local-development-with-tilt.md).

> [!NOTE]
> Do not run `make` as part of documentation work. For code contributions, follow the project's
> normal build/test workflow.

## Next steps

- Concept: [CRD/controller model](../concepts/crd-controller-model.md), [Overview](../concepts/overview.md)
- Tutorial: [Local development with Tilt](../tutorials/local-development-with-tilt.md)
- Reference: [Pipeline](../reference/crds/pipeline.md), [Knowledge](../reference/crds/knowledge.md), [KPI](../reference/crds/kpi.md)
