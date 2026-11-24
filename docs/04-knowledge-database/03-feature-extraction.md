<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Feature extraction

Raw datasource objects are facts, not insight. Feature extraction is the middle stage of the knowledge flow:
a `Knowledge` resource names an *extractor* (Go code) or an *expression* that receives the cached datasource
objects as typed lists, does the expensive joining and aggregation once, and writes the result back as a
**feature** the rest of Cortex can read cheaply. This page covers how a `Knowledge` declares its inputs, the
two ways to extract features, and the recency mechanism that keeps features fresh without recomputing them
on every request.

## Inputs: typed lists of objects

Extractors never talk to a database. A `Knowledge` names its inputs in `spec.inputs`, each binding a
variable name to a `Datasource` or another `Knowledge`:

```yaml
apiVersion: cortex.cloud/v1alpha1
kind: Knowledge
metadata:
  name: host-capabilities
spec:
  schedulingDomain: nova
  extractor:
    name: host_capabilities_extractor
  recency: 60s
  inputs:
    - var: hypervisors
      datasource: nova-hypervisors
    - var: traits
      datasource: placement-resource-provider-traits
```

Before an extraction, the controller resolves every input (`pkg/knowledge/inputs`):

- A **datasource** input lists all objects of that datasource from its storage backend
  (`spec.storage` on the `Datasource`, see [Datasources](02-datasources.md)) as a slice of the datasource's
  Go model, e.g. `[]nova.Hypervisor`. Prometheus datasources sharing a table are scoped to their alias.
- A **knowledge** input unboxes the features of that knowledge, e.g. `[]compute.HostPinnedProjects`.

Inputs also drive re-extraction: a change to a referenced datasource or knowledge triggers the dependent
knowledge (within its recency). Resolution errors surface as the `Ready` condition reasons `InputMissing`,
`InputUnused`, `InputTypeMismatch` and `InputFetchFailed`.

## Go extractors

A Go extractor declares its inputs as a struct with fields tagged `input:"<var>"` and returns typed
features. The feature type lives in the same file, so consumers import it and unbox the knowledge with
`v1alpha1.UnboxFeatureList[compute.HostCapabilities](knowledge.Status.Raw)`:

```go
type HostCapabilitiesInputs struct {
	Hypervisors []nova.Hypervisor `input:"hypervisors"`
	Traits      []placement.Trait `input:"traits"`
}

type HostCapabilitiesExtractor struct {
	extractor.BaseExtractor[struct{}] // options from spec.extractor.config
}

func (e *HostCapabilitiesExtractor) Extract(ctx context.Context, in HostCapabilitiesInputs) ([]HostCapabilities, error) {
	// join and aggregate in Go
}

func init() {
	extractor.Register("host_capabilities_extractor",
		func() extractor.Extractor[HostCapabilitiesInputs, HostCapabilities] { return &HostCapabilitiesExtractor{} })
}
```

`extractor.Register` (`pkg/knowledge/extractor`) validates the inputs struct at startup and also registers
the feature type by its Go type name, so expression knowledges can produce it. The configured inputs must
match the tagged fields exactly, and the datasource model must match the field's element type.

Cortex ships two plugin families under `internal/knowledge/extractor/plugins`:

- **compute** — `host_utilization_extractor`, `host_capabilities_extractor`, `host_az_extractor`,
  `host_pinned_projects_extractor`, `vmware_host_details_extractor`, `flavor_groups`,
  `vm_host_residency_extractor`, `vm_life_span_histogram_extractor`, the vROps resolver, noisiness and
  contention extractors, and `kvm_libvirt_domain_cpu_steal_pct_extractor` (the feature the
  [`avoid_high_steal_pct` detector](../02-external-scheduler-api/02-nova-smart-scheduling.md) acts on).
- **storage** — `netapp_storage_pool_cpu_usage_extractor`, the feature behind Manila's one weigher.

Extractors outside the cortex repository can be built into a custom manager binary, see
[Extend Cortex](../02-external-scheduler-api/07-extending-cortex.md).

## Expression knowledges

