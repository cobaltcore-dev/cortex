<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Local development with Tilt

This page walks you through running Cortex on a local Kubernetes cluster with [Tilt](https://tilt.dev),
which builds the images, installs the Helm bundles, and live-reloads your changes as you edit. By the
end you will have Cortex running locally and will have watched it pick up a code change — the inner
loop you will use for the rest of the book.

Every command is spelled out and safe to run on a throwaway cluster. It builds on the pieces from the
earlier pages in this chapter: the [Helm bundles](04-helm-charts.md) Tilt installs, and the
[manager image](03-cicd-and-packaging.md) it builds.

## What you will need

- A local Kubernetes cluster you can throw away — [kind](https://kind.sigs.k8s.io/) works well.
- `kubectl` pointed at that cluster.
- [Tilt](https://docs.tilt.dev/install.html) and Helm 3.
- A checkout of the Cortex repository, with your shell in its root.

## Step 1 — Create a cluster

Create a local cluster and confirm `kubectl` talks to it:

```bash
kind create cluster --name cortex-dev
kubectl cluster-info --context kind-cortex-dev
```

You should see the control plane URL printed. Leave this context active.

## Step 2 — Provide a Tilt values file

The `Tiltfile` requires the environment variable `TILT_VALUES_PATH` to point at a Helm values file;
it fails fast if the variable is unset or the file is missing. Create a minimal one:

```bash
cat > /tmp/cortex-dev-values.yaml <<'EOF'
# Minimal local values. Add datasource credentials and, per subchart,
# conf.enabledControllers for the domains you want to exercise; see the
# configuration reference.
global:
  conf: {}
EOF
export TILT_VALUES_PATH=/tmp/cortex-dev-values.yaml
```

You can layer environment-specific overrides two ways (both optional):

- Set `TILT_OVERRIDES_PATH` to a second values file that is merged on top (this is how the
  [multicluster walkthrough](../06-cortex-library/01-multicluster-client.md#walkthrough-multicluster-with-kind) injects remote clusters).
- Export `CORTEX_*` variables — `CORTEX_AAA_BBB_CCC=value` becomes the Helm override
  `aaa.bbb.ccc=value`. Setting `OS_REGION_NAME` additionally derives region-scoped OpenStack and
  Prometheus URLs.

## Step 3 — Choose which bundles to run

By default Tilt deploys every domain: `nova,manila,cinder,ironcore,pods,placement`. For a faster
loop, narrow it with `ACTIVE_DEPLOYMENTS`:

```bash
export ACTIVE_DEPLOYMENTS=nova
```

## Step 4 — Start Tilt

```bash
tilt up
```

Tilt opens a web UI (press `space`) and starts building. Watch the resources go green: the CRDs, the
manager image build, and the `cortex-nova-scheduling-controller-manager` deployment. When the manager
resource is green, Cortex is running against your cluster.

Verify from another terminal:

```bash
kubectl get pods
```

Expected — a running Cortex manager pod (and Postgres):

```
NAME                                                    READY   STATUS    RESTARTS   AGE
cortex-nova-scheduling-controller-manager-...           1/1     Running   0          1m
```

## Step 5 — Make a change and watch it reload

Edit any Go source under the manager's packages — for example, add a log line to a reconciler — and
save. Tilt detects the change, rebuilds the image, and redeploys automatically. In the Tilt UI the
manager resource turns yellow (building) then green (updated); your new log line appears in its logs:

```bash
kubectl logs deploy/cortex-nova-scheduling-controller-manager --follow
```

This is the core inner loop: edit, save, and let Tilt reconcile the running deployment.

## Step 6 — Clean up

Stop Tilt with `Ctrl-C`, then remove the cluster:

```bash
tilt down
kind delete cluster --name cortex-dev
```

## How this relates to Cortex

Tilt is just an orchestrator over the same artifacts the rest of the book describes: it runs the
[Make targets](05-make-targets.md) to build the manager, installs the [bundles](04-helm-charts.md)
you select with `ACTIVE_DEPLOYMENTS`, and brings up the [Postgres](06-postgres-and-tooling.md) they
depend on. Once you have this loop, you are ready to read how the scheduler actually makes decisions.
When you want to add a pipeline step, extractor, or KPI, see
[Extend Cortex](../02-external-scheduler-api/07-extending-cortex.md); for a multi-cluster local setup, the
[multicluster-with-kind walkthrough](../06-cortex-library/01-multicluster-client.md#walkthrough-multicluster-with-kind) stands up three
clusters with this same tooling.

## Next

[Prev: Postgres and tooling](06-postgres-and-tooling.md) · [Next: Install a domain bundle »](08-installing-a-domain-bundle.md)
