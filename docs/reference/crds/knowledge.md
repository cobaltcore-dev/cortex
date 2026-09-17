<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Knowledge

`cortex.cloud/v1alpha1`, Kind `Knowledge`, cluster-scoped. A Knowledge describes a feature
extractor that turns raw [Datasource](datasource.md) rows into enriched, strongly-typed features
stored in its status. See [Configure knowledge](../../guides/configure-knowledge.md).

```bash
kubectl get knowledges
```

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `schedulingDomain` | string | — | Domain this knowledge serves. |
| `extractor.name` | string | — | Name of the extractor implementation in Cortex. |
| `extractor.config` | RawExtension | — | Extractor-specific configuration. |
| `recency` | duration | `60s` | How old the knowledge may be before re-extraction. |
| `description` | string | — | Human-readable description. |
| `dependencies.datasources` | []ObjectReference | — | Datasources required; all must share one database secret so they can be joined. |
| `dependencies.knowledges` | []ObjectReference | — | Other knowledges this one depends on. |

## Status

| Field | Type | Description |
|---|---|---|
| `lastExtracted` | time | Last successful extraction. |
| `lastContentChange` | time | When the extracted content last actually changed. |
| `raw` | RawExtension | The extracted data (e.g. a `{ "features": [...] }` list). |
| `rawLength` | int | Number of features, or 1 if not a list. |
| `conditions` | []Condition | Includes `Ready`. |

## Next steps

- Concept: [The knowledge flow](../../concepts/overview.md)
- Guide: [Extend Cortex](../../guides/extend-cortex.md) — write a knowledge extractor plugin.
