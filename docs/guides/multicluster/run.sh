#!/bin/bash

set -e

echo "Creating home cluster"
kind create cluster --config docs/guides/multicluster/cortex-home.yaml

echo "Applying cluster role binding for oidc endpoint access"
kubectl --context kind-cortex-home apply -f docs/guides/multicluster/cortex-home-crb.yaml

echo "Storing home cluster cert under /tmp/root-ca-home.pem"
kubectl --context kind-cortex-home --namespace kube-system \
  get configmap extension-apiserver-authentication \
  -o jsonpath="{.data['client-ca-file']}" > /tmp/root-ca-home.pem

echo "Creating az-a and az-b clusters"
kind create cluster --config docs/guides/multicluster/cortex-remote-az-a.yaml
kind create cluster --config docs/guides/multicluster/cortex-remote-az-b.yaml

echo "Granting cortex-home sa tokens access to az-a and az-b clusters"
kubectl --context kind-cortex-remote-az-a apply -f docs/guides/multicluster/cortex-remote-crb.yaml
kubectl --context kind-cortex-remote-az-b apply -f docs/guides/multicluster/cortex-remote-crb.yaml

echo "Installing cortex crds in az-a and az-b clusters"
kubectl config use-context kind-cortex-remote-az-a
helm install helm/bundles/cortex-crds --generate-name
kubectl config use-context kind-cortex-remote-az-b
helm install helm/bundles/cortex-crds --generate-name

echo "Installing hypervisor crd as external dependency to all three clusters"
curl -L https://raw.githubusercontent.com/cobaltcore-dev/openstack-hypervisor-operator/refs/heads/main/charts/openstack-hypervisor-operator/crds/kvm.cloud.sap_hypervisors.yaml > /tmp/hypervisor-crd.yaml
kubectl --context kind-cortex-home apply -f /tmp/hypervisor-crd.yaml
kubectl --context kind-cortex-remote-az-a apply -f /tmp/hypervisor-crd.yaml
kubectl --context kind-cortex-remote-az-b apply -f /tmp/hypervisor-crd.yaml

echo "Storing az-a and az-b cluster certs under /tmp/root-ca-remote-az-a.pem and /tmp/root-ca-remote-az-b.pem"
kubectl --context kind-cortex-remote-az-a --namespace kube-system \
  get configmap extension-apiserver-authentication \
  -o jsonpath="{.data['client-ca-file']}" > /tmp/root-ca-remote-az-a.pem
kubectl --context kind-cortex-remote-az-b --namespace kube-system \
  get configmap extension-apiserver-authentication \
  -o jsonpath="{.data['client-ca-file']}" > /tmp/root-ca-remote-az-b.pem

echo "Setting up tilt overrides for cortex values"
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
        labels:
          az: cortex-remote-az-a
        caCert: |
$(cat /tmp/root-ca-remote-az-a.pem | sed 's/^/          /')
      - host: https://host.docker.internal:8445
        gvks:
        - kvm.cloud.sap/v1/Hypervisor
        - kvm.cloud.sap/v1/HypervisorList
        labels:
          az: cortex-remote-az-b
        caCert: |
$(cat /tmp/root-ca-remote-az-b.pem | sed 's/^/          /')
EOF

echo "Applying hypervisor resources in az-a and az-b clusters"
kubectl --context kind-cortex-remote-az-a apply \
    -f docs/guides/multicluster/hypervisors-az-a.yaml
kubectl --context kind-cortex-remote-az-b apply \
    -f docs/guides/multicluster/hypervisors-az-b.yaml

echo "Starting cortex in home cluster with tilt, using overrides from $TILT_OVERRIDES_PATH"
kubectl config use-context kind-cortex-home
export ACTIVE_DEPLOYMENTS="nova" && tilt up
