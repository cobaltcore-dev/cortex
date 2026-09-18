<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# The `monitoring` package

Every `cortex_` metric in the book is registered through one package. `pkg/monitoring` wraps the
Prometheus registry with Cortex's conventions and, distinctively, turns logging into metrics so a spike
in errors is visible on a dashboard, not just in a log stream.

## How it works

`monitoring` wraps the Prometheus registry (`NewRegistry`, `WrapRegistry`) with Cortex's conventions, so
every component registers its metrics the same way. It also bridges logs to metrics:

```go
reg := monitoring.NewRegistry()
// Count log messages by level as a metric.
monitor := monitoring.NewLogMetricsMonitor(reg)
logger := zap.New(monitoring.WrapCoreWithLogMetrics(core, monitor))
```

`NewLogMetricsMonitor` plus the `MetricsSlogHandler` / `WrapCoreWithLogMetrics` hooks count log messages
by level, so an error-level spike shows up as a metric you can alert on rather than a pattern you have to
notice in `kubectl logs`.

## How this relates to Cortex

`monitoring` is where the metrics of the whole book are born: the KPIs of
[Chapter 4](../04-knowledge-database/04-kpis-and-metrics-api.md), the sync and pipeline counters, and the
multicluster/overlay gauges all register through it, and the alerts of
[Monitoring Cortex](../04-knowledge-database/06-monitoring.md) fire on what it exposes. The metric names
themselves are declared next to the subsystem that emits them. The next package covers a different kind
of coordination: short-lived distributed locks.

## Next

[Prev: The `sso` package](06-sso.md) · [Next: The `resourcelock` package »](08-resourcelock.md)
