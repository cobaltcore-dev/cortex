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
  name: vm-host-residency
spec:
  schedulingDomain: nova
  extractor:
    name: vm_host_residency_extractor
  recency: 60s
  inputs:
    - var: migrations
      datasource: nova-migrations
    - var: servers
      datasource: nova-servers
    - var: flavors
      datasource: nova-flavors
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

Use a Go extractor when the derivation doesn't fit an [expression](#expression-knowledges), e.g.
histograms or multi-step logic. It declares its inputs as a struct with fields tagged `input:"<var>"` and
returns typed features. The feature type lives in the same file, so consumers import it and unbox the
knowledge with `v1alpha1.UnboxFeatureList[compute.VMHostResidencyHistogramBucket](knowledge.Status.Raw)`:

```go
type VMHostResidencyInputs struct {
	Migrations []nova.Migration `input:"migrations"`
	Servers    []nova.Server    `input:"servers"`
	Flavors    []nova.Flavor    `input:"flavors"`
}

type VMHostResidencyExtractor struct {
	extractor.BaseExtractor[struct{}] // options from spec.extractor.config
}

func (e *VMHostResidencyExtractor) Extract(ctx context.Context, in VMHostResidencyInputs) ([]VMHostResidencyHistogramBucket, error) {
	// join and aggregate in Go
}

func init() {
	extractor.Register("vm_host_residency_extractor",
		func() extractor.Extractor[VMHostResidencyInputs, VMHostResidencyHistogramBucket] { return &VMHostResidencyExtractor{} })
}
```

`extractor.Register` (`pkg/knowledge/extractor`) validates the inputs struct at startup. The configured
inputs must match the tagged fields exactly, and the datasource model must match the field's element type.

Cortex ships these Go extractors under `internal/knowledge/extractor/plugins/compute`:
`host_pinned_projects_extractor`, `vmware_host_details_extractor`, `flavor_groups`,
`vm_host_residency_extractor` and `vm_life_span_histogram_extractor`. All other knowledges in the helm
bundles are expressions.

Extractors outside the cortex repository can be built into a custom manager binary, see
[Extend Cortex](../02-external-scheduler-api/07-extending-cortex.md).

## Expression knowledges

Instead of Go code, a knowledge can carry an [expr](https://expr-lang.org) expression. The inputs are
available as variables, and fields of the input objects are accessed by their **Go field names**. The
expression must return a list, whose elements are stored as JSON features — no Go type needs to exist to
add a new knowledge. Consumers model the features themselves and unbox them with
`v1alpha1.UnboxFeatureList[T]`, e.g. into `compute.HostCapabilities`:

```yaml
spec:
  expression:
    run: |
      let traitsByProvider = groupBy(traits, .ResourceProviderUUID);
      hypervisors
      | filter(len(traitsByProvider[.ID] ?? []) > 0)
      | groupBy(.ServiceHost + "\x00" + .HypervisorType)
      | values()
      | map(let g = #; {
          ComputeHost:    g[0].ServiceHost,
          HypervisorType: g[0].HypervisorType,
          Traits:         g | map(traitsByProvider[.ID]) | flatten() | map(.Name) | sort() | join(","),
        })
      | sortBy(.ComputeHost + "\x00" + .HypervisorType)
```

The expression knowledges shipped with cortex are `host-capabilities`, `host-utilization`, `host-az`,
`kvm-libvirt-domain-cpu-steal-pct` (the feature the
[`avoid_high_steal_pct` detector](../02-external-scheduler-api/02-nova-smart-scheduling.md) acts on), the
vROps knowledges `vmware-resolved-hostsystems`, `vmware-project-noisiness`, `vmware-long-term-contended-hosts`
and `vmware-short-term-contended-hosts`, and `netapp-storage-pool-cpu-usage-manila`, the feature behind
Manila's one weigher. Their test cases live in `internal/knowledge/extractor/plugins/expressions_test.go`,
which evaluates the expressions straight from the helm templates and checks that the features decode into
the Go types their consumers use; `extractor.EvaluateExpression` does the same for your own expressions.

Things to keep in mind:

- Object keys of the output must match the JSON field names of the consumer's Go type (the Go field name
  if it has no `json` tag, matched case-insensitively). Nothing enforces this at runtime: unknown keys are
  ignored by `UnboxFeatureList` and missing keys are left at their zero value, so cover it with a test.
- The output must be a list of JSON values, otherwise the knowledge fails with `ExpressionOutputInvalid`.
- Features of an expression knowledge used as input of another expression are generic maps keyed as
  written by the producing expression, so typos in their keys are only noticed at runtime.
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
