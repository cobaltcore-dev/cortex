<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Chapter 2 — The external scheduler API

Cortex's headline feature is smarter *initial placement*: when a platform is about to place a
workload, Cortex reorders the candidate hosts so the workload lands somewhere better. This chapter
covers that surface end to end.

It opens with the shared **scheduling engine** — the filter/weigher/detector model that every domain
reuses — because understanding it once means the per-domain pages can stay short. The following pages
then walk each domain Cortex schedules for: Nova (compute), Manila (shared file storage), Cinder
(block storage), Kubernetes pods, and IronCore (bare metal machines). Each domain page explains what
it consumes, which steps it ships, and how it hooks into the platform. The chapter closes with how to
add your own steps, extractors, and KPIs.

## In this chapter

1. [The scheduling engine](01-the-scheduling-engine.md) — filters, weighers, detectors, scoring, and the delegation contract.
2. [Nova smart scheduling](02-nova-smart-scheduling.md) — the external scheduler hook for OpenStack compute.
3. [Manila smart scheduling](03-manila-smart-scheduling.md) — placement for shared file storage.
4. [Cinder smart scheduling](04-cinder-smart-scheduling.md) — placement for block storage.
5. [Pod gang-scheduling for inference workloads](05-pod-gang-scheduling.md) — controller-driven pod placement.
6. [IronCore machine scheduling](06-ironcore-machine-scheduling.md) — bare-metal machine-pool assignment.
7. [Extend Cortex](07-extending-cortex.md) — register a new scheduling step, extractor, or KPI.

## Next

[Prev: Chapter 1 — Getting started](../01-getting-started/readme.md) · [Next: The scheduling engine »](01-the-scheduling-engine.md)
