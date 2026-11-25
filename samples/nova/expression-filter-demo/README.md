<!--
Copyright SAP SE
SPDX-License-Identifier: Apache-2.0
-->

# Expression filter demo (nova)

A minimal end-to-end demo of the `filter_expression` nova step running on top of
a dynamically added expression knowledge — no Go code, no redeploy.

What it sets up:

- **`vms-per-host`** (`knowledge.yaml`): an expression knowledge that counts how
  many nova VMs are placed on each KVM (Cloud-Hypervisor/QEMU) compute host,
  producing a list of `{computeHost, vms}` features. VMware and Ironic hosts are
  excluded.
- **`nova-expression-demo-baseline`** (`pipeline-baseline.yaml`): an empty
  pipeline (no filters), used to show the host list returned *without* the
  filter step.
- **`nova-expression-demo`** (`pipeline.yaml`): the same empty pipeline plus one
  `filter_expression` step that excludes the ~1/3 of hosts with the most VMs.

The demo is script-driven: `demo.sh` applies, inspects, compares, and deletes
these resources on demand.

## How the candidate set works (KVM)

For KVM requests, cortex ignores the caller's host preselection and gathers the
**whole KVM fleet** before filtering (`GatherAllCandidates`). After the pipeline
runs, the external scheduler can limit the response back to the hosts that were
in the request (the `novaLimitHostsToRequest` option, enabled by default in the
cortex-nova bundle). This demo assumes that option is on.

So the `filter_expression` ranks VM counts across the entire fleet, but the
response only shows which of the request's hosts survived. The demo request
(`request.json`) lists 5 hosts, 2 of which are among the busiest third, so the
comparison shows those 2 removed. The flavor carries
`capabilities:hypervisor_type: CH` so the request routes to the KVM path.

## Prerequisites

- A cortex-nova deployment pointed at a **real OpenStack**, so the
  `nova-servers` and `nova-hypervisors` datasources are populated. The filter
  refuses to initialize against an empty or not-ready knowledge.
- `kubectl` targets that cluster; `curl` and `jq` are installed.
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

Both print the VM count per host, send the same request (`request.json`) to the
baseline and filtered pipelines, and list which of the request's hosts the
filter excluded (the ~1/3 with the most VMs).

### Run only part of it

Each step is a subcommand, so you can execute or re-run pieces in isolation:

```bash
./demo.sh knowledge   # apply the vms-per-host knowledge; show its spec + result
./demo.sh pipelines   # apply the baseline and filtered pipelines
./demo.sh apply       # apply the knowledge and both pipelines
./demo.sh counts      # print the VM count per host (top 15; COUNTS_TOP=0 for all)
./demo.sh schedule nova-expression-demo            # schedule against one pipeline
./demo.sh schedule nova-expression-demo-baseline   # ...and the baseline
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
./demo.sh schedule nova-expression-demo my-request.json
```

The `pipeline` field is overridden per invocation, so the same file works for
both pipelines.

## How the filter rule works

The expression is evaluated once per host, with the host name in `host` and the
knowledge features in `vms`. It returns a boolean: the host is kept when the
result is `true` and removed when it is `false`.

```
let loadedCounts = sort(map(filter(vms, int(.vms) > 0), int(.vms)), "desc");
let k = int((len(loadedCounts) + 2) / 3);
let cut = loadedCounts[k - 1];
let mine = int(sum(map(filter(vms, .computeHost == host), .vms)));
mine == 0 || mine < cut
```

Only hosts that actually run VMs are ranked. `cut` is the VM count at the 1/3
cutoff of loaded hosts (sorted descending); a host is dropped when its VM count
is at or above `cut` — i.e. it is in the busiest third. Idle hosts (and hosts
not present in the knowledge, treated as 0 VMs) are always kept.

Ranking only loaded hosts avoids the degenerate case where most of the fleet is
idle: a naive "top third of all hosts" would otherwise either drop nothing or
drop every loaded host.
