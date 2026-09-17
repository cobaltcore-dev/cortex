<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# CLI flags

Command-line flags for the two Cortex binaries. Flags cover server bind addresses, TLS, leader
election, and (shim only) supervisor and shim toggles. Functional configuration is not passed via
flags — see the [configuration reference](configuration.md).

## `manager` (`cmd/manager`)

| Flag | Type | Default | Description |
|---|---|---|---|
| `--metrics-bind-address` | string | `0` | Metrics endpoint bind address. `:8443` (HTTPS), `:8080` (HTTP), or `0` to disable. |
| `--metrics-secure` | bool | `true` | Serve metrics over HTTPS with authn/authz. |
| `--health-probe-bind-address` | string | `:8081` | Health/readiness probe bind address. |
| `--leader-elect` | bool | `false` | Enable leader election. |
| `--webhook-cert-path` | string | `""` | Directory holding the webhook TLS cert; enables a cert watcher when set. |
| `--webhook-cert-name` | string | `tls.crt` | Webhook cert file name. |
| `--webhook-cert-key` | string | `tls.key` | Webhook cert key file name. |
| `--metrics-cert-path` | string | `""` | Directory holding the metrics TLS cert. |
| `--metrics-cert-name` | string | `tls.crt` | Metrics cert file name. |
| `--metrics-cert-key` | string | `tls.key` | Metrics cert key file name. |
| `--enable-http2` | bool | `false` | Enable HTTP/2 (off by default as a CVE mitigation). |
| `--zap-*` | various | — | Standard controller-runtime zap logging flags. |

The manager's HTTP API server (external scheduler endpoints, LIQUID API) is served on a hardcoded
`:8080` and is not a flag. The log level is set by the `LOG_LEVEL` env var (`debug`/`info`/`warn`/
`error`, default `info`).

## `shim` (`cmd/shim`)

| Flag | Type | Default | Description |
|---|---|---|---|
| `--api-bind-address` | string | `:8080` | Shim REST API bind address. |
| `--metrics-bind-address` | string | `0` | Metrics endpoint bind address; `0` disables. |
| `--metrics-secure` | bool | `true` | Serve metrics over HTTPS. Rejected in self-heal mode when metrics are enabled. |
| `--health-probe-bind-address` | string | `:8081` | Health/readiness probe bind address. |
| `--leader-elect` | bool | `false` | **Must stay `false`** — the shim errors out if enabled. |
| `--placement-shim` | bool | `false` | Register the [Placement API shim](../concepts/placement-api-shim.md) handlers on the API server. |
| `--self-heal` | bool | `true` | Run the controller-manager under a supervisor that rebuilds it with backoff on failure while the REST API, probe, and metrics stay up. See below. |
| `--e2e-placement-shim` | bool | `false` | Run placement-shim e2e tests instead of the manager. |
| `--webhook-cert-*` / `--metrics-cert-*` | string | as above | Same TLS flags as the manager. |
| `--enable-http2` | bool | `false` | Enable HTTP/2. |
| `--zap-*` | various | — | Standard zap logging flags. |

### `--self-heal`

Default **on**. The controller-manager (cache + controllers) runs under a supervisor
(`pkg/shim/supervisor`) that rebuilds it with backoff if it fails, while the REST API, liveness
probe, and metrics endpoint run in a durable outer process that survives manager restarts. The pod
never crashes on apiserver/cache connectivity issues; a looping manager is surfaced via the
`cortex_placement_shim_manager_up` gauge.

> [!NOTE]
> In self-heal mode with metrics enabled, `--metrics-secure` and `--metrics-cert-path` are
> rejected: the outer metrics server is plain `promhttp` without TLS or authz. Set
> `--self-heal=false` for coupled mode, where a manager failure exits the process and the manager
> owns the probe, API, and metrics servers.

Bind addresses must be distinct: probe ≠ api, metrics ≠ api, metrics ≠ probe (metrics `0` is
exempt).

## Next steps

- Reference: [Configuration](configuration.md), [Metrics and alerts](metrics.md)
- Concept: [Placement API shim](../concepts/placement-api-shim.md)
