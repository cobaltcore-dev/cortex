<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Glossary

One-line definitions of the domain terms used across the Cortex book. Terms link to the chapter or
reference page where they are explained in depth.

| Term | Definition |
|---|---|
| **Scheduling domain** | A logical area Cortex schedules for: `nova`, `cinder`, `manila`, `machines` (IronCore), or `pods`. Set on most CRDs via `schedulingDomain`. |
| **Modular monolith** | The single `manager` binary that contains every controller, extractor, and pipeline type; each deployment runs only the subset selected by `enabledControllers`/`enabledTasks`. See [Architecture at a glance](01-getting-started/02-architecture-at-a-glance.md). |
| **[Datasource](../api/v1alpha1/datasource_types.go)** | A CRD describing an external source (Prometheus or OpenStack) that Cortex periodically syncs into its Postgres store. |
| **[Knowledge](../api/v1alpha1/knowledge_types.go)** | A CRD describing a feature extractor that turns raw datasource rows into enriched, strongly-typed features. |
| **Feature** | A single enriched datum produced by a knowledge extractor and consumed by scheduling steps. |
| **[KPI](../api/v1alpha1/kpi_types.go)** | A CRD describing a key performance indicator computed from datasources/knowledges and exported as a metric. |
| **[Pipeline](../api/v1alpha1/pipeline_types.go)** | An ordered chain of scheduling steps for one domain. Type `filter-weigher` (placement) or `detector` (descheduling). |
| **Filter** | A pipeline step that removes host candidates that cannot satisfy a request. Filters run sequentially. |
| **Weigher** | A pipeline step that scores the surviving host candidates. Weighers run in parallel and emit an activation. |
| **Detector** | A step in a `detector` pipeline that flags running workloads for descheduling. |
| **Activation** | A weigher's per-host output value; activations are aggregated into a host's final score as `weight + multiplier * tanh(activation)`. |
| **[Decision](../api/v1alpha1/decision_types.go)** | The transient object carrying one scheduling run's input, step activations, and ordered result. |
| **[Descheduling](../api/v1alpha1/descheduling_types.go)** | A CRD requesting that a running workload be moved off its current host. |
| **[History](../api/v1alpha1/history_types.go)** | A CRD recording the last (up to 10) scheduling decisions for a resource, for troubleshooting. |
| **[Delegation model](02-external-scheduler-api/01-the-scheduling-engine.md)** | The pattern by which the platform calls Cortex for a recommendation and remains the authority that executes it. Advisory today; fuller delegation planned. |
| **External scheduler** | Nova's hook that POSTs candidate hosts and weights to Cortex and receives a reordered host list. |
| **Forced destination** | A Nova request pinning specific hosts (`force_hosts`/`force_nodes`); Cortex returns them directly and skips the pipeline. |
| **[Placement API shim](03-reservations-and-inventory/05-placement-api-shim.md)** | The `shim` binary presenting an OpenStack Placement-API-compatible HTTP surface; passthrough today, KVM backend planned. |
| **Passthrough** | The shim's default per-endpoint behaviour: reverse-proxy the request unchanged to the upstream Placement API. |
| **KVM backend** | The shim's opt-in per-endpoint behaviour (via `features.*`) that would serve the endpoint from Cortex rather than forwarding. Not yet implemented. |
| **[Reservation](../api/v1alpha1/reservation_types.go)** | A CRD holding capacity on a specific host. Types: committed-resource, failover, in-flight. |
| **[CommittedResource](../api/v1alpha1/committed_resource_types.go)** | A CRD representing a customer capacity commitment (from Limes), which drives creation of committed-resource reservations. |
| **Committed-resource reservation** | A reservation backing a customer commitment. Memory commitments hold an actual host slot (an implicit CPU+RAM place); CPU-only commitments are an arithmetic headroom/billing check with no slot. |
| **Failover reservation** | Best-effort headroom, reserved after workloads exist and shared across them, sized to absorb a configurable number of simultaneous host failures so workloads can evacuate. |
| **In-flight reservation** | Capacity blocked pessimistically for a workload currently being scheduled, so concurrent requests are not offered the same slot (double-booking). |
| **[FlavorGroupCapacity](../api/v1alpha1/flavor_group_capacity_types.go)** | A CRD caching pre-computed capacity data per (flavor group × AZ). |
| **[ProjectQuota](../api/v1alpha1/project_quota_types.go)** | A CRD persisting per-project, per-AZ quota and usage pushed by Limes via LIQUID. |
| **Flavor group** | A named grouping of flavors that share a hardware version, used together for capacity and commitment accounting. |
| **Limes** | OpenStack's quota and commitment service; Cortex syncs commitments and quota from it. |
| **LIQUID** | The HTTP protocol Limes uses to exchange quota/capacity/usage data; Cortex exposes a LIQUID API for committed resources. |
| **[Home cluster](06-cortex-library/01-multicluster-client.md)** | The cluster running the Cortex pods and controllers. |
| **[Remote cluster](06-cortex-library/01-multicluster-client.md)** | A cluster that stores selected CRDs/resources for Cortex, addressed per-GVK and routed by matching labels (commonly the availability zone). |
| **Resource router** | The rule that maps an object to the remote cluster whose labels it matches. |
| **[Overlay cache](06-cortex-library/03-controller-runtime-cache.md)** | An in-process write-through overlay that makes just-written objects immediately visible, masking informer lag. |
| **Hypervisor CR** | The `kvm.cloud.sap/v1` `Hypervisor` custom resource Cortex consumes (and, for overcommit, updates); provided by the openstack-hypervisor-operator. |
| **IronCore Machine / MachinePool** | Bare-metal resources (`compute.ironcore.dev/v1alpha1`) Cortex consumes to schedule machines. |
| **Bundle** | A domain-specific Helm chart (e.g. `cortex-nova`) that stylizes the `cortex` library chart for one deployment. |
| **Library chart** | The generic `cortex` (or `cortex-shim`, `cortex-postgres`) Helm chart that bundles depend on. |
| **enabledControllers / enabledTasks** | Config lists selecting which controllers and periodic tasks the manager runs. |
| **Self-heal** | The shim's default supervisor mode: the controller-manager is rebuilt with backoff on failure while the REST API stays up. |

## Next

[« Back to the table of contents](readme.md)
