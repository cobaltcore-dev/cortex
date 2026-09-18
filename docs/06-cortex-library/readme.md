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

1. [The multicluster client](01-multicluster-client.md) — one `client.Client` across many clusters, routed per GVK, with a hands-on kind walkthrough.
2. [The controller-runtime client cache](02-controller-runtime-cache.md) — the write-through overlay with tombstones.
3. [The `conf` package](03-conf.md) — typed config loading with the `conf.json`/`secrets.json` overlay.
4. [The `keystone` package](04-keystone.md) — the Keystone connector behind every OpenStack datasource.
5. [The `sso` package](05-sso.md) — mTLS transports and outbound User-Agent tagging.
6. [The `monitoring` package](06-monitoring-package.md) — the registry wrapper and log-to-metric hooks.
7. [The `resourcelock` package](07-resourcelock.md) — short-lived Lease locks for serializing writes.
8. [The `task` package](08-task.md) — running periodic work as a controller.
9. [The `supervisor` package](09-supervisor.md) — decoupling the shim's request path from apiserver liveness.

## Next

[Prev: Chapter 5 — Hypervisor lifecycle management](../05-hypervisor-lifecycle/readme.md) · [Next: The multicluster client »](01-multicluster-client.md)
