// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestStoragePoolUnmarshalJSON(t *testing.T) {
	jsonData := `{
		"name": "pool1",
		"host": "host1",
		"backend": "backend1",
		"pool": "poolA",
		"capabilities": {
			"total_capacity_gb": 100.5,
			"free_capacity_gb": 50.25,
			"reserved_percentage": 10,
			"pool_name": "poolA",
			"share_backend_name": "backend1",
			"storage_protocol": "NFS",
			"vendor_name": "OpenStack",
			"replication_domain": "domain1",
			"sg_consistent_snapshot_support": "yes",
			"timestamp": "2024-06-12T12:00:00Z",
			"driver_version": ["1.0", "1.1"],
			"replication_type": "dr",
			"driver_handles_share_servers": true,
			"snapshot_support": [true, false],
			"create_share_from_snapshot_support": false,
			"revert_to_snapshot_support": null,
			"mount_snapshot_support": "supported",
			"dedupe": ["on", "off"],
			"compression": "lz4",
			"ipv4_support": true,
			"ipv6_support": [false]
		}
	}`

	var sp StoragePool
	err := json.Unmarshal([]byte(jsonData), &sp)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if sp.Name != "pool1" || sp.Host != "host1" || sp.Backend != "backend1" || sp.Pool != "poolA" {
		t.Errorf("Basic fields not set correctly: %+v", sp)
	}
	if sp.CapabilitiesTotalCapacityGB != 100.5 {
		t.Errorf("TotalCapacityGB: got %v, want 100.5", sp.CapabilitiesTotalCapacityGB)
	}
	if sp.CapabilitiesFreeCapacityGB != 50.25 {
		t.Errorf("FreeCapacityGB: got %v, want 50.25", sp.CapabilitiesFreeCapacityGB)
	}
	if sp.CapabilitiesReservedPercentage != 10 {
		t.Errorf("ReservedPercentage: got %v, want 10", sp.CapabilitiesReservedPercentage)
	}
	if sp.CapabilitiesPoolName != "poolA" {
		t.Errorf("PoolName: got %v, want poolA", sp.CapabilitiesPoolName)
	}
	if sp.CapabilitiesShareBackendName != "backend1" {
		t.Errorf("ShareBackendName: got %v, want backend1", sp.CapabilitiesShareBackendName)
	}
	if sp.CapabilitiesStorageProtocol != "NFS" {
		t.Errorf("StorageProtocol: got %v, want NFS", sp.CapabilitiesStorageProtocol)
	}
	if sp.CapabilitiesVendorName != "OpenStack" {
		t.Errorf("VendorName: got %v, want OpenStack", sp.CapabilitiesVendorName)
	}
	if sp.CapabilitiesReplicationDomain == nil || *sp.CapabilitiesReplicationDomain != "domain1" {
		t.Errorf("ReplicationDomain: got %v, want domain1", sp.CapabilitiesReplicationDomain)
	}
	if sp.CapabilitiesSGConsistentSnapshotSupport != "yes" {
		t.Errorf("SGConsistentSnapshotSupport: got %v, want yes", sp.CapabilitiesSGConsistentSnapshotSupport)
	}
	if sp.CapabilitiesTimestamp != "2024-06-12T12:00:00Z" {
		t.Errorf("Timestamp: got %v, want 2024-06-12T12:00:00Z", sp.CapabilitiesTimestamp)
	}

	// Check JSON-string fields
	checkJSON := func(field *string, want any, label string) {
		if field == nil {
			t.Errorf("%s: got nil, want %v", label, want)
			return
		}
		var got any
		if err := json.Unmarshal([]byte(*field), &got); err != nil {
			t.Errorf("%s: failed to unmarshal: %v", label, err)
			return
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", label, got, want)
		}
	}
	checkJSON(sp.CapabilitiesDriverVersion, []any{"1.0", "1.1"}, "DriverVersion")
	checkJSON(sp.CapabilitiesReplicationType, "dr", "ReplicationType")
	checkJSON(sp.CapabilitiesDriverHandlesShareServers, true, "DriverHandlesShareServers")
	checkJSON(sp.CapabilitiesSnapshotSupport, []any{true, false}, "SnapshotSupport")
	checkJSON(sp.CapabilitiesCreateShareFromSnapshotSupport, false, "CreateShareFromSnapshotSupport")
	if sp.CapabilitiesRevertToSnapshotSupport != nil {
		t.Errorf("RevertToSnapshotSupport: got %v, want nil", sp.CapabilitiesRevertToSnapshotSupport)
	}
	checkJSON(sp.CapabilitiesMountSnapshotSupport, "supported", "MountSnapshotSupport")
	checkJSON(sp.CapabilitiesDedupe, []any{"on", "off"}, "Dedupe")
	checkJSON(sp.CapabilitiesCompression, "lz4", "Compression")
	checkJSON(sp.CapabilitiesIPv4Support, true, "IPv4Support")
	checkJSON(sp.CapabilitiesIPv6Support, []any{false}, "IPv6Support")
}
