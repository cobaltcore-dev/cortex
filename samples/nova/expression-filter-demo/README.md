<!--
Copyright SAP SE
SPDX-License-Identifier: Apache-2.0
-->

# Expression filter demo (nova)

A minimal end-to-end demo of the `filter_expression` nova step running on top of
a dynamically added expression knowledge — no Go code, no redeploy. It mirrors
the CPU-contention example from the Knowledge DB 2.0 slides.

What it sets up:

- **`host-cpu-metrics`** (`knowledge-metrics.yaml`): synthetic raw CPU-contention
  metric samples (`{Node, Value}`), standing in for a Prometheus datasource so
  the demo runs offline. Carried directly as a literal-list expression.
- **`host-cpu-contention`** (`knowledge.yaml`): the slide's knowledge — an
  expr-lang expression that **joins** the metrics (by node) onto the real
  `nova-hypervisors` datasource (by hostname) and aggregates average and maximum
  contention per compute host, as `{ComputeHost, AvgCPUContention,
  MaxCPUContention}`. No Go code — the join is pure expression.
- **`nova-contention-demo-baseline`** (`pipeline-baseline.yaml`): an empty
  pipeline (no filters), used to show the host list returned *without* the
  filter step.
- **`nova-contention-demo`** (`pipeline.yaml`): the same empty pipeline plus one
  `filter_expression` step that keeps only hosts with average CPU contention
  below 50%.

Only the raw metrics are faked; the join onto the hypervisors and the filter are
exactly the real expression mechanism from the slides.

The demo is script-driven: `demo.sh` applies, inspects, compares, and deletes
these resources on demand.

## How the candidate set works (KVM)

For KVM requests, cortex ignores the caller's host preselection and gathers the
**whole KVM fleet** before filtering (`GatherAllCandidates`). After the pipeline
runs, the external scheduler can limit the response back to the hosts that were
in the request (the `novaLimitHostsToRequest` option, enabled by default in the
cortex-nova bundle). This demo assumes that option is on.

So the `filter_expression` evaluates against the fleet, but the response only
shows which of the request's hosts survived. The demo request (`request.json`)
lists 5 hosts, 2 of which have contention at or above 50%, so the comparison
shows those 2 removed. The flavor carries `capabilities:hypervisor_type: CH` so
the request routes to the KVM path.

## Prerequisites

- A cortex-nova deployment reachable via `kubectl`, with the `nova-hypervisors`
  datasource populated (the contention knowledge joins onto it). The CPU metrics
  are synthetic, so no live Prometheus is needed. `curl` and `jq` installed.
- The scheduler endpoint is reachable. If you run cortex under Tilt, it forwards
  `localhost:8001 -> 8080`. Otherwise start a port-forward:

  ```bash
  kubectl port-forward deploy/cortex-nova-scheduling-controller-manager 8001:8080
  ```

  If the endpoint is unreachable, the script prints this command and, when run
  interactively, offers to start the port-forward for the duration of the run.
  Set `AUTO_PORT_FORWARD=1` to start it without prompting, or override the
  endpoint with `SCHEDULER_URL`.

## Run it

Guided walkthrough — cleans up first, applies and shows each resource, runs the
comparison, then cleans up, pausing for Enter between steps:

```bash
./demo.sh walkthrough
```

Set `AUTO=1` to run it without the pauses.

Or run the whole thing in one shot (apply, then compare):

```bash
./demo.sh
```

Both print the contention per host, send the same request (`request.json`) to
the baseline and filtered pipelines, and list which of the request's hosts the
filter excluded (those with contention >= 50%).

### Run only part of it

Each step is a subcommand, so you can execute or re-run pieces in isolation:

```bash
./demo.sh knowledge   # apply the metrics + contention knowledges; show spec + result
./demo.sh pipelines   # apply the baseline and filtered pipelines
./demo.sh apply       # apply the knowledge and both pipelines
./demo.sh counts      # print avg CPU contention per host (top 15; COUNTS_TOP=0 for all)
./demo.sh schedule nova-contention-demo            # schedule against one pipeline
./demo.sh schedule nova-contention-demo-baseline   # ...and the baseline
./demo.sh compare     # schedule both and diff the returned hosts
```

`schedule` and `compare` only send requests; they assume the resources already
exist (run `./demo.sh apply` first). They fail fast with a hint if a pipeline is
not ready.

### Start over

Delete the demo resources:

```bash
./demo.sh clean
```

### Use your own candidate set

By default the request is read from `request.json` (5 hosts, flavor routed to
KVM). Edit it to use different hosts, or pass another file:

```bash
./demo.sh compare my-request.json
./demo.sh schedule nova-contention-demo my-request.json
```

The `pipeline` field is overridden per invocation, so the same file works for
both pipelines.

## How the filter rule works

The expression is evaluated once per host, with the host name in `host` and the
contention features in `contention`. It returns a boolean: the host is kept when
the result is `true` and removed when it is `false`.

```
any(contention,
  .ComputeHost == host &&
  .AvgCPUContention < 50)
```

A host is kept only when a matching contention feature exists and its average
contention is below 50%. Hosts above the threshold are dropped as too contended;
hosts without a contention feature are also dropped (no match), matching the
slide example.

## How the join works

`host-cpu-contention` groups the metric samples by `Node`, then for each nova
hypervisor whose `Hostname` has samples it emits `ComputeHost` (the hypervisor's
`ServiceHost`) with the mean and max of those samples. So a metric's `Node` must
equal the hypervisor's `Hostname`, and hypervisors without samples are omitted.
In the synthetic data the node names are set to real KVM hosts so the join
produces features for them.

