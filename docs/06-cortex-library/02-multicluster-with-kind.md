<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Multicluster with kind (walkthrough)

This walkthrough stands up a full Cortex multicluster environment on your laptop — a **home** cluster
running the controllers and two **remote** clusters (`az-a` and `az-b`) that own their own resources —
using three local [kind](https://kind.sigs.k8s.io/) clusters. You will then send a scheduling request
through Cortex and watch the resulting `History` land on the remote cluster that owns it. By the end you
will have seen home/remote routing work end to end.

This is a learning-oriented walkthrough on throwaway clusters. Every command is spelled out inline, along
with the YAML it applies — copy each block in order and read along as you go. For the concept behind what
you are building — and how a real deployment is wired up — see the previous page,
[The multicluster client](01-multicluster-client.md).

## What you will need

- [kind](https://kind.sigs.k8s.io/), `kubectl`, [Helm 3](https://helm.sh/docs/intro/install/),
  [Tilt](https://docs.tilt.dev/install.html), `curl`, and `python3` (for pretty-printing JSON).
- Docker with enough headroom for three small clusters.
- A checkout of the Cortex repository, with your shell in its root. Some commands use paths relative to
  the repo root, so run them from there.

## Step 1 — Create the environment

### Create the home cluster

The home cluster runs the controllers. Its API server is configured as an OIDC issuer so the remotes can
verify home-issued service-account tokens. Create it from this `kind` config:

```bash
kind create cluster --config - <<'EOF'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: cortex-home
nodes:
  - role: worker
  - role: control-plane
    extraPortMappings:
    - containerPort: 6443
      hostPort: 8443
    kubeadmConfigPatches:
      - |
        kind: ClusterConfiguration
        apiServer:
          extraArgs:
            service-account-issuer: "https://host.docker.internal:8443"
            service-account-jwks-uri: "https://host.docker.internal:8443/openid/v1/jwks"
          certSANs:
            - api-proxy
            - api-proxy.default.svc
            - api-proxy.default.svc.cluster.local
            - localhost
            - 127.0.0.1
            - host.docker.internal
EOF
```

Grant anonymous access to the service-account-issuer discovery endpoint so the remotes can fetch the home
cluster's OIDC configuration:

```bash
kubectl --context kind-cortex-home apply -f - <<'EOF'
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: grant-cortex-remote-oidc-access
subjects:
- kind: User
  apiGroup: rbac.authorization.k8s.io
  name: system:anonymous
roleRef:
  kind: ClusterRole
  name: system:service-account-issuer-discovery
  apiGroup: rbac.authorization.k8s.io
EOF
```

Store the home CA at `/tmp/root-ca-home.pem` so the remotes can verify home-issued tokens:

```bash
kubectl --context kind-cortex-home --namespace kube-system \
  get configmap extension-apiserver-authentication \
  -o jsonpath="{.data['client-ca-file']}" > /tmp/root-ca-home.pem
```

### Create the two remote clusters

Each remote is configured to trust the home cluster as an OIDC provider (`oidc-issuer-url`,
`oidc-client-id`) and mounts the home CA you just saved. The two configs differ only in their name and
host port (`8444` for `az-a`, `8445` for `az-b`).

```bash
kind create cluster --config - <<'EOF'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: cortex-remote-az-a
nodes:
  - role: control-plane
    extraPortMappings:
    - containerPort: 6443
      hostPort: 8444
    extraMounts:
      - hostPath: /tmp/root-ca-home.pem
        containerPath: /etc/ca-certificates/root-ca.pem
    kubeadmConfigPatches:
      - |
        kind: ClusterConfiguration
        apiServer:
          extraArgs:
            oidc-client-id: "https://host.docker.internal:8443" # = audience
            oidc-issuer-url: "https://host.docker.internal:8443"
            oidc-username-claim: sub
            oidc-ca-file: /etc/ca-certificates/root-ca.pem
          certSANs:
            - api-proxy
            - api-proxy.default.svc
            - api-proxy.default.svc.cluster.local
            - localhost
            - 127.0.0.1
            - host.docker.internal
EOF
```

```bash
kind create cluster --config - <<'EOF'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: cortex-remote-az-b
nodes:
  - role: control-plane
    extraPortMappings:
    - containerPort: 6443
      hostPort: 8445
    extraMounts:
      - hostPath: /tmp/root-ca-home.pem
        containerPath: /etc/ca-certificates/root-ca.pem
    kubeadmConfigPatches:
      - |
        kind: ClusterConfiguration
        apiServer:
          extraArgs:
            oidc-client-id: "https://host.docker.internal:8443" # = audience
            oidc-issuer-url: "https://host.docker.internal:8443"
            oidc-username-claim: sub
            oidc-ca-file: /etc/ca-certificates/root-ca.pem
          certSANs:
            - api-proxy
            - api-proxy.default.svc
            - api-proxy.default.svc.cluster.local
            - localhost
            - 127.0.0.1
            - host.docker.internal
EOF
```

Grant the home controllers' service-account tokens `cluster-admin` on each remote. The subject names
combine the home OIDC issuer with each controller's service account:

```bash
for CTX in kind-cortex-remote-az-a kind-cortex-remote-az-b; do
kubectl --context "$CTX" apply -f - <<'EOF'
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: grant-cortex-cluster-admin
subjects:
- kind: User
  apiGroup: rbac.authorization.k8s.io
  name: "https://host.docker.internal:8443#system:serviceaccount:default:cortex-nova-knowledge-controller-manager"
- kind: User
  apiGroup: rbac.authorization.k8s.io
  name: "https://host.docker.internal:8443#system:serviceaccount:default:cortex-nova-scheduling-controller-manager"
- kind: User
  apiGroup: rbac.authorization.k8s.io
  name: "https://host.docker.internal:8443#system:serviceaccount:default:cortex-ironcore-controller-manager"
- kind: User
  apiGroup: rbac.authorization.k8s.io
  name: "https://host.docker.internal:8443#system:serviceaccount:default:cortex-pods-controller-manager"
- kind: User
  apiGroup: rbac.authorization.k8s.io
  name: "https://host.docker.internal:8443#system:serviceaccount:default:cortex-placement-shim"
roleRef:
  kind: ClusterRole
  name: cluster-admin
  apiGroup: rbac.authorization.k8s.io
EOF
done
```

### Install the CRDs

Install the `cortex-crds` bundle on each remote:

```bash
kubectl config use-context kind-cortex-remote-az-a
helm install helm/bundles/cortex-crds --generate-name
kubectl config use-context kind-cortex-remote-az-b
helm install helm/bundles/cortex-crds --generate-name
```

Install the external `Hypervisor` CRD on all three clusters:

```bash
curl -L https://raw.githubusercontent.com/cobaltcore-dev/openstack-hypervisor-operator/refs/heads/main/charts/openstack-hypervisor-operator/crds/kvm.cloud.sap_hypervisors.yaml > /tmp/hypervisor-crd.yaml
kubectl --context kind-cortex-home apply -f /tmp/hypervisor-crd.yaml
kubectl --context kind-cortex-remote-az-a apply -f /tmp/hypervisor-crd.yaml
kubectl --context kind-cortex-remote-az-b apply -f /tmp/hypervisor-crd.yaml
```

### Configure routing and start Cortex

Store each remote's CA so the home controllers can verify the remote API servers:

```bash
kubectl --context kind-cortex-remote-az-a --namespace kube-system \
  get configmap extension-apiserver-authentication \
  -o jsonpath="{.data['client-ca-file']}" > /tmp/root-ca-remote-az-a.pem
kubectl --context kind-cortex-remote-az-b --namespace kube-system \
  get configmap extension-apiserver-authentication \
  -o jsonpath="{.data['client-ca-file']}" > /tmp/root-ca-remote-az-b.pem
```

Write a Tilt overrides file that declares the two remotes under `global.conf.apiservers.remotes` — each
with its `host`, `gvks`, routing `labels` (the AZ), and inlined `caCert`. The `caCert` values are spliced
in from the CA files you just saved:

```bash
export TILT_OVERRIDES_PATH=/tmp/cortex-values.yaml
tee $TILT_OVERRIDES_PATH <<EOF
global:
  conf:
    apiservers:
      remotes:
      - host: https://host.docker.internal:8444
        gvks:
        - kvm.cloud.sap/v1/Hypervisor
        - kvm.cloud.sap/v1/HypervisorList
        - cortex.cloud/v1alpha1/History
        - cortex.cloud/v1alpha1/HistoryList
        labels:
          availabilityZone: cortex-remote-az-a
        caCert: |
$(cat /tmp/root-ca-remote-az-a.pem | sed 's/^/          /')
      - host: https://host.docker.internal:8445
        gvks:
        - kvm.cloud.sap/v1/Hypervisor
        - kvm.cloud.sap/v1/HypervisorList
        - cortex.cloud/v1alpha1/History
        - cortex.cloud/v1alpha1/HistoryList
        labels:
          availabilityZone: cortex-remote-az-b
        caCert: |
$(cat /tmp/root-ca-remote-az-b.pem | sed 's/^/          /')
EOF
```

> [!NOTE]
> The overrides use `global.conf`, which is a Helm *global* — it merges into every manager subchart's
> configuration. This is why one `apiservers` block configures the whole deployment. The `apiservers`
> config shape is the `conf` struct loaded via `pkg/conf`.

Seed sample hypervisors into the remotes. Each remote gets two hypervisors labelled with its own zone:

```bash
kubectl --context kind-cortex-remote-az-a apply -f - <<'EOF'
apiVersion: kvm.cloud.sap/v1
kind: Hypervisor
metadata:
  name: hypervisor-1-az-a
  labels:
    topology.kubernetes.io/zone: cortex-remote-az-a
---
apiVersion: kvm.cloud.sap/v1
kind: Hypervisor
metadata:
  name: hypervisor-2-az-a
  labels:
    topology.kubernetes.io/zone: cortex-remote-az-a
EOF

kubectl --context kind-cortex-remote-az-b apply -f - <<'EOF'
apiVersion: kvm.cloud.sap/v1
kind: Hypervisor
metadata:
  name: hypervisor-1-az-b
  labels:
    topology.kubernetes.io/zone: cortex-remote-az-b
---
apiVersion: kvm.cloud.sap/v1
kind: Hypervisor
metadata:
  name: hypervisor-2-az-b
  labels:
    topology.kubernetes.io/zone: cortex-remote-az-b
EOF
```

Start Cortex in the home cluster with Tilt, using the overrides you exported:

```bash
kubectl config use-context kind-cortex-home
tilt up
```

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

With Tilt still up, work through the following in another terminal.

### Apply the test pipeline

This is a minimal `filter-weigher` pipeline with `createHistory: true` and a single `filter_correct_az`
step (no weighers, so surviving hosts keep their input order):

```bash
kubectl --context kind-cortex-home apply -f - <<'EOF'
apiVersion: cortex.cloud/v1alpha1
kind: Pipeline
metadata:
  name: multicluster-test
spec:
  schedulingDomain: nova
  description: Minimal test pipeline for the multicluster guide.
  type: filter-weigher
  createHistory: true
  filters:
    - name: filter_correct_az
  weighers: []
EOF
```

## Step 3 — Clean up

Tear everything down — the three kind clusters and the temporary CA and override files under `/tmp`:

```bash
kind delete cluster --name cortex-home
kind delete cluster --name cortex-remote-az-a
kind delete cluster --name cortex-remote-az-b
rm -f /tmp/root-ca-home.pem \
    /tmp/root-ca-remote-az-a.pem \
    /tmp/root-ca-remote-az-b.pem \
    /tmp/cortex-values.yaml \
    /tmp/hypervisor-crd.yaml
```

## What you learned

- A Cortex deployment has one **home** cluster (controllers) and any number of **remote** clusters that
  physically own routed resources.
- Remotes trust the home cluster by verifying its service-account tokens against the home OIDC issuer; no
  external identity provider is involved.
- Routing is declared once under `apiservers` (GVKs, `labels`, `caCert` per remote). **Writes** go to the
  single remote whose labels match; **reads** fan out and merge.
- You watched a `History` for an `az-b` request land on the `az-b` remote automatically.

## How this relates to Cortex

This walkthrough is the hands-on companion to
[The multicluster client](01-multicluster-client.md): the routing, OIDC trust, and
`global.conf.apiservers` block you configured here are exactly the mechanisms that page describes, run on
throwaway clusters so you can watch them work. The next page returns to the library with the second big
piece of `pkg/`: the controller-runtime client cache.

## Next

[Prev: The multicluster client](01-multicluster-client.md) · [Next: The controller-runtime client cache »](03-controller-runtime-cache.md)
