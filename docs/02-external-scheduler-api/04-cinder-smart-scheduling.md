<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Cinder smart scheduling

Cinder is OpenStack's block storage service. Cortex schedules Cinder **volumes** as an external scheduler
over HTTP, exactly like Nova and Manila. It is the smallest domain of the three — today it ships no
filters and no weighers — so this page mainly documents the hook and what "no steps" means in practice.

## The external scheduler hook

Cinder calls Cortex at:

```
POST /scheduler/cinder/external
```

The handler (`internal/scheduling/cinder/external_scheduler_api.go`) receives candidate volume hosts and
their weights, runs the `cinder-external-scheduler` pipeline, and returns a re-ordered list. Each request
produces a `Decision` (generated name `cinder-…`) and stores the raw request. The scheduling unit is the
**volume host** — the storage backend a volume would land on.

## The steps it ships

Cinder currently registers **no filters and no weighers**. With no weighers configured, the engine takes
its no-weigher path: it preserves the incoming Cinder weights unchanged and returns the hosts in that
order (see the input-weight handling in [The scheduling engine](01-the-scheduling-engine.md)).

> [!NOTE]
> An empty step set means Cinder scheduling is a *pass-through* today — Cortex is wired in and producing
> `Decision`/`History` records, but not yet altering Cinder's ordering. The plumbing is in place so that
> adding block-storage-specific logic later is only a matter of implementing and registering steps
> ([Extend Cortex](07-extending-cortex.md)); the integration does not have to change.

## How this relates to Cortex

Cinder shows the integration cost of a new domain in its purest form: a route, a default pipeline name,
and the shared `Decision`/`History` machinery — with the actual placement intelligence added later as
plugins. It is the same engine as Nova and Manila with the plugin set empty. The next two domains break
from the HTTP pattern: pods and IronCore machines are scheduled by *watching* Kubernetes resources rather
than answering an HTTP call.

## Next

[Prev: Manila smart scheduling](03-manila-smart-scheduling.md) · [Next: Pod gang-scheduling »](05-pod-gang-scheduling.md)
