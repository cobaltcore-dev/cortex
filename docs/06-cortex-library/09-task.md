<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# The `task` package

Some Cortex work is periodic rather than event-driven — a detector pipeline that runs on an interval, a
sync that fires on a schedule. Rather than spawn loose goroutines with their own lifecycle, `pkg/task`
runs periodic work *as a controller*, so it lives and dies with the controller-runtime manager.

## How it works

`task.Runner` runs a periodic task as a controller: it watches for generic events and triggers the task
on each, so a scheduled job is integrated with the manager lifecycle rather than being a detached
goroutine. You give it a `Name`, an `Interval`, an optional `Init`, and a `Run` function:

```go
runner := &task.Runner{
    Name:     "my-periodic-task",
    Interval: 30 * time.Second,
    Run:      func(ctx context.Context) error { /* do the work */ },
}
// registered with the manager like any other controller
```

Because it is a controller, it participates in leader election, graceful shutdown, and metrics the same
way every other controller does — there is no separate lifecycle to reason about.

## How this relates to Cortex

`task.Runner` is the machinery behind the detector pipelines of
[Chapter 2](../02-external-scheduler-api/01-the-scheduling-engine.md) and other scheduled work selected
by `enabledTasks`. It is why a periodic job in Cortex is observable and shuts down cleanly like the rest
of the manager. The final package in this chapter is the one that keeps the placement shim's request path
alive independently of the manager.

## Next

[Prev: The `resourcelock` package](08-resourcelock.md) · [Next: The `supervisor` package »](10-supervisor.md)
