<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Glossary

One-line definitions of the domain terms used across the Cortex documentation. Terms link to the
concept or reference page where they are explained in depth.

| Term | Definition |
|---|---|
| **Scheduling domain** | A logical area Cortex schedules for: `nova`, `cinder`, `manila`, `machines` (IronCore), or `pods`. Set on most CRDs via `schedulingDomain`. |
| **[Datasource](reference/crds/datasource.md)** | A CRD describing an external source (Prometheus or OpenStack) that Cortex periodically syncs into its Postgres store. |
| **[Knowledge](reference/crds/knowledge.md)** | A CRD describing a feature extractor that turns raw datasource rows into enriched, strongly-typed features. |
| **[KPI](reference/crds/kpi.md)** | A CRD describing a key performance indicator computed from datasources/knowledges and exported as a metric. |
| **Feature** | A single enriched datum produced by a knowledge extractor and consumed by scheduling steps. |
| **[Pipeline](reference/crds/pipeline.md)** | An ordered chain of scheduling steps for one domain. Type `filter-weigher` (placement) or `detector` (descheduling). |
| **Filter** | A pipeline step that removes host candidates that cannot satisfy a request. |
| **Weigher** | A pipeline step that scores the surviving host candidates; scores are aggregated to order hosts. |
| **Detector** | A step in a `detector` pipeline that flags running workloads for descheduling. |
| **[Decision](reference/crds/decision.md)** | The transient object carrying one scheduling run's input, step activations, and ordered result. Not persisted to etcd. |
| **Activation** | A step's per-host output value; activations across steps are aggregated into a host's final weight. |
| **[History](reference/crds/history.md)** | A CRD recording the last (up to 10) scheduling decisions for a resource, for troubleshooting. |
| **[Descheduling](reference/crds/descheduling.md)** | A CRD requesting that a running VM be moved off its current host. |
| **[Delegation model](concepts/delegation-model.md)** | The pattern by which Nova calls Cortex as an external scheduling step over HTTP. |
| **External scheduler** | Nova's hook that POSTs candidate hosts and weights to Cortex and receives a reordered host list. |
| **Forced destination** | A Nova request pinning specific hosts (`force_hosts`/`force_nodes`); Cortex returns them directly and skips the pipeline. |
| **[Placement API shim](concepts/placement-api-shim.md)** | The `shim` binary presenting an OpenStack Placement-API-compatible HTTP surface; passthrough today, KVM backend planned. |
| **Passthrough** | The shim's default per-endpoint behavior: reverse-proxy the request unchanged to the upstream Placement API. |
| **KVM backend** | The shim's opt-in per-endpoint behavior (via `features.*`) that serves the endpoint from Cortex rather than forwarding. Not yet implemented. |
| **[Home cluster](concepts/multicluster.md)** | The cluster running the Cortex pods and controllers. |
| **[Remote cluster](concepts/multicluster.md)** | A cluster that stores selected CRDs/resources for Cortex, addressed per-GVK and routed by availability zone. |
| **Resource router** | The rule that maps an object to the remote cluster serving its availability zone. |
| **[Pending-cache overlay](concepts/pending-cache-overlay.md)** | An in-process write-through overlay that makes just-written objects immediately visible, masking informer lag. |
| **[Reservation](reference/crds/reservation.md)** | A CRD holding capacity on a specific host. Types: committed-resource, failover, in-flight. |
| **[CommittedResource](reference/crds/committed-resource.md)** | A CRD representing a customer capacity commitment (from Limes), which drives creation of committed-resource reservations. |
| **Committed-resource reservation** | A reservation slot backing a confirmed/guaranteed commitment. |
| **Failover reservation** | A reservation pre-holding empty capacity so specific VMs can evacuate on host failure. |
| **In-flight reservation** | A reservation blocking capacity for a VM currently being scheduled, to avoid double-booking. |
| **[FlavorGroupCapacity](reference/crds/flavor-group-capacity.md)** | A CRD caching pre-computed capacity data per (flavor group × AZ). |
| **[ProjectQuota](reference/crds/project-quota.md)** | A CRD persisting per-project, per-AZ quota and usage pushed by Limes via LIQUID. |
| **Flavor group** | A named grouping of Nova flavors (e.g. `hana-v2`) used by capacity and commitment accounting. |
| **Bundle** | A domain-specific Helm chart (e.g. `cortex-nova`) that stylizes the `cortex` library chart for one deployment. |
| **Library chart** | The generic `cortex` Helm chart that bundles depend on. |
| **enabledControllers / enabledTasks** | Config lists selecting which controllers and periodic tasks the manager runs. |
| **Self-heal** | The shim's default supervisor mode: the controller-manager is rebuilt with backoff on failure while the REST API stays up. |
