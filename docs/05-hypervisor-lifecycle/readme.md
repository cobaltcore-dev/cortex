<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Chapter 5 — Hypervisor lifecycle management

Beyond deciding *where* workloads land, Cortex also helps manage the hosts themselves. This chapter
covers the lifecycle actions Cortex takes on hypervisors — starting with automated overcommit
management, which keeps each hypervisor's CPU and memory overcommit ratios in line with policy as its
role and hardware traits change.

## In this chapter

1. [Automated overcommit management](01-automated-overcommit.md) — driving overcommit ratios on `Hypervisor` CRs from a policy ConfigMap.

## Next

[Prev: Chapter 4 — The knowledge database](../04-knowledge-database/readme.md) · [Next: Automated overcommit management »](01-automated-overcommit.md)
