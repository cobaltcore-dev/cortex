<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Configure shim endpoints

This guide deploys the Cortex placement shim and configures which Placement API endpoint groups it
proxies unchanged versus serves from Cortex, plus optional Keystone authentication. For the concept,
see [Placement API shim](../concepts/placement-api-shim.md).

## Before you begin

- A reachable upstream OpenStack Placement API.
- Keystone service credentials if you want auth.
- The `cortex-placement-shim` bundle available.

> [!IMPORTANT]
> Leader election must stay disabled for the shim — the binary errors out if `--leader-elect` is
> set. The chart does not expose a leader-election toggle. See [CLI flags](../reference/cli-flags.md).

## Step 1 — Point the shim at upstream Placement

Set the required `placementURL` and (for auth) `keystoneURL` under `global.conf`:

```yaml
# values.yaml
global:
  conf:
    placementURL: https://placement.example.com
    keystoneURL: https://keystone.example.com
```

Install:

```bash
helm install cortex-placement-shim helm/bundles/cortex-placement-shim --values values.yaml
```

Expected output:

```
NAME: cortex-placement-shim
STATUS: deployed
```

The shim starts with `--placement-shim` and `--self-heal` on by default. Verify it is serving:

```bash
kubectl get pods -l app.kubernetes.io/name=cortex-placement-shim
```

## Step 2 — Verify passthrough

With no `features.*` toggles set, every endpoint group proxies to upstream. Send a request through
the shim's API bind address (`:8080` by default) and confirm you get the upstream response:

```bash
kubectl port-forward deploy/cortex-placement-shim 8080:8080 &
curl -s http://localhost:8080/resource_providers -H "X-Auth-Token: $TOKEN" | head
```

Expected — the same JSON the upstream Placement API returns.

## Step 3 — (Optional) enable a KVM-backed endpoint group

Endpoint groups are switched from passthrough to the KVM backend individually:

```yaml
global:
  conf:
    features:
      resourceProviders: true
```

> [!WARNING]
> The KVM backend is not yet implemented. A toggle set to `true` currently returns
> `501 Not Implemented` for that endpoint group. Keep toggles at their default `false` for
> passthrough. See [Placement API shim](../concepts/placement-api-shim.md).

## Step 4 — (Optional) enable authentication

Add an `auth` block with ordered policies. Each policy matches a `METHOD /path` and lists allowed
roles (optionally project-scoped); first match wins, no match denies with `403`, an empty role list
is public.

```yaml
global:
  conf:
    auth:
      tokenCacheTTL: 5m
      policies:
        - pattern: "GET /resource_providers"
          roles:
            - name: reader
        - pattern: "PUT /resource_providers/*"
          roles:
            - name: admin
              projectScope: true
```

Supply the service credentials as secrets (rendered into `secrets.json`) — see
[Configuration](../reference/configuration.md). Verify an unauthorized call is rejected:

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/resource_providers
```

Expected:

```
403
```

## Troubleshooting

- **Pod up but requests fail** — confirm `placementURL` is reachable from the pod.
- **All requests 403** — check that a policy matches the method+path and that the token's roles
  match; unmatched requests deny by default.
- **Manager looping** — watch `cortex_placement_shim_manager_up`; self-heal keeps the API up while
  the manager rebuilds. See [CLI flags](../reference/cli-flags.md).

## Next steps

- Concept: [Placement API shim](../concepts/placement-api-shim.md), [Delegation model](../concepts/delegation-model.md)
- Reference: [Configuration](../reference/configuration.md), [CLI flags](../reference/cli-flags.md)
