#!/bin/bash

set -e

API_URL="http://localhost:8001/scheduler/nova/external"
INSTANCE_UUID="cortex-test-instance-001"
HISTORY_NAME="nova-$INSTANCE_UUID"

# --- Step 1: Apply the test pipeline -----------------------------------------

echo "=== Step 1: Apply test pipeline ==="
echo ""
echo "The test pipeline is a minimal filter-weigher pipeline with:"
echo "  - createHistory: true  (so a History CRD is created for each decision)"
echo "  - filter_correct_az    (filters hosts not matching the requested AZ)"
echo "  - no weighers          (hosts are returned in their original order)"
echo ""

kubectl --context kind-cortex-home apply -f docs/guides/multicluster/test-pipeline.yaml

echo ""
echo "Press enter to send a scheduling request..."
read -r

# --- Step 2: Send scheduling request -----------------------------------------

echo "=== Step 2: Send scheduling request ==="
echo ""
echo "Sending a Nova external scheduler request to the cortex API."
echo ""
echo "  Instance UUID:      $INSTANCE_UUID"
echo "  Availability Zone:  cortex-remote-az-b"
echo "  Pipeline:           multicluster-test"
echo "  Candidate hosts:    hypervisor-{1,2}-az-{a,b}  (4 hosts across 2 AZs)"
echo ""
echo "The pipeline's filter_correct_az step should filter out the az-a hosts,"
echo "leaving only hypervisor-1-az-b and hypervisor-2-az-b."
echo ""

RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "$API_URL" \
  -H "Content-Type: application/json" \
  -d @- <<EOF
{
  "spec": {
    "nova_object.name": "RequestSpec",
    "nova_object.namespace": "nova",
    "nova_object.version": "1.14",
    "nova_object.changes": [],
    "nova_object.data": {
      "project_id": "test-project",
      "user_id": "test-user",
      "instance_uuid": "$INSTANCE_UUID",
      "availability_zone": "cortex-remote-az-b",
      "num_instances": 1,
      "is_bfv": false,
      "scheduler_hints": {},
      "ignore_hosts": null,
      "force_hosts": null,
      "force_nodes": null,
      "image": {
        "nova_object.name": "ImageMeta",
        "nova_object.namespace": "nova",
        "nova_object.version": "1.8",
        "nova_object.changes": [],
        "nova_object.data": {
          "id": "00000000-0000-0000-0000-000000000001",
          "name": "test-image",
          "status": "active",
          "checksum": "0000000000000000",
          "owner": "test-project",
          "size": 1024,
          "container_format": "bare",
          "disk_format": "raw",
          "created_at": "2025-01-01T00:00:00Z",
          "updated_at": "2025-01-01T00:00:00Z",
          "min_ram": 0,
          "min_disk": 0,
          "properties": {
            "nova_object.name": "ImageMetaProps",
            "nova_object.namespace": "nova",
            "nova_object.version": "1.36",
            "nova_object.changes": [],
            "nova_object.data": {}
          }
        }
      },
      "flavor": {
        "nova_object.name": "Flavor",
        "nova_object.namespace": "nova",
        "nova_object.version": "1.2",
        "nova_object.changes": [],
        "nova_object.data": {
          "id": 1,
          "name": "m1.small",
          "memory_mb": 2048,
          "vcpus": 1,
          "root_gb": 20,
          "ephemeral_gb": 0,
          "flavorid": "1",
          "swap": 0,
          "rxtx_factor": 1.0,
          "vcpu_weight": 0,
          "disabled": false,
          "is_public": true,
          "extra_specs": {
            "capabilities:hypervisor_type": "qemu"
          },
          "description": null,
          "created_at": "2025-01-01T00:00:00Z",
          "updated_at": null
        }
      },
      "request_level_params": {
        "nova_object.name": "RequestLevelParams",
        "nova_object.namespace": "nova",
        "nova_object.version": "1.1",
        "nova_object.changes": [],
        "nova_object.data": {
          "root_required": [],
          "root_forbidden": [],
          "same_subtree": []
        }
      },
      "network_metadata": {
        "nova_object.name": "NetworkMetadata",
        "nova_object.namespace": "nova",
        "nova_object.version": "1.0",
        "nova_object.changes": [],
        "nova_object.data": {
          "physnets": [],
          "tunneled": false
        }
      },
      "limits": {
        "nova_object.name": "SchedulerLimits",
        "nova_object.namespace": "nova",
        "nova_object.version": "1.0",
        "nova_object.changes": [],
        "nova_object.data": {}
      },
      "requested_networks": {
        "objects": null
      },
      "security_groups": {
        "objects": null
      }
    }
  },
  "context": {
    "user": "test-user",
    "project_id": "test-project",
    "system_scope": null,
    "project": "test-project",
    "domain": null,
    "user_domain": "Default",
    "project_domain": "Default",
    "is_admin": false,
    "read_only": false,
    "show_deleted": false,
    "request_id": "req-test-001",
    "global_request_id": null,
    "resource_uuid": null,
    "roles": [],
    "user_identity": "test-user test-project - Default -",
    "is_admin_project": false,
    "read_deleted": "no",
    "remote_address": "127.0.0.1",
    "timestamp": "2025-01-01T00:00:00.000000",
    "quota_class": null,
    "user_name": "test-user",
    "project_name": "test-project"
  },
  "hosts": [
    {"host": "hypervisor-1-az-a", "hypervisor_hostname": "hypervisor-1-az-a"},
    {"host": "hypervisor-2-az-a", "hypervisor_hostname": "hypervisor-2-az-a"},
    {"host": "hypervisor-1-az-b", "hypervisor_hostname": "hypervisor-1-az-b"},
    {"host": "hypervisor-2-az-b", "hypervisor_hostname": "hypervisor-2-az-b"}
  ],
  "weights": {
    "hypervisor-1-az-a": 1.0,
    "hypervisor-2-az-a": 2.0,
    "hypervisor-1-az-b": 3.0,
    "hypervisor-2-az-b": 4.0
  },
  "pipeline": "multicluster-test"
}
EOF
)

