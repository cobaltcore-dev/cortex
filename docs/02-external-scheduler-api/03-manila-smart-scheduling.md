<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Manila smart scheduling

Manila is OpenStack's shared file storage service. Cortex schedules Manila **shares** the same way it
schedules Nova instances — as an external scheduler over HTTP — but with a much smaller set of steps. This
page is short by design: if you have read [The scheduling engine](01-the-scheduling-engine.md) and
[Nova smart scheduling](02-nova-smart-scheduling.md), the only new things are the route and the one
weigher.

## The external scheduler hook

Manila calls Cortex at:

```
POST /scheduler/manila/external
```

The handler (`internal/scheduling/manila/external_scheduler_api.go`) receives candidate share hosts and
their weights, runs the `manila-external-scheduler` pipeline, and returns a re-ordered list. Each request
produces a `Decision` (generated name `manila-…`) and stores the raw request. The scheduling unit is the
**share host** — the backend a share would be placed on.

## The steps it ships

- **Filters:** none. Manila ships no filters today; every candidate host passes straight through to
  weighing.
- **Weighers:** one — `netapp_cpu_usage_balancing`, which balances placement by NetApp backend CPU usage,
  steering new shares toward less-loaded backends.

Because there is exactly one weigher, a Manila decision is essentially "reorder the candidates by NetApp
CPU load, respecting Manila's incoming weights."

> [!NOTE]
> The small step set is a reflection of where the domain is, not a limit of the engine. Manila uses the
> identical `lib` pipeline as Nova, so adding a filter or weigher is exactly the process in
> [Extend Cortex](07-extending-cortex.md) — implement the step, self-register it, and
> reference it from the `manila-external-scheduler` pipeline.

## How this relates to Cortex

Manila is a second consumer of the same HTTP external-scheduler pattern and the same `Decision`/`History`
machinery as Nova, proving the "write the engine once, reuse per domain" design from
[Architecture at a glance](../01-getting-started/02-architecture-at-a-glance.md). Its one weigher reads a
NetApp-usage feature from the [knowledge database](../04-knowledge-database/readme.md). Cinder, next, is
the same story with even fewer steps.

## Next

[Prev: Nova smart scheduling](02-nova-smart-scheduling.md) · [Next: Cinder smart scheduling »](04-cinder-smart-scheduling.md)
