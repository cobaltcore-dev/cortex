<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# The `sso` package

Cortex makes a lot of outbound HTTP calls — to OpenStack services, to Prometheus, to remote apiservers.
`pkg/sso` owns two cross-cutting concerns for those calls: mutual-TLS single sign-on, and a consistent
outbound User-Agent so operators can attribute traffic to a specific Cortex build.

## How it works

`sso` builds HTTP clients and transports for mutual-TLS single sign-on from an `SSOConfig`:

```go
transport, err := sso.NewTransport(ssoConfig)   // mTLS *http.Transport
client, err := sso.NewHTTPClient(ssoConfig)     // *http.Client using it
```

It also owns the outbound **User-Agent**. `SetUserAgent(component, version)` sets a string like
`cortex-nova/sha-70af93a8`, and `WrapUserAgent` decorates any `http.RoundTripper` to send it:

```go
sso.SetUserAgent("cortex-nova", version)
rt := sso.WrapUserAgent(base)   // every request now carries the User-Agent
```

so upstream services can tell which Cortex build a request came from — invaluable when several versions
run side by side during a rollout.

`sso` is the transport under the [`keystone`](04-keystone.md) sessions and other outbound calls: where
`keystone` authenticates, `sso` carries the bytes and labels them. The User-Agent it stamps is the same
build identity that shows up in logs and metrics. The next page covers where those metrics are
registered.

## Next

[Prev: The `keystone` package](04-keystone.md) · [Next: The `monitoring` package »](06-monitoring-package.md)
