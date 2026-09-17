<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Multicluster with kind

In this tutorial you will stand up a full Cortex multicluster environment on your laptop — a **home**
cluster running the controllers and two **remote** clusters (`az-a` and `az-b`) that own their own
resources — using three local [kind](https://kind.sigs.k8s.io/) clusters. You will then send a
scheduling request through Cortex and watch the resulting `History` land on the remote cluster that
owns it. By the end you will have seen home/remote routing work end to end.

This is a learning-oriented walkthrough on throwaway clusters. Every step is scripted; you run one
script per phase and read along as it narrates what it does. For the concept behind what you are
building — and how a real deployment is wired up — see [Multicluster](../concepts/multicluster.md).

## What you will need

- [kind](https://kind.sigs.k8s.io/), `kubectl`, [Helm 3](https://helm.sh/docs/intro/install/),
  [Tilt](https://docs.tilt.dev/install.html), `curl`, and `python3` (for pretty-printing JSON).
- Docker with enough headroom for three small clusters.
- A checkout of the Cortex repository, with your shell in its root. All scripts use paths relative to
  the repo root, so run them from there.

The scripts live alongside this tutorial in [`multicluster/`](multicluster/):

| File | Role |
|---|---|
| `run.sh` | Create the three clusters, wire up trust, and start Cortex under Tilt. |
| `schedule.sh` | Send a scheduling request and inspect the resulting `History`. |
| `cleanup.sh` | Delete the clusters and temporary files. |
| `cortex-home.yaml`, `cortex-remote-az-{a,b}.yaml` | `kind` cluster definitions. |
| `cortex-home-crb.yaml`, `cortex-remote-crb.yaml` | RBAC that lets the home cluster reach the remotes. |
| `hypervisors-az-{a,b}.yaml` | Sample `Hypervisor` resources placed in each remote. |
| `test-pipeline.yaml` | A minimal filter-weigher pipeline used by `schedule.sh`. |

## Step 1 — Create the environment

Run the setup script from the repo root:

```bash
./docs/tutorials/multicluster/run.sh
```

It performs the whole bring-up and narrates each part. In order, it:

1. **Creates the home cluster** (`kind-cortex-home`) and applies `cortex-home-crb.yaml`, which grants
   discovery on the home cluster's service-account issuer.
2. **Stores the home CA** at `/tmp/root-ca-home.pem` so the remotes can verify home-issued tokens.
3. **Creates the two remote clusters** (`kind-cortex-remote-az-a` and `-az-b`) and applies
   `cortex-remote-crb.yaml` to each, letting home service-account tokens act on the remote.
4. **Installs the `cortex-crds` bundle** on each remote and the external `Hypervisor` CRD on all three
   clusters.
5. **Stores each remote's CA** at `/tmp/root-ca-remote-az-{a,b}.pem`.
6. **Writes a Tilt overrides file** to `/tmp/cortex-values.yaml` (exported as `TILT_OVERRIDES_PATH`)
   that declares the two remotes under `global.conf.apiservers.remotes` — each with its `host`,
   `gvks`, routing `labels` (the AZ), and inlined `caCert`.
7. **Seeds sample hypervisors** into the remotes from `hypervisors-az-{a,b}.yaml`.
8. **Starts Cortex** in the home cluster with `tilt up`.

> [!NOTE]
> The overrides use `global.conf`, which is a Helm *global* — it merges into every manager subchart's
> configuration. This is why one `apiservers` block configures the whole deployment. See
> [Configuration](../reference/configuration.md).

Leave Tilt running. When its resources go green, the home cluster's controllers are live and aware of
both remotes. In another terminal, confirm the routing was picked up:

```bash
kubectl --context kind-cortex-home logs deploy/cortex-nova-scheduling-controller-manager \
  | grep "adding remote cluster"
```

Expected — one line per GVK per remote:

```
INFO  adding remote cluster for resource  {"gvk": "kvm.cloud.sap/v1, Kind=Hypervisor", "host": "https://host.docker.internal:8444", "labels": {"availabilityZone":"cortex-remote-az-a"}}
```

## Step 2 — Send a scheduling request

With Tilt still up, run the scheduling walkthrough in another terminal:

```bash
./docs/tutorials/multicluster/schedule.sh
```

It pauses between phases (press Enter to advance) so you can read each result. It:

1. **Applies `test-pipeline.yaml`** — a minimal `filter-weigher` pipeline with `createHistory: true`
   and a single `filter_correct_az` step (no weighers, so surviving hosts keep their input order).
2. **Sends a Nova external-scheduler request** to `http://localhost:8001/scheduler/nova/external`
   with four candidate hosts — `hypervisor-{1,2}-az-{a,b}` — and availability zone
   `cortex-remote-az-b`. Because `filter_correct_az` drops hosts that do not match the requested AZ,
   you should get back only the two `az-b` hosts.
3. **Lists `histories` and scheduling events across all three clusters**. The pipeline created a
   `History`, and — because history for an `az-b` workload is routed there — it appears on
   `kind-cortex-remote-az-b`, **not** on the home cluster.
4. **Describes the `History`**, showing the chosen host, the per-step explanation, and the `Ready`
   condition.

The key observation is in Step 3: you never told `kubectl` which cluster holds the `History`, yet
Cortex wrote it to the remote whose labels matched the request. That is the resource router doing its
job. Contrast a read that fans out (listing `histories` on every context finds it in exactly one)
with the write that went to a single owner.

## Step 3 — Clean up

Tear everything down:

```bash
./docs/tutorials/multicluster/cleanup.sh
```

This deletes the three kind clusters and removes the temporary CA and override files under `/tmp`.

## What you learned

- A Cortex deployment has one **home** cluster (controllers) and any number of **remote** clusters
  that physically own routed resources.
- Remotes trust the home cluster by verifying its service-account tokens against the home OIDC issuer;
  no external identity provider is involved.
- Routing is declared once under `apiservers` (GVKs, `labels`, `caCert` per remote). **Writes** go to
  the single remote whose labels match; **reads** fan out and merge.
- You watched a `History` for an `az-b` request land on the `az-b` remote automatically.

## Next steps

- Concept: [Multicluster](../concepts/multicluster.md) — the trust model, routing, and how a real
  deployment is wired up.
- Tutorial: [Local development with Tilt](local-development-with-tilt.md)