Instead of Go code, a knowledge can carry an [expr](https://expr-lang.org) expression. The inputs are
available as variables, and fields of the input objects are accessed by their **Go field names**. The
expression must return a list, which is strictly decoded into the registered Go feature type named in
`output` — so consumers keep using the same typed feature:

```yaml
spec:
  expression:
    output: HostCapabilities
    run: |
      let traitsByProvider = groupBy(traits, .ResourceProviderUUID);
      hypervisors
      | filter(len(traitsByProvider[.ID] ?? []) > 0)
      | groupBy(.ServiceHost + "/" + .HypervisorType)
      | values()
      | map(let g = #; {
          ComputeHost:    g[0].ServiceHost,
          HypervisorType: g[0].HypervisorType,
          Traits:         g | map(traitsByProvider[.ID]) | flatten() | map(.Name) | sort() | join(","),
        })
      | sortBy(.ComputeHost)
```

Things to keep in mind:

- Object keys of the output must match the JSON field names of the output type (the Go field name if it
  has no `json` tag, matched case-insensitively); unknown keys fail with `ExpressionOutputInvalid`.
  Missing keys are left at their zero value.
- Typos in variables or input fields are caught when the expression is compiled
  (`ExpressionCompilationFailed`).
- A map literal can't be the direct argument of `map(...)`: `map({...})` is parsed as a closure body.
  Bind the element first, e.g. `map(let o = #; {...})`.
- Pointer fields (e.g. `*string`) compare fine with `==` and `nil`, but `groupBy` and `sort` use the pointer
  itself. Dereference string pointers with `.Field + ""`.
- Join through an index built with `groupBy` as above. Filtering one list inside a `map` over another is
  quadratic and quickly takes seconds on real inventories.
- `groupBy` and `values()` return entries in random order; sort the output (e.g. `sortBy`) for a stable
  result. Change detection ignores the order of features either way.
- Evaluations are bounded by the knowledge controller config: `expressionTimeoutSeconds` (default 60) and
  `expressionMemoryBudget` (default 1e8, counted by expr in allocated list and map elements). A timed out
  evaluation can't be interrupted and finishes in the background.

## Where features live

The extracted features are written to the resource's own status, not to a separate table: `status.raw` holds
the boxed feature list under a `{"features": [...]}` envelope, alongside `rawLength`, `lastExtracted`, and
`lastContentChange` (which advances only when the features actually change, ignoring their order, so
consumers can tell a real update from a no-op re-run).

## Recency: fresh enough, not recomputed constantly

`spec.recency` (default `60s`) is the contract for staleness: the controller re-runs the extractor when the
features are older than the recency window, comparing `status.lastExtracted` against `spec.recency`. This is
the knob that makes the intermediate feature step pay off — a `host_utilization` feature is derived at most
once per recency window and then read by every weigher for free, rather than recomputed on each placement
request.

> [!NOTE]
> A tighter `recency` gives fresher features at the cost of more extraction work and storage reads. Match it
> to how fast the underlying signal actually moves — utilization drifts slowly; a per-second value gains
> nothing from a per-second re-extraction.

## Inspecting knowledge resources

`Knowledge` is cluster-scoped. List what is deployed and see extraction state at a glance:

```bash
kubectl get knowledges
```

```
NAME               DOMAIN   CREATED   EXTRACTED   CHANGED   RECENCY   FEATURES   READY
host-utilization   nova     3d        45s         12m       60s       1240       True
```

`Extracted` is the last run (`.status.lastExtracted`), `Changed` the last time the features actually
differed (`.status.lastContentChange`), and `Features` the number produced (`.status.rawLength`) — so a
`Changed` timestamp far older than `Extracted` means the extractor is re-running but the signal is stable.
Read the conditions (including the input and expression reasons above) and the extracted feature envelope with:

```bash
kubectl get knowledge host-utilization -o yaml
```

Feature extraction is the value-add stage of the [knowledge flow](01-knowledge-flow-overview.md): it turns
the datasource objects of the [previous page](02-datasources.md) into the query-ready features that every
weigher in [Chapter 2](../02-external-scheduler-api/readme.md) and every reservation controller in
[Chapter 3](../03-reservations-and-inventory/readme.md) reads. The `Knowledge` CRD is documented
field-by-field on the `Knowledge` type in `api/v1alpha1/knowledge_types.go`. The next page turns features into
metrics — the observability half of the flow.

## Next

[Prev: Datasources](02-datasources.md) · [Next: KPIs and the metrics endpoint »](04-kpis-and-metrics-api.md)
