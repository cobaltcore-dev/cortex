<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# What is Cortex?

Cortex is a Kubernetes operator that makes smarter placement decisions for cloud workloads. Instead
of scheduling with only the point-in-time facts a cloud platform's own scheduler sees, Cortex
continuously ingests operational data, distills it into reusable *knowledge*, and runs that knowledge
through configurable *pipelines* to influence where a workload lands — or to recommend moving one that
is already placed.

This page explains the problem Cortex solves, the components it ships as, and the single arc that
everything else in the book elaborates on. If you are impatient to run it, skip ahead to
[Local development with Tilt](08-local-development-with-tilt.md) and come back.

## Declarative by design

Cortex is a Kubernetes operator built **cloud-native**: its entire behaviour is modeled as custom
resources in the API group `cortex.cloud/v1alpha1`, reconciled by controllers. You do not call Cortex to
*perform* an action — you declare the *desired state* (which datasources to ingest, which pipelines to
run, which capacity to reserve) as Kubernetes objects, and the controllers continuously converge the
system toward it, reporting progress through resource `status`/conditions and Prometheus metrics.

This is deliberately the opposite of the world Cortex extends. OpenStack is **imperative and RPC-driven**:
you issue a request over an API and the service carries it out then and there. Cortex sits beside that
platform and adds a **declarative control plane** on top of it — the same reconcile-to-desired-state model
Kubernetes uses for pods, applied to placement intelligence. Every concept in the rest of this book —
`Datasource`, `Knowledge`, `Pipeline`, `Reservation`, `Decision` — is an instance of this one idea; they
are all cluster-scoped custom resources, catalogued in
[Architecture at a glance](02-architecture-at-a-glance.md#everything-is-a-cluster-scoped-custom-resource).

## Why a scheduling operator

A cloud platform's built-in scheduler (Nova for compute, Cinder for block storage, and so on) decides
placement from the state it can cheaply observe at request time. That state is narrow: it rarely
reflects historical load, cross-project commitments, hardware health trends, or capacity that is
promised but not yet consumed. Encoding that richer picture into each platform scheduler is hard and
couples policy to the platform.

There is also a *fragmentation* problem. In a typical OpenStack cloud, placement intelligence is
scattered across several independent schedulers — one inside Nova for compute, one inside Cinder for
block storage, one inside Manila for shares, network-locality logic in Neutron. Each has its own
extension mechanism, its own configuration surface, and its own copy of concerns that recur
everywhere: how to weigh load, how to respect capacity commitments, how to keep related workloads
together or apart. Improving placement means making the same change in several codebases, in several
different ways — and cross-service objectives (placing a VM near the storage it will attach, spreading
a tenant's resources across failure domains) have no single place to live at all.

Cortex takes a different stance: it treats *placement intelligence* as its own concern, deployed
beside the platform and shared across domains. The platform still owns the workload lifecycle — it
boots and tracks the workload — while Cortex owns the placement decision: depending on the domain it
either reorders the platform's candidates or selects the hosts itself, built from data the platform
does not track (see [Architecture at a glance](02-architecture-at-a-glance.md#advise-or-own--it-depends-on-the-hypervisor-type)).
Because the intelligence lives outside the platform, the same model serves
compute, storage, bare metal, and Kubernetes pods, and generic scheduling logic (load balancing,
anti-affinity) is written once and reused, while domain-specific logic is layered on top.

The alternative — keep improving each platform's *own* scheduler and glue the results together — was
weighed and set aside. A hybrid like that spreads the same concerns back across every service's extension
mechanism, gives cross-service objectives nowhere central to live, and leaves each improvement to be
re-implemented per platform. A single scheduling brain, external to the platforms, is what makes the
shared logic and the cross-domain view possible at all.

> [!NOTE]
> Consolidating the logic in one operator is what makes *cross-domain* placement possible in
> principle — reasoning about compute and storage together in a single decision. Today each domain is
> scheduled independently; joint cross-domain scheduling is a future direction, not current behaviour.

## The three components

Cortex is delivered as three separately deployed components:

- **cortex core** — the `manager` binary (`cmd/manager`), packaged through the `cortex` library chart
  and the per-domain bundles (`cortex-nova`, `cortex-cinder`, `cortex-manila`, `cortex-ironcore`,
  `cortex-pods`). This runs the controllers, the knowledge pipeline, and the external scheduler API.
- **cortex-postgres** — the datastore for ingested facts and derived knowledge, a custom Postgres
  image rendered by the `cortex-postgres` library chart. See
  [Postgres](06-postgres.md).
- **cortex-shim** — the `shim` binary (`cmd/shim`), packaged through the `cortex-shim` library and the
  `cortex-placement-shim` bundle, presenting an OpenStack Placement-API-compatible surface. See
  [The Placement API shim](../03-reservations-and-inventory/05-placement-api-shim.md).

The next page, [Architecture at a glance](02-architecture-at-a-glance.md), explains how one binary
serves every domain and how its custom resources and controllers fit together.

## Cortex in the CobaltCore ecosystem

Cortex is not a standalone product; it is the placement brain of a larger open-source platform.

- **CobaltCore** ([cobaltcore.dev](https://cobaltcore.dev/)) is the platform Cortex ships as part of —
  "infrastructure management for cloud-native and traditional workloads," pairing Kubernetes-native
  orchestration with OpenStack-compatible APIs (Nova, Neutron, Cinder, Keystone, Glance). Alongside
  Cortex it includes services such as a unified management frontend and an observability stack, all
  operated declaratively through operators and Helm charts. Cortex is the component that makes the
  platform's placement decisions smarter, using data the individual OpenStack schedulers do not track.
- **ApeiroRA** ([apeirora.eu](https://apeirora.eu/)), the Apeiro Reference Architecture, is the wider
  initiative CobaltCore belongs to: an open blueprint for a sovereign European **cloud-edge continuum**,
  developed as SAP's contribution to the IPCEI-CIS and governed for the long term under **NeoNephos**
  (part of the Linux Foundation Europe). Cortex's cloud-native, declarative approach is what lets it fit
  a construction-kit architecture built on open standards rather than proprietary lock-in.
- **Downstream services build on Cortex.** Because its scheduling is exposed as a reusable, declarative
  control plane, higher-level services delegate placement to it. **Thalamus**
  ([docs](https://cobaltcore-dev.github.io/thalamus/main/)) — a sovereign, Kubernetes-native LLM
  inference service — uses Cortex for orchestration, a concrete example of Cortex extending the platform
  well beyond OpenStack compute.

In short: Cortex reaches down into the imperative OpenStack world to gather state and influence
placement, and reaches up into the cloud-native CobaltCore/ApeiroRA ecosystem that consumes its
decisions.

## The end-to-end flow

Everything Cortex does follows one arc: raw facts become knowledge, knowledge feeds pipelines, and
pipelines emit decisions the platform acts on.

```mermaid
flowchart LR
    subgraph sources[Datasources]
        OS[OpenStack APIs]
        PM[Prometheus]
    end
    DB[(Postgres)]
    K[Knowledge extractors]
    subgraph pipe[Pipelines]
        FW[Filter-weigher pipeline]
        DET[Detector pipeline]
    end
    RES[Reservations / capacity]
    DEC[Decision / Descheduling]
    PLAT[Platform scheduler]

    OS --> DB
    PM --> DB
    DB --> K
    K --> FW
    K --> DET
    RES -->|reserved capacity| FW
    PLAT -->|placement request| FW
    FW -->|ordered hosts| PLAT
    FW --> DEC
    DET --> DEC
    K --> KPI[KPIs → Prometheus]
```

1. **Datasources** pull raw facts from OpenStack APIs and Prometheus into Postgres. See
   [Datasources](../04-knowledge-database/02-datasources.md).
2. **Knowledge extractors** turn those raw rows into features — the reusable, query-ready facts a
   pipeline step consumes. See [Feature extraction](../04-knowledge-database/03-feature-extraction.md).
3. **Reservations** hold capacity that is promised but not yet consumed — customer commitments,
   failover headroom, and in-flight placements — so a pipeline treats reserved space as unavailable
   even before a workload lands on it. See
   [Reservations overview](../03-reservations-and-inventory/01-reservations-overview.md).
4. **Filter-weigher pipelines** answer a live placement request: filters remove unsuitable hosts,
   weighers score the survivors, and the platform receives a re-ordered candidate list. See
   [The scheduling engine](../02-external-scheduler-api/01-the-scheduling-engine.md).
5. **Detector pipelines** run on a schedule to spot already-placed workloads that should move, emitting
   descheduling recommendations.
6. **KPIs** publish knowledge as Prometheus metrics for dashboards and alerting. See
   [KPIs and the Metrics API](../04-knowledge-database/04-kpis-and-metrics-api.md).

This arc *is* Cortex — every feature chapter in this book slots into one of its stages. Chapter 2
covers the pipelines and the scheduler API; Chapter 3 covers reservations and inventory; Chapter 4
covers the datasource-to-KPI knowledge database. Keep the diagram above in mind as a map: whenever a
later page introduces a component, locate it on the arc first.

## Next

[Prev: Chapter 1 — Getting started](readme.md) · [Next: Architecture at a glance »](02-architecture-at-a-glance.md)
