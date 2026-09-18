<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Chapter 4 — The knowledge database

Everything Cortex decides rests on *knowledge*: a continuously refreshed picture of the environment,
distilled from raw operational data into query-ready facts. This chapter explains where that data
comes from, how it becomes knowledge, and how it is published back out as metrics and dashboards.

The chapter opens with the end-to-end knowledge flow, then walks each stage: **datasources** that
cache OpenStack objects and Prometheus metrics into Postgres, **feature extraction** that turns those
raw rows into enriched features, **KPIs and the Metrics API** that publish knowledge as Prometheus
metrics, and the **infrastructure dashboard** that visualizes it for smart workload planning. It
closes with how to monitor Cortex itself.

## In this chapter

1. [The knowledge flow](01-knowledge-flow-overview.md) — datasource → feature → KPI, end to end.
2. [Datasources](02-datasources.md) — caching metrics and OpenStack objects into Postgres.
3. [Feature extraction](03-feature-extraction.md) — turning raw rows into features.
4. [KPIs and the Metrics API](04-kpis-and-metrics-api.md) — publishing knowledge as metrics.
5. [The infrastructure dashboard](05-infrastructure-dashboard.md) — visualizing the knowledge database.
6. [Monitoring Cortex](06-monitoring.md) — the metrics and alerts that watch Cortex itself.

## Next

[Prev: Chapter 3 — Reservations and inventory management](../03-reservations-and-inventory/readme.md) · [Next: The knowledge flow »](01-knowledge-flow-overview.md)
