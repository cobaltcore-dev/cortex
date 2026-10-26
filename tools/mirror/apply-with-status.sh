#!/usr/bin/env bash
#
# apply-with-status.sh — apply dumped Kubernetes resources into a cluster,
#                        status subresource included.
#
# WHAT IT DOES
#   `kubectl apply` never restores the `.status` of an object: status lives on a
#   separate subresource that the API server owns. When you want to replicate a
#   snapshot of resources (e.g. captured with `kubectl get ... -o yaml`) into
#   another cluster *with their status intact*, a plain apply is not enough.
#
#   This script does it in two passes:
#     1. Sanitize and server-side apply every item, creating the objects.
#     2. Patch each object's `.status` onto the freshly created resource via the
#        status subresource.
#
#   Before applying, it strips metadata that binds objects to their origin
#   cluster. The important one is `ownerReferences`: those point at owner
#   objects (e.g. Nodes) that don't exist in the target cluster, so Kubernetes'
#   garbage collector would delete the resources moments after creation. The
#   other fields (uid, resourceVersion, generation, ...) are server-managed and
#   would be rejected or cause conflicts if carried over.
#
# INPUT
#   Each file in FILES is expected to be a Kubernetes List (the shape produced
#   by `kubectl get <kind> -o yaml`), i.e. an object with an `.items[]` array.
#   Capture such a snapshot from the source cluster like:
#
#     kubectl get hypervisors -o yaml > hvs.yaml.out
#     kubectl get virtualmachines -o yaml > vms.yaml.out
#
# USAGE
#   Point your kubeconfig/context at the *target* cluster, then run from the
#   directory holding the snapshot files:
#
#     ./apply-with-status.sh
#
#   By default the standard kubectl config (the KUBECONFIG env var, else
#   ~/.kube/config) and its current context are used. Override them for a
#   single run via environment variables:
#
#     MIRROR_KUBECONFIG=target.yaml MIRROR_CONTEXT=kind-cluster-a ./apply-with-status.sh
#
#   Adjust the FILES array below to match the snapshots you want to replay.
#
# REQUIREMENTS
#   bash, kubectl, and yq (https://github.com/mikefarah/yq).
#
set -euo pipefail

# Snapshot files to replay, relative to the current directory. Each must be a
# Kubernetes List (`.items[]`) as produced by `kubectl get ... -o yaml`.
FILES=(hvs.yaml.out vms.yaml.out)

# Seconds to wait between the apply pass and the status-patch pass, giving the
# API server a moment to persist all created objects.
SETTLE_SECONDS=3

# Optional kubeconfig path and context for the target cluster. When empty,
# kubectl falls back to its default config (KUBECONFIG env var, else
# ~/.kube/config) and current context.
MIRROR_KUBECONFIG="${MIRROR_KUBECONFIG:-}"
MIRROR_CONTEXT="${MIRROR_CONTEXT:-}"

# Assemble the kubectl flags for the chosen config/context. Empty when neither
# is set, so kubectl uses its own defaults.
kubectl_args=()
if [ -n "$MIRROR_KUBECONFIG" ]; then
  kubectl_args+=(--kubeconfig "$MIRROR_KUBECONFIG")
fi
if [ -n "$MIRROR_CONTEXT" ]; then
  kubectl_args+=(--context "$MIRROR_CONTEXT")
fi

for bin in kubectl yq; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "error: required command '$bin' not found in PATH" >&2
    exit 1
  fi
done

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Strip fields that bind the objects to the source cluster. ownerReferences are
# the important one: they point at owner objects that don't exist here, so the
# garbage collector deletes the resources right after creation. The rest are
# server-managed and would conflict with the target cluster's own bookkeeping.
sanitize='del(.metadata.ownerReferences, .metadata.uid, .metadata.resourceVersion,
              .metadata.generation, .metadata.creationTimestamp,
              .metadata.managedFields, .metadata.selfLink)'

# Pass 1: create every object (spec only; status is ignored by apply).
for f in "${FILES[@]}"; do
  clean="$tmp/$(basename "$f").clean"
  yq "(.items[]) |= ($sanitize)" "$f" > "$clean"
  echo ">>> applying specs from $f"
  kubectl ${kubectl_args[@]+"${kubectl_args[@]}"} apply --server-side --force-conflicts -f "$clean"
done

# Give the API server a moment to persist all created objects.
sleep "$SETTLE_SECONDS"

# Pass 2: patch each object's status onto the now-existing resource.
for f in "${FILES[@]}"; do
  n=$(yq '.items | length' "$f")
  echo ">>> patching status for $n items from $f"
  for i in $(seq 0 $((n - 1))); do
    obj="$tmp/obj.yaml"
    st="$tmp/status.yaml"
    yq ".items[$i]" "$f" > "$obj"
    kind=$(yq '.kind' "$obj")
    name=$(yq '.metadata.name' "$obj")
    # Skip items that carry no status — nothing to patch.
    if [ "$(yq '.status // "null"' "$obj")" = "null" ]; then
      continue
    fi
    yq '{"status": .status}' "$obj" > "$st"
    kubectl ${kubectl_args[@]+"${kubectl_args[@]}"} patch "$kind" "$name" --subresource=status --type=merge --patch-file="$st"
  done
done
