<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# The `resourcelock` package

When several replicas of a controller may write the same shared resource, their writes need serializing.
Full leader election is the wrong tool for a single write — it is heavyweight and long-lived.
`pkg/resourcelock` provides the lightweight alternative: a lock held only for the duration of one write.

## How it works

`resourcelock` provides distributed locking over `coordination.k8s.io/v1` **Lease** objects. Unlike a
leader-election library, these locks are short-lived — held for milliseconds during a single write and
not renewed in the background — which is enough to serialize concurrent writes to a shared resource
(such as a ConfigMap) across replicas:

```go
rl := resourcelock.NewResourceLocker(client, "my-namespace")
if err := rl.AcquireLock(ctx, "my-lock", "holder-abc"); err != nil { /* ... */ }
defer rl.ReleaseLock(ctx, "my-lock", "holder-abc")
```

Because the lock is acquired and released around a single critical section, there is no background lease
renewal to manage and no leader to fail over — the lock exists only while a write is in flight.

## How this relates to Cortex

`resourcelock` is what lets Cortex run multiple replicas without them clobbering each other's writes to a
shared object. It is deliberately small and importable on its own. The next package covers the other
piece of in-manager machinery: running periodic work as a controller.

## Next

[Prev: The `monitoring` package](06-monitoring-package.md) · [Next: The `task` package »](08-task.md)
