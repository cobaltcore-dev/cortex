<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Cortex

Cortex is a modular, extensible service for **initial placement and scheduling** in cloud-native
environments. It makes smarter placement decisions — for compute, storage, network, and other
domains — by continuously ingesting the state of the environment, extracting knowledge from it,
and running that knowledge through configurable scheduling pipelines. Cortex plugs into existing
schedulers (chiefly OpenStack Nova) as an extra decision step rather than replacing them.

Cortex is a [Kubebuilder](https://book.kubebuilder.io/)-based Kubernetes operator: everything it
does is driven by custom resources (`cortex.cloud/v1alpha1`) reconciled by controllers, so its
behavior is configured declaratively and observed through Kubernetes-native status and metrics.

Cortex ships as three separately deployed components:

- **cortex core** — the `manager` binary and the `cortex` library Helm chart; runs the knowledge
  and scheduling controllers.
- **cortex-postgres** — the bundled Postgres storing ingested and enriched data.
- **cortex-shim** — the `shim` binary and the `cortex-shim` chart; an OpenStack
  Placement-API-compatible HTTP front.

## Where to go next

Pick the entry point that matches your intent.

| I want to… | Go to |
|---|---|
| **Understand** how Cortex works and why | [Concepts](concepts/overview.md) |
| **Install and operate** Cortex on a cluster | [Guides](guides/install-a-domain-bundle.md) |
| **Try it locally** end-to-end | [Tutorials](tutorials/local-development-with-tilt.md) |
| **Look up** an exact CRD field, config key, flag, or metric | [Reference](reference/crds/readme.md) |
| Learn a **domain term** | [Glossary](glossary.md) |

## Audiences

- **Operators** deploy and run Cortex — install per domain, configure via Helm values and CRDs,
  wire feature toggles, monitor, run multicluster, operate reservations. Start with the
  [guides](guides/install-a-domain-bundle.md).
- **Integrators** wire Cortex into an existing scheduler — the [Nova delegation
  model](concepts/delegation-model.md) and the [Placement API shim](concepts/placement-api-shim.md).
- **Developers** extend Cortex — see [Extend Cortex](guides/extend-cortex.md) and the
  [local dev tutorial](tutorials/local-development-with-tilt.md).
