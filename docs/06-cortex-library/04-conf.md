<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# The `conf` package

Every Cortex binary needs configuration, and every binary needs it the same way: a non-secret block that
is safe to commit to a Helm chart, plus secret values injected separately. `pkg/conf` is the one place
that merge happens, so no component re-implements it.

## How it works

`conf` loads Cortex's file-based configuration and overlays secrets on top. The generic entry points read
the config struct a component asks for:

```go
// Load the config this component expects; panic if it cannot be read.
cfg := conf.GetConfigOrDie[nova.Config]()

// Or handle the error yourself.
cfg, err := conf.GetConfig[nova.Config]()
```

`GetConfig[C]()` reads `conf.json` and then overlays `secrets.json` on top, so a secret value overrides
the non-secret config of the same shape. The type parameter `C` lets each component ask for exactly the
config struct it expects — the Nova manager loads its config type, the shim loads its own — rather than
sharing one giant struct. In a deployment the two files are mounted from a ConfigMap and a Secret
respectively (`/etc/config/conf.json`, `/etc/secrets/secrets.json`).

## How this relates to Cortex

`conf` is the intake for nearly everything else in this chapter: the `apiservers` block the
[multicluster client](01-multicluster-client.md) routes on, the `cache.*` keys of the
[client cache](03-controller-runtime-cache.md), and the `HypervisorOvercommitConfig` of the
[overcommit controller](../05-hypervisor-lifecycle/01-automated-overcommit.md) are all loaded this way.
The config shapes themselves live beside the code that consumes them. The next package covers how a
datasource authenticates to OpenStack.

## Next

[Prev: The controller-runtime client cache](03-controller-runtime-cache.md) · [Next: The `keystone` package »](05-keystone.md)
