<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# The Cortex Book

Cortex is a modular, extensible service for **smart placement and scheduling** in cloud-native
environments. It makes better placement decisions — for compute, storage, bare metal, and
Kubernetes pods — by continuously ingesting the state of the environment, distilling it into
reusable *knowledge*, and running that knowledge through configurable scheduling pipelines. Cortex
plugs into existing schedulers (chiefly OpenStack Nova) as an extra decision step rather than
replacing them.

This book is meant to be read **in order**, like a textbook. Early chapters assume no prior
knowledge of Cortex and build up the mental model; later chapters go feature by feature. Every page
ends with a **Next** link, so you can read the whole book front to back by following it. If you
already know what you are looking for, jump straight to the chapter. For exact fields, flags, and
metrics, read the code: the CRD types under `api/v1alpha1/`, the flag definitions in `cmd/manager`
and `cmd/shim`, and the Helm defaults under `helm/`.

> [!NOTE]
> Cortex is a **cloud-native Kubernetes operator**: everything it does is driven by custom resources in
> the API group `cortex.cloud/v1alpha1`, reconciled by controllers. Its behaviour is configured
> declaratively and observed through Kubernetes-native status and metrics — the reconcile-to-desired-state
> model applied to placement intelligence.

## Table of contents

### [1 — Getting started](01-getting-started/readme.md)

1. [What is Cortex?](01-getting-started/01-what-is-cortex.md)
2. [Architecture at a glance](01-getting-started/02-architecture-at-a-glance.md)
3. [CI/CD and packaging](01-getting-started/03-cicd-and-packaging.md)
4. [The Helm charts](01-getting-started/04-helm-charts.md)
5. [Make targets](01-getting-started/05-make-targets.md)
6. [Postgres and tooling](01-getting-started/06-postgres-and-tooling.md)
7. [Local development with Tilt](01-getting-started/07-local-development-with-tilt.md)
8. [Install a domain bundle](01-getting-started/08-installing-a-domain-bundle.md)

### [2 — The external scheduler API](02-external-scheduler-api/readme.md)

1. [The scheduling engine](02-external-scheduler-api/01-the-scheduling-engine.md)
2. [Nova smart scheduling](02-external-scheduler-api/02-nova-smart-scheduling.md)
3. [Manila smart scheduling](02-external-scheduler-api/03-manila-smart-scheduling.md)
4. [Cinder smart scheduling](02-external-scheduler-api/04-cinder-smart-scheduling.md)
5. [Pod gang-scheduling for inference workloads](02-external-scheduler-api/05-pod-gang-scheduling.md)
6. [IronCore machine scheduling](02-external-scheduler-api/06-ironcore-machine-scheduling.md)
7. [Extend Cortex](02-external-scheduler-api/07-extending-cortex.md)

### [3 — Reservations and inventory management](03-reservations-and-inventory/readme.md)

1. [Reservations overview](03-reservations-and-inventory/01-reservations-overview.md)
2. [Committed-resource reservations and Limes quota/capacity](03-reservations-and-inventory/02-committed-resource-reservations.md)
3. [Failover reservations for KVM HA](03-reservations-and-inventory/03-failover-reservations.md)
4. [Concurrency and in-flight reservations](03-reservations-and-inventory/04-concurrency-and-in-flight-reservations.md)
5. [The Placement API shim](03-reservations-and-inventory/05-placement-api-shim.md)

### [4 — The knowledge database](04-knowledge-database/readme.md)

1. [The knowledge flow](04-knowledge-database/01-knowledge-flow-overview.md)
2. [Datasources](04-knowledge-database/02-datasources.md)
3. [Feature extraction](04-knowledge-database/03-feature-extraction.md)
4. [KPIs and the Metrics API](04-knowledge-database/04-kpis-and-metrics-api.md)
5. [The infrastructure dashboard](04-knowledge-database/05-infrastructure-dashboard.md)
6. [Monitoring Cortex](04-knowledge-database/06-monitoring.md)

### [5 — Hypervisor lifecycle management](05-hypervisor-lifecycle/readme.md)

1. [Automated overcommit management](05-hypervisor-lifecycle/01-automated-overcommit.md)

### [6 — The Cortex library (`pkg/`)](06-cortex-library/readme.md)

1. [The multicluster client](06-cortex-library/01-multicluster-client.md)
2. [The controller-runtime client cache](06-cortex-library/02-controller-runtime-cache.md)
3. [The `conf` package](06-cortex-library/03-conf.md)
4. [The `keystone` package](06-cortex-library/04-keystone.md)
5. [The `sso` package](06-cortex-library/05-sso.md)
6. [The `monitoring` package](06-cortex-library/06-monitoring-package.md)
7. [The `resourcelock` package](06-cortex-library/07-resourcelock.md)
8. [The `task` package](06-cortex-library/08-task.md)
9. [The `supervisor` package](06-cortex-library/09-supervisor.md)

## Who this book is for

- **Operators** deploy and run Cortex — install per domain, configure via Helm values and CRDs, wire
  feature toggles, monitor, run multicluster, operate reservations. Read Chapter 1 (including
  [Install a domain bundle](01-getting-started/08-installing-a-domain-bundle.md)), then the feature
  chapters for your domain, and keep [Monitoring Cortex](04-knowledge-database/06-monitoring.md) nearby.
- **Integrators** wire Cortex into an existing scheduler — start with
  [The scheduling engine](02-external-scheduler-api/01-the-scheduling-engine.md) and the
  [Placement API shim](03-reservations-and-inventory/05-placement-api-shim.md).
- **Developers** extend Cortex — read Chapter 1, then
  [The Cortex library](06-cortex-library/readme.md) and
  [Extend Cortex](02-external-scheduler-api/07-extending-cortex.md).

## Next

[Start reading: Chapter 1 — Getting started »](01-getting-started/readme.md)
