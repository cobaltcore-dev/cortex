<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Chapter 6 — The Cortex library (`pkg/`)

Not everything in Cortex is a controller. The `pkg/` folder holds reusable, importable library code —
building blocks written so that other services (and other parts of Cortex) can depend on them without
pulling in the whole operator. This chapter documents the pieces most worth understanding.

The chapter opens with the **multicluster client**, which lets one control plane act across several
Kubernetes clusters as if they were one, and a hands-on walkthrough of standing that up with kind.
It then covers the **controller-runtime client cache**, an overlay that hides informer lag during
tight reconcile loops, and finishes with a page per supporting package — configuration loading,
Keystone, SSO, monitoring, leader-election locks, periodic tasks, and the shim supervisor that keeps
the Placement API shim serving even when its controller-manager cannot.

## In this chapter

1. [The multicluster client](01-multicluster-client.md) — one `client.Client` across many clusters, routed per GVK.
2. [Multicluster with kind (walkthrough)](02-multicluster-with-kind.md) — a hands-on multi-cluster setup on your laptop.
3. [The controller-runtime client cache](03-controller-runtime-cache.md) — the write-through overlay with tombstones.
4. [The `conf` package](04-conf.md) — typed config loading with the `conf.json`/`secrets.json` overlay.
5. [The `keystone` package](05-keystone.md) — the Keystone connector behind every OpenStack datasource.
6. [The `sso` package](06-sso.md) — mTLS transports and outbound User-Agent tagging.
7. [The `monitoring` package](07-monitoring-package.md) — the registry wrapper and log-to-metric hooks.
8. [The `resourcelock` package](08-resourcelock.md) — short-lived Lease locks for serializing writes.
9. [The `task` package](09-task.md) — running periodic work as a controller.
10. [The `supervisor` package](10-supervisor.md) — decoupling the shim's request path from apiserver liveness.

## Next

[Prev: Chapter 5 — Hypervisor lifecycle management](../05-hypervisor-lifecycle/readme.md) · [Next: The multicluster client »](01-multicluster-client.md)