HTTP_CODE=$(echo "$RESPONSE" | tail -1)
BODY=$(echo "$RESPONSE" | sed '$d')

echo "Response (HTTP $HTTP_CODE):"
echo "$BODY" | python3 -m json.tool 2>/dev/null || echo "$BODY"

if [ "$HTTP_CODE" != "200" ]; then
  echo ""
  echo "ERROR: Scheduling request failed. Check the controller logs:"
  echo "  kubectl --context kind-cortex-home logs deploy/cortex-nova-scheduling-controller-manager"
  exit 1
fi

echo ""
echo "Press enter to check History CRDs and events across all clusters..."
read -r

# --- Step 3: Check History and Events ----------------------------------------

echo "=== Step 3: Check History CRDs and Events ==="
echo ""
echo "The pipeline has createHistory: true, so a History CRD named '$HISTORY_NAME'"
echo "should have been created. An event should also have been recorded on it."
echo "Based on the multicluster config, this should be on the remote cluster cortex-remote-az-b."
echo ""

sleep 1

for CLUSTER in kind-cortex-home kind-cortex-remote-az-a kind-cortex-remote-az-b; do
  echo "--- $CLUSTER ---"
  echo "Histories:"
  kubectl --context "$CLUSTER" get histories 2>/dev/null || echo "  (none)"
  echo "Events:"
  kubectl --context "$CLUSTER" get events --field-selector reason=SchedulingSucceeded 2>/dev/null || echo "  (none)"
  echo ""
done

echo "Press enter to describe the History CRD and see the full scheduling result..."
read -r

# --- Step 4: Describe History ------------------------------------------------

echo "=== Step 4: Describe History CRD ==="
echo ""
echo "The History CRD contains the full scheduling decision context:"
echo "  - Which pipeline was used"
echo "  - The target host that was selected"
echo "  - An explanation of each filter/weigher step"
echo "  - The Ready condition (True = host selected, False = no host found)"
echo ""

# Try all clusters to find where the History ended up.
for CLUSTER in kind-cortex-home kind-cortex-remote-az-a kind-cortex-remote-az-b; do
  if kubectl --context "$CLUSTER" get history "$HISTORY_NAME" &>/dev/null; then
    echo "Found History '$HISTORY_NAME' in $CLUSTER:"
    echo ""
    kubectl --context "$CLUSTER" describe history "$HISTORY_NAME"
    exit 0
  fi
done

echo "WARNING: History '$HISTORY_NAME' was not found in any cluster."
echo "Check the controller logs for errors:"
echo "  kubectl --context kind-cortex-home logs deploy/cortex-nova-scheduling-controller-manager | grep -i history"
