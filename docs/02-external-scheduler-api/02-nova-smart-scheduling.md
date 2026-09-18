<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Nova smart scheduling

Nova is the domain Cortex was built for first, and the one with by far the most steps. This page shows how
Cortex hooks into OpenStack compute as an external scheduler, which pipeline a request is routed to, the
filters and weighers it ships, and how descheduling closes the loop. It assumes you have read
[The scheduling engine](01-the-scheduling-engine.md) — everything here is that engine with a Nova-specific
set of plugins.

## The external scheduler hook

Nova calls Cortex over HTTP at:

```
POST /scheduler/nova/external
```

The handler (`internal/scheduling/nova/external_scheduler_api.go`) receives Nova's candidate hosts and
their weights, runs the selected pipeline, and returns a re-ordered host list. Each request produces a
`Decision` resource (generated name `nova-…`) recording what happened.

> [!NOTE]
> This hook supports the two-phase integration from
> [The scheduling engine](01-the-scheduling-engine.md): Nova can run Cortex **advisory** — reordering the
> candidates Nova would otherwise use, off the critical path — before graduating it to a **drop-in**
> scheduler whose ordering Nova takes directly. Same endpoint, same pipeline; what changes is how much
> Nova binds itself to the answer.

Two things run *before* the normal pipeline, because they are operator escape hatches:

- **Forced destination.** If the request carries a forced host and `ForcedDestinationEnabled` is set (the
  default), Cortex returns those hosts directly and skips filtering and weighing entirely. This runs
  before the "can we schedule this?" check because Nova may not send weights for a forced request.
- **Evacuation shuffle.** For an evacuation intent, `EvacuationShuffleK` randomly reorders the top `k`
  hosts, so a wave of evacuations does not stampede every VM onto the same "best" host. Set it to `0` to
  disable.

## Which pipeline: inferred from hypervisor and flavor

Nova does not use one pipeline. Cortex *infers* the pipeline name from the request
(`inferPipelineName`): the hypervisor type selects `kvm-*` (for CH/QEMU) or `vmware-*`, and the flavor
selects the strategy — a HANA flavor routes to `*-hana-bin-packing`, everything else to
`*-general-purpose-load-balancing`. The four resulting pipelines are:

- `kvm-general-purpose-load-balancing`
- `kvm-hana-bin-packing`
- `vmware-general-purpose-load-balancing`
- `vmware-hana-bin-packing`

This is why the weighers come in `kvm_*` and `vmware_*` families: the split is realized at pipeline
selection, so a KVM request never runs a VMware weigher and vice versa.

## The filters

Nova ships seventeen filters (registered under `internal/scheduling/nova/plugins/filters/`). Filters run
sequentially and each removes hosts that fail its rule:

| Filter | Removes hosts that… |
|---|---|
| `filter_correct_az` | are not in the requested availability zone |
| `filter_has_enough_capacity` | lack free capacity for the request |
| `filter_capabilities` | do not satisfy requested capabilities |
| `filter_has_requested_traits` | are missing requested Placement traits |
| `filter_has_accelerators` | lack requested accelerators (e.g. GPUs) |
| `filter_image_properties` | are incompatible with the image's properties |
| `filter_aggregate_metadata` | fail host-aggregate metadata matching |
| `filter_allowed_projects` | are not allowed for the request's project |
| `filter_external_customer` | violate external-customer isolation rules |
| `filter_instance_group_affinity` | break a required instance-group affinity |
| `filter_instance_group_anti_affinity` | break a required instance-group anti-affinity |
| `filter_live_migratable` | cannot accept a live-migratable instance |
| `filter_quota_enforcement` | would exceed enforced quota |
| `filter_requested_destination` | are not the explicitly requested destination |
| `filter_host_instructions` | are excluded by per-host instructions |
| `filter_exclude_hosts` | appear on an explicit exclusion list |
| `filter_status_conditions` | are unhealthy per their status conditions |

## The weighers

Weighers run in parallel; each emits an activation combined as `weight + multiplier·tanh(activation)`.
They split by hypervisor family:

**KVM weighers:**

- `kvm_binpack` — pack VMs onto fuller hosts (consolidation).
- `kvm_prefer_smaller_hosts` — bias toward smaller hosts.
- `kvm_committed_resource_reservation` — respect committed-resource reservations (see
  [Committed-resource reservations](../03-reservations-and-inventory/02-committed-resource-reservations.md)).
- `kvm_failover_reservation_consolidation` — consolidate to preserve failover headroom.
- `kvm_failover_evacuation` — bias placement during evacuation to keep failover capacity (see
  [Failover reservations](../03-reservations-and-inventory/03-failover-reservations.md)).
- `kvm_instance_group_soft_affinity` — honor *soft* instance-group affinity as a preference.

**VMware weighers:**

- `vmware_binpack` — consolidation for VMware hosts.
- `vmware_anti_affinity_noisy_projects` — spread noisy projects apart.
- `vmware_avoid_short_term_contended_hosts` — avoid hosts with recent contention.
- `vmware_avoid_long_term_contended_hosts` — avoid persistently contended hosts.

> [!NOTE]
> Several Nova weighers read *features* produced by the knowledge database — contention and steal
> metrics, committed-resource state. Those come from [Chapter 4](../04-knowledge-database/readme.md); the
> weigher only reads the already-extracted feature.

## Recording committed-resource slots

When committed-resource tracking is enabled (`FeatureGates.CommittedResourceTracking`, and not skipped per
request), a successful placement is recorded into the matching reservation slot by the `crs` recorder
(`internal/scheduling/nova/crs/`). It resolves the flavor to a flavor group; a flavor in no group is a
pay-as-you-go placement and is not slotted. This is how a committed reservation goes from "promised" to
"consumed" — the mechanics are in
[Committed-resource reservations](../03-reservations-and-inventory/02-committed-resource-reservations.md).

> [!NOTE]
> Because Nova *executes* the placement Cortex only *recommended*, a returned ordering is not yet a
> confirmed location — and several Nova requests can be in flight at once. How Cortex reserves for its
> candidates before the next request scores, and how it reconciles against what Nova actually did, is the
> subject of
> [Concurrency and in-flight reservations](../03-reservations-and-inventory/04-concurrency-and-in-flight-reservations.md).

## Closing the loop: descheduling

Beyond initial placement, Nova runs a **detector pipeline** on a schedule to find VMs that should move.
Today the shipped detector is `avoid_high_steal_pct`, which flags VMs whose CPU-steal percentage (a
knowledge feature) exceeds a threshold over an observed window. The flow:

1. The `kvm-descheduler` detector pipeline runs periodically (roughly once a minute, jittered), combines
   detections, and passes them through a cycle breaker so a VM is not endlessly bounced.
2. It creates `Descheduling` resources naming the affected VM.
3. A separate **executor** acts on them — in dry-run by default (`DisableDeschedulerDryRun` flips it to
   issue real live-migrations via the Nova client).
4. A **cleanup** controller TTL-expires old `Descheduling` resources (and runs once on startup).

## How this relates to Cortex

Nova exercises the full arc: an HTTP request drives a filter-weigher pipeline whose weighers read
knowledge features and respect reservations, the result is a `Decision`, committed slots are recorded, and
a scheduled detector emits `Descheduling` recommendations recorded as `History`. Every other domain in
this chapter is a smaller version of this same shape — starting with the storage domains, which are
HTTP-driven like Nova but ship far fewer steps.

## Next

[Prev: The scheduling engine](01-the-scheduling-engine.md) · [Next: Manila smart scheduling »](03-manila-smart-scheduling.md)
