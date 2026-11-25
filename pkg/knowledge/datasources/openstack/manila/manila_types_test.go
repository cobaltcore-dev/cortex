// Copyright SAP SE
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

func TestStoragePoolMarshalJSON(t *testing.T) {
	// Create a StoragePool with test data
	replicationDomain := "domain1"
	driverVersionJSON := `["1.0","1.1"]`
	replicationTypeJSON := `"dr"`
	driverHandlesShareServersJSON := `true`
	snapshotSupportJSON := `[true,false]`
	createShareFromSnapshotSupportJSON := `false`
	mountSnapshotSupportJSON := `"supported"`
	dedupeJSON := `["on","off"]`
	compressionJSON := `"lz4"`
	ipv4SupportJSON := `true`
	ipv6SupportJSON := `[false]`

	sp := StoragePool{
		Name:    "pool1",
		Host:    "host1",
		Backend: "backend1",
		Pool:    "poolA",

		CapabilitiesTotalCapacityGB:             100.5,
		CapabilitiesFreeCapacityGB:              50.25,
		CapabilitiesReservedPercentage:          10,
		CapabilitiesPoolName:                    "poolA",
		CapabilitiesShareBackendName:            "backend1",
		CapabilitiesStorageProtocol:             "NFS",
		CapabilitiesVendorName:                  "OpenStack",
		CapabilitiesReplicationDomain:           &replicationDomain,
		CapabilitiesSGConsistentSnapshotSupport: "yes",
		CapabilitiesTimestamp:                   "2024-06-12T12:00:00Z",

		CapabilitiesDriverVersion:                  &driverVersionJSON,
		CapabilitiesReplicationType:                &replicationTypeJSON,
		CapabilitiesDriverHandlesShareServers:      &driverHandlesShareServersJSON,
		CapabilitiesSnapshotSupport:                &snapshotSupportJSON,
		CapabilitiesCreateShareFromSnapshotSupport: &createShareFromSnapshotSupportJSON,
		CapabilitiesRevertToSnapshotSupport:        nil,
		CapabilitiesMountSnapshotSupport:           &mountSnapshotSupportJSON,
		CapabilitiesDedupe:                         &dedupeJSON,
		CapabilitiesCompression:                    &compressionJSON,
		CapabilitiesIPv4Support:                    &ipv4SupportJSON,
		CapabilitiesIPv6Support:                    &ipv6SupportJSON,
	}

	// Marshal the StoragePool
	jsonData, err := json.Marshal(&sp)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal into a generic map to verify structure
	var result map[string]any
	err = json.Unmarshal(jsonData, &result)
	if err != nil {
		t.Fatalf("Unmarshal result failed: %v", err)
	}

	// Check top-level fields
	if result["name"] != "pool1" {
		t.Errorf("name: got %v, want pool1", result["name"])
	}
	if result["host"] != "host1" {
		t.Errorf("host: got %v, want host1", result["host"])
	}
	if result["backend"] != "backend1" {
		t.Errorf("backend: got %v, want backend1", result["backend"])
	}
	if result["pool"] != "poolA" {
		t.Errorf("pool: got %v, want poolA", result["pool"])
	}

	// Check capabilities object exists
	capabilities, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities is not a map: %T", result["capabilities"])
	}

	// Check capabilities fields
	if capabilities["total_capacity_gb"] != 100.5 {
		t.Errorf("total_capacity_gb: got %v, want 100.5", capabilities["total_capacity_gb"])
	}
	if capabilities["free_capacity_gb"] != 50.25 {
		t.Errorf("free_capacity_gb: got %v, want 50.25", capabilities["free_capacity_gb"])
	}
	if capabilities["reserved_percentage"] != float64(10) {
		t.Errorf("reserved_percentage: got %v, want 10", capabilities["reserved_percentage"])
	}
	if capabilities["pool_name"] != "poolA" {
		t.Errorf("pool_name: got %v, want poolA", capabilities["pool_name"])
	}
	if capabilities["share_backend_name"] != "backend1" {
		t.Errorf("share_backend_name: got %v, want backend1", capabilities["share_backend_name"])
	}
	if capabilities["storage_protocol"] != "NFS" {
		t.Errorf("storage_protocol: got %v, want NFS", capabilities["storage_protocol"])
	}
	if capabilities["vendor_name"] != "OpenStack" {
		t.Errorf("vendor_name: got %v, want OpenStack", capabilities["vendor_name"])
	}
	if capabilities["replication_domain"] != "domain1" {
		t.Errorf("replication_domain: got %v, want domain1", capabilities["replication_domain"])
	}
	if capabilities["sg_consistent_snapshot_support"] != "yes" {
		t.Errorf("sg_consistent_snapshot_support: got %v, want yes", capabilities["sg_consistent_snapshot_support"])
	}
	if capabilities["timestamp"] != "2024-06-12T12:00:00Z" {
		t.Errorf("timestamp: got %v, want 2024-06-12T12:00:00Z", capabilities["timestamp"])
	}

	// Check complex fields that were stored as JSON strings
	if !reflect.DeepEqual(capabilities["driver_version"], []any{"1.0", "1.1"}) {
		t.Errorf("driver_version: got %v, want [1.0 1.1]", capabilities["driver_version"])
	}
	if capabilities["replication_type"] != "dr" {
		t.Errorf("replication_type: got %v, want dr", capabilities["replication_type"])
	}
	if capabilities["driver_handles_share_servers"] != true {
		t.Errorf("driver_handles_share_servers: got %v, want true", capabilities["driver_handles_share_servers"])
	}
	if !reflect.DeepEqual(capabilities["snapshot_support"], []any{true, false}) {
		t.Errorf("snapshot_support: got %v, want [true false]", capabilities["snapshot_support"])
	}
	if capabilities["create_share_from_snapshot_support"] != false {
		t.Errorf("create_share_from_snapshot_support: got %v, want false", capabilities["create_share_from_snapshot_support"])
	}
	if capabilities["revert_to_snapshot_support"] != nil {
		t.Errorf("revert_to_snapshot_support: got %v, want nil", capabilities["revert_to_snapshot_support"])
	}
	if capabilities["mount_snapshot_support"] != "supported" {
		t.Errorf("mount_snapshot_support: got %v, want supported", capabilities["mount_snapshot_support"])
	}
	if !reflect.DeepEqual(capabilities["dedupe"], []any{"on", "off"}) {
		t.Errorf("dedupe: got %v, want [on off]", capabilities["dedupe"])
	}
	if capabilities["compression"] != "lz4" {
		t.Errorf("compression: got %v, want lz4", capabilities["compression"])
	}
	if capabilities["ipv4_support"] != true {
		t.Errorf("ipv4_support: got %v, want true", capabilities["ipv4_support"])
	}
	if !reflect.DeepEqual(capabilities["ipv6_support"], []any{false}) {
		t.Errorf("ipv6_support: got %v, want [false]", capabilities["ipv6_support"])
	}
}

func TestStoragePoolMarshalUnmarshalRoundTrip(t *testing.T) {
	// Test that marshaling and unmarshaling produces the same result
	originalJSON := `{
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

	// Unmarshal original JSON
	var sp1 StoragePool
	err := json.Unmarshal([]byte(originalJSON), &sp1)
	if err != nil {
		t.Fatalf("First unmarshal failed: %v", err)
	}

	// Marshal back to JSON
	marshaledJSON, err := json.Marshal(&sp1)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal the marshaled JSON
	var sp2 StoragePool
	err = json.Unmarshal(marshaledJSON, &sp2)
	if err != nil {
		t.Fatalf("Second unmarshal failed: %v", err)
	}

	// Compare the two StoragePool structs
	if !reflect.DeepEqual(sp1, sp2) {
		t.Errorf("Round trip failed: structs are not equal")
		t.Logf("Original: %+v", sp1)
		t.Logf("Round trip: %+v", sp2)
	}
}
