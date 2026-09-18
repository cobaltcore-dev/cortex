<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# The `keystone` package

Cortex's OpenStack datasources talk to Nova, Cinder, Manila, and the rest, and every one of those calls
must be authenticated against Keystone. `pkg/keystone` is the single place that turns a Kubernetes secret
into an authenticated OpenStack session.

## How it works

`keystone` initializes an OpenStack connection from a Kubernetes secret. Its `Connector` reads a secret
reference, builds an authenticated Keystone session, and exposes it through the `KeystoneClient`
interface:

```go
kc, err := connector.FromSecretRef(ctx, corev1.SecretReference{
    Name:      "cortex-nova-openstack-keystone",
    Namespace: "cortex",
})
// kc is a KeystoneClient — hand it to a datasource syncer.
```

The secret carries the usual OpenStack credentials (auth URL, username, project/domain scope, password).
Because the `Connector` is the one entry point, credential handling and session construction are uniform
across every OpenStack datasource kind.

## How this relates to Cortex

`keystone` is the doorway the OpenStack [datasources](../04-knowledge-database/02-datasources.md) use to
reach the services they cache — it is what a Nova or Cinder syncer holds to make its authenticated calls.
It pairs with [`sso`](05-sso.md), which owns the outbound HTTP transport and User-Agent for those calls.
The next page covers that transport layer.

## Next

[Prev: The `conf` package](03-conf.md) · [Next: The `sso` package »](05-sso.md)
