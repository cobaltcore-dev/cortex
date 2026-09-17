<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# CRD reference

Cortex owns the API group `cortex.cloud/v1alpha1`. All Cortex CRDs are **cluster-scoped**. The
authoritative source for these types is `PROJECT` and `api/v1alpha1/*_types.go`; this reference is
derived from them.

## Owned Kinds

| Kind | Purpose | Concept |
|---|---|---|
| [Datasource](datasource.md) | Sync external data (Prometheus / OpenStack) into Postgres | [Knowledge flow](../../concepts/overview.md) |
| [Knowledge](knowledge.md) | Extract enriched features from datasources | [Knowledge flow](../../concepts/overview.md) |
| [KPI](kpi.md) | Compute a key performance indicator as a metric | [Knowledge flow](../../concepts/overview.md) |
| [Pipeline](pipeline.md) | Ordered filter/weigher or detector steps for a domain | [Overview](../../concepts/overview.md) |
| [Decision](decision.md) | Transient carrier of one scheduling run's result | [Delegation model](../../concepts/delegation-model.md) |
| [Descheduling](descheduling.md) | Request to move a VM off its current host | [Overview](../../concepts/overview.md) |
| [Reservation](reservation.md) | Hold capacity on a host (committed / failover / in-flight) | [Reservations](../../guides/operate-reservations.md) |
| [CommittedResource](committed-resource.md) | A customer capacity commitment driving reservations | [Reservations](../../guides/operate-reservations.md) |
| [ProjectQuota](project-quota.md) | Per-project, per-AZ quota & usage from Limes | [Reservations](../../guides/operate-reservations.md) |
| [FlavorGroupCapacity](flavor-group-capacity.md) | Cached capacity per (flavor group × AZ) | [Reservations](../../guides/operate-reservations.md) |
| [History](history.md) | Record of past scheduling decisions | [Overview](../../concepts/overview.md) |

## Consumed, not owned

Cortex reads these CRDs but does not define them. They are provided by other operators and must be
installed separately.

| Kind | API group | Provided by |
|---|---|---|
| Hypervisor | `kvm.cloud.sap/v1` | [openstack-hypervisor-operator](https://github.com/cobaltcore-dev/openstack-hypervisor-operator) |
| Machine | `compute.ironcore.dev/v1alpha1` (wrapped as `api/external/ironcore/v1alpha1`) | IronCore |
| MachinePool | `compute.ironcore.dev/v1alpha1` | IronCore |
| MachineClass | `compute.ironcore.dev/v1alpha1` | IronCore |

> [!NOTE]
> The IronCore `Machine` type Cortex uses embeds the upstream `computev1alpha1.MachineSpec` and
> adds a `scheduler` field; when `scheduler: cortex`, Cortex assigns a machine pool if the pool
> reference is unset.

## Common fields

Every Kind carries a `schedulingDomain` on most specs, one of: `nova`, `cinder`, `manila`,
`machines`, `pods`. Every Kind exposes a `Ready` status condition. See the [Configure
datasources, knowledge, and KPIs](../../guides/configure-knowledge.md) guide for how to author
these resources, and the [CRD & controller model](../../concepts/crd-controller-model.md) concept
for how they are reconciled.
