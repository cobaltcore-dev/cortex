<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# KPI

`cortex.cloud/v1alpha1`, Kind `KPI`, cluster-scoped. A KPI describes a key performance indicator
computed from [Datasources](datasource.md) and [Knowledges](knowledge.md) and exported as a
Prometheus metric. See [Configure KPIs](../../guides/configure-knowledge.md).

```bash
kubectl get kpis
```

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `schedulingDomain` | string | — | Domain this KPI serves. |
| `impl` | string | — | Name of the KPI implementation in Cortex. |
| `opts` | RawExtension | — | Additional configuration for the implementation. |
| `description` | string | — | Human-readable description. |
| `dependencies.datasources` | []ObjectReference | — | Datasources required; must share one database secret. |
| `dependencies.knowledges` | []ObjectReference | — | Knowledges required; must share one database secret. |

## Status

| Field | Type | Description |
|---|---|---|
| `readyDependencies` | int | How many dependencies are reconciled. |
| `totalDependencies` | int | Total dependencies configured. |
| `dependenciesReadyFrac` | string | e.g. `2/3 ready`, or `ready` if none configured. |
| `conditions` | []Condition | Includes `Ready`. |

## Next steps

- Concept: [The knowledge flow](../../concepts/overview.md)
- Guide: [Monitor Cortex](../../guides/monitor-cortex.md)
