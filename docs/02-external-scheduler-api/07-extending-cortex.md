<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Extending Cortex

The chapter so far described the scheduling engine and its five domains as they ship. This closing page
is the practical payoff of the plugin model behind them: how you add your own pipeline step (filter,
weigher, or detector), knowledge extractor, or KPI. All three are *named* plugins — you register the
implementation in code, then reference it by name from a custom resource. The model behind this is
[The scheduling engine](01-the-scheduling-engine.md); this page is the how-to.

## Before you begin

- A local checkout and the Go toolchain — see [Local development with Tilt](../01-getting-started/08-local-development-with-tilt.md).
- Familiarity with which index your plugin belongs to (below).

## Choose the right registration style

All plugin categories use the same `init()` self-registration pattern, differing only in which
package-level `Index` map they register into:

| Plugin | Registration | Referenced by |
|---|---|---|
| Filter / weigher / detector | Self-register via `init()` into a package-level `Index` map of factory functions (e.g. `internal/scheduling/nova/plugins/filters/`) | `Pipeline` CRD step name |
| Knowledge extractor | Self-register via `init()` into the `Index` map in `internal/knowledge/extractor/plugins/zz_index.go` | `Knowledge` CRD |
| KPI | Self-register via `init()` into the `Index` map in `internal/knowledge/kpis/plugins/zz_index.go` | `KPI` CRD |
| Datasource | Self-register via `init()` into the `Index` map in the datasource plugin package (e.g. `internal/knowledge/datasources/plugins/openstack/zz_index.go`) | `Datasource` CRD |

## Add a scheduling step (filter / weigher / detector)

1. Implement the step interface in the appropriate scheduling package (filters, weighers, and detectors
   live under `internal/scheduling/<domain>/plugins/`).
2. Register it in that package's `init()` by adding a factory function to the `Index` map under a unique
   name — for example:

   ```go
   func init() {
       Index["filter_has_enough_capacity"] = func() NovaFilter { return &FilterHasEnoughCapacity{} }
   }
   ```

   Because registration happens in `init()`, importing the package is enough — no central list to edit.
3. Reference the step by that name in a `Pipeline` resource.

Filters run sequentially and remove hosts; weighers run in parallel and emit an activation that is
aggregated as `weight + multiplier * tanh(activation)`; detectors emit descheduling detections. See
[The scheduling engine](01-the-scheduling-engine.md) for the pipeline model.

Verify the step is actually registered by applying a `Pipeline` that references it — but note that a
successful apply is **not** by itself proof of registration. The pipeline admission webhook only *warns*
about an unknown step name and admits the resource anyway (the step is ignored at run time); it hard-fails
only on invalid parameters or a step of the wrong kind for the pipeline type. So confirm registration on
the resource itself:

```bash
kubectl apply -f my-pipeline.yaml
kubectl get pipeline <name>   # All Steps Known must be True
```

If the step name is unknown, the apply still succeeds but prints a `Warning: unknown ... will be ignored`
and the pipeline's `All Steps Known` (`AllStepsIndexed`) condition stays `False` — that, not the apply
result, is the signal your step was picked up. See
[Inspecting and editing a pipeline](01-the-scheduling-engine.md#inspecting-and-editing-a-pipeline).

## Add a knowledge extractor or KPI

1. Implement the extractor/KPI in the appropriate plugin subdirectory.
2. Add a `func init()` at the bottom of the file that registers a factory function into the
   package-level `Index` map (declared in `zz_index.go`) — for example:

   ```go
   func init() {
       plugins.Index["my_extractor"] = func() plugins.FeatureExtractor { return &MyExtractor{} }
   }
   ```

   Because registration happens in `init()`, importing the package is enough — no central list to edit.
3. Reference it from a `Knowledge` or `KPI` resource.

Verify with the feature/KPI metrics:

```
cortex_feature_pipeline_step_features{extractor="my-extractor"} > 0
cortex_kpi_state{name="my-kpi"} 1
```

## Add a datasource kind

Datasources self-register via `init()` into the `Index` map in their plugin package. Add the new kind
by implementing its ingestion and registering a factory function in `init()`, then reference it from a
`Datasource` resource. The datasource types are the `DatasourceType` / `OpenStackDatasourceType`
constants in `api/v1alpha1/datasource_types.go`, and the `Index` maps are in
`internal/knowledge/datasources/plugins/openstack/zz_index.go` and
`internal/knowledge/datasources/plugins/prometheus/zz_index.go`.

## Build and test locally

```bash
go build ./...
```

Run the change under Tilt to see it reconcile against a live cluster — see
[Local development with Tilt](../01-getting-started/08-local-development-with-tilt.md). Remember that a
change to the `api/v1alpha1` types means running `make generate` and committing the result
([Make targets](../01-getting-started/05-make-targets.md)).

This is the whole point of the plugin model: because steps, extractors, and KPIs are named and resolved
at run time, extending Cortex is adding code plus a registration, never editing a scheduler core. The
webhook ties it back to the `Pipeline` CRD (`api/v1alpha1/pipeline_types.go`) — invalid parameters and
wrong-kind steps are caught at apply time, while an unknown name is admitted with a warning and reported
through the pipeline's `All Steps Known` condition. With the scheduler API covered end to end, the next
chapter turns to what Cortex reserves on top of it.

## Next

[Prev: IronCore machine scheduling](06-ironcore-machine-scheduling.md) · [Next: Chapter 3 — Reservations and inventory management »](../03-reservations-and-inventory/readme.md)
