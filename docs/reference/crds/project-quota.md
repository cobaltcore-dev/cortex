<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# ProjectQuota

`cortex.cloud/v1alpha1`, Kind `ProjectQuota`, cluster-scoped. A ProjectQuota persists quota and
usage for one OpenStack project in one AZ, populated from Limes via the LIQUID quota endpoint
(`PUT /v1/projects/:uuid/quota`). See [Operate reservations](../../guides/operate-reservations.md).

```bash
kubectl get projectquotas
```

## Spec

| Field | Type | Description |
|---|---|---|
| `projectID` | string | OpenStack project UUID. |
| `projectName` | string | Human-readable project name. |
| `domainID` | string | OpenStack domain UUID. |
| `domainName` | string | Human-readable domain name. |
| `availabilityZone` | string | AZ this quota covers (routes the CRD in multicluster). |
| `quota` | map[string]int64 | LIQUID resource name → per-AZ quota. |

## Status

| Field | Type | Description |
|---|---|---|
| `observedGeneration` | int64 | Last spec generation processed. |
| `totalUsage` | map[string]int64 | Total per-resource usage in this AZ. |
| `paygUsage` | map[string]int64 | Pay-as-you-go usage (`totalUsage - CRUsage`, clamped ≥ 0). |
| `totalUsageSummary` / `paygUsageSummary` | string | Compact summaries grouped by flavor group. |
| `limesUsageSummary` / `limesQuotaSummary` | string | Usage/quota converted to Limes declared units (debugging). |
| `lastReconcileAt` / `lastFullReconcileAt` | time | Last reconcile / last periodic full reconcile. |
| `conditions` | []Condition | Includes `Ready`. |

## Next steps

- Guide: [Operate committed-resource and failover reservations](../../guides/operate-reservations.md)
- Reference: [CommittedResource](committed-resource.md)
