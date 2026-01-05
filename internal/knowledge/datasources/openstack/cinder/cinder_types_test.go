// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"encoding/json"
	"testing"
)

func checkOptionalStringField(t *testing.T, field, expected *string) {
	if field == nil && expected != nil {
		t.Errorf("Expected custom attribute to be '%s', got '<nil>'", *expected)
		return
	}
	if field != nil && expected == nil {
		t.Errorf("Expected custom attribute to be '<nil>', got '%s'", *field)
		return
	}
	if field != nil && expected != nil && *field != *expected {
		t.Errorf("Expected custom attribute to be '%s', got '%s'", *expected, *field)
		return
	}
	// Both are nil - this is OK, no error
}

func checkOptionalFloatField(t *testing.T, field, expected *float64) {
	if field == nil && expected != nil {
		t.Errorf("Expected float field to be %f, got '<nil>'", *expected)
		return
	}
	if field != nil && expected == nil {
		t.Errorf("Expected float field to be '<nil>', got %f", *field)
		return
	}
	if field != nil && expected != nil && *field != *expected {
		t.Errorf("Expected float field to be %f, got %f", *expected, *field)
		return
	}
	// Both are nil - this is OK, no error
}

func TestStoragePoolUnmarshalJSON_VMware(t *testing.T) {
	jsonData := `{
        "name": "test-pool",
        "capabilities": {
            "allocated_capacity_gb": 100,
            "backend_state": "up",
            "custom_attributes": {
                "cinder_state": "drain",
                "cinder_aggregate_id": "aggregate_id",
                "netapp_fqdn": "test-netapp-fqdn"
            },
            "driver_version": "driver-version",
            "free_capacity_gb": 1000,
            "multiattach": false,
            "pool_down_reason": "down reason",
            "pool_name": "pool-name",
            "pool_state": "down",
            "reserved_percentage": 20,
            "storage_protocol": "storage-protocol",
            "thick_provisioning_support": true,
            "thin_provisioning_support": false,
            "timestamp": "2025-08-18T11:45:00.000000",
            "total_capacity_gb": 5000,
            "vendor_name": "VMware",
            "volume_backend_name": "standard_hdd"
        }
    }`

	var sp StoragePool
	err := json.Unmarshal([]byte(jsonData), &sp)

	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}
	if sp.Name != "test-pool" {
		t.Errorf("Expected storage pool name to be 'test-pool', got '%s'", sp.Name)
	}
	if sp.CapabilitiesAllocatedCapacityGB != 100 {
		t.Errorf("Expected allocated capacity to be 100, got %f", sp.CapabilitiesAllocatedCapacityGB)
	}
	if sp.CapabilitiesDriverVersion != "driver-version" {
		t.Errorf("Expected driver version to be 'driver-version', got '%s'", sp.CapabilitiesDriverVersion)
	}
	if sp.CapabilitiesFreeCapacityGB != 1000 {
		t.Errorf("Expected free capacity to be 1000, got %f", sp.CapabilitiesFreeCapacityGB)
	}
	if sp.CapabilitiesMultiattach != false {
		t.Errorf("Expected multiattach to be false, got %v", sp.CapabilitiesMultiattach)
	}
	if sp.CapabilitiesPoolName != "pool-name" {
		t.Errorf("Expected pool name to be 'pool-name', got '%s'", sp.CapabilitiesPoolName)
	}
	if sp.CapabilitiesReservedPercentage != 20 {
		t.Errorf("Expected reserved percentage to be 20, got %v", sp.CapabilitiesReservedPercentage)
	}
	if sp.CapabilitiesThickProvisioningSupport != true {
		t.Errorf("Expected thick provisioning support to be true, got %v", sp.CapabilitiesThickProvisioningSupport)
	}
	if sp.CapabilitiesThinProvisioningSupport != false {
		t.Errorf("Expected thin provisioning support to be false, got %v", sp.CapabilitiesThinProvisioningSupport)
	}
	if sp.CapabilitiesTimestamp != "2025-08-18T11:45:00.000000" {
		t.Errorf("Expected timestamp to be '2025-08-18T11:45:00.000000', got '%s'", sp.CapabilitiesTimestamp)
	}
	if sp.CapabilitiesTotalCapacityGB != 5000 {
		t.Errorf("Expected total capacity to be 5000, got %f", sp.CapabilitiesTotalCapacityGB)
	}
	if sp.CapabilitiesVendorName != "VMware" {
		t.Errorf("Expected vendor name to be 'VMware', got '%s'", sp.CapabilitiesVendorName)
	}
	if sp.CapabilitiesVolumeBackendName != "standard_hdd" {
		t.Errorf("Expected volume backend name to be 'standard_hdd', got '%s'", sp.CapabilitiesVolumeBackendName)
	}

	// Check VMware specific fields exist
	expectedCapabilitiesNetAppAggregate := "test-netapp-fqdn"
	checkOptionalStringField(t, sp.CapabilitiesCustomAttributeNetAppFQDN, &expectedCapabilitiesNetAppAggregate)
	expectedCapabilitiesCinderState := "drain"
	checkOptionalStringField(t, sp.CapabilitiesCustomAttributeCinderState, &expectedCapabilitiesCinderState)
	expectedCapabilitiesCinderAggregateID := "aggregate_id"
	checkOptionalStringField(t, sp.CapabilitiesCustomAttributeCinderAggregateID, &expectedCapabilitiesCinderAggregateID)
	expectedCapabilitiesPoolDownReason := "down reason"
	checkOptionalStringField(t, sp.CapabilitiesPoolDownReason, &expectedCapabilitiesPoolDownReason)
	expectedCapabilitiesPoolState := "down"
	checkOptionalStringField(t, sp.CapabilitiesPoolState, &expectedCapabilitiesPoolState)

	// Check NetApp specific fields are nil
	checkOptionalStringField(t, sp.CapabilitiesNetAppAggregate, nil)
	checkOptionalFloatField(t, sp.CapabilitiesNetAppAggregateUsedPercent, nil)
	checkOptionalFloatField(t, sp.CapabilitiesUtilization, nil)
}

func TestStoragePoolUnmarshalJSON_NetApp(t *testing.T) {
	jsonData := `{
        "name": "test-pool",
        "capabilities": {
            "allocated_capacity_gb": 100,
            "driver_version": "driver-version",
            "free_capacity_gb": 1000,
            "multiattach": false,
            "pool_name": "pool-name",
            "reserved_percentage": 20,
            "storage_protocol": "storage-protocol",
            "thick_provisioning_support": true,
            "thin_provisioning_support": false,
            "timestamp": "2025-08-18T11:45:00.000000",
            "total_capacity_gb": 5000,
            "vendor_name": "VMware",
            "volume_backend_name": "standard_hdd",
			"netapp_aggregate": [
                "aggregate_1",
				"aggregate_2"
            ],
            "netapp_aggregate_used_percent": 30,
            "utilization": 50
        }
    }`

	var sp StoragePool
	err := json.Unmarshal([]byte(jsonData), &sp)

	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}
	if sp.Name != "test-pool" {
		t.Errorf("Expected storage pool name to be 'test-pool', got '%s'", sp.Name)
	}
	if sp.CapabilitiesAllocatedCapacityGB != 100 {
		t.Errorf("Expected allocated capacity to be 100, got %f", sp.CapabilitiesAllocatedCapacityGB)
	}
	if sp.CapabilitiesDriverVersion != "driver-version" {
		t.Errorf("Expected driver version to be 'driver-version', got '%s'", sp.CapabilitiesDriverVersion)
	}
	if sp.CapabilitiesFreeCapacityGB != 1000 {
		t.Errorf("Expected free capacity to be 1000, got %f", sp.CapabilitiesFreeCapacityGB)
	}
	if sp.CapabilitiesMultiattach != false {
		t.Errorf("Expected multiattach to be false, got %v", sp.CapabilitiesMultiattach)
	}
	if sp.CapabilitiesPoolName != "pool-name" {
		t.Errorf("Expected pool name to be 'pool-name', got '%s'", sp.CapabilitiesPoolName)
	}
	if sp.CapabilitiesReservedPercentage != 20 {
		t.Errorf("Expected reserved percentage to be 20, got %v", sp.CapabilitiesReservedPercentage)
	}
	if sp.CapabilitiesThickProvisioningSupport != true {
		t.Errorf("Expected thick provisioning support to be true, got %v", sp.CapabilitiesThickProvisioningSupport)
	}
	if sp.CapabilitiesThinProvisioningSupport != false {
		t.Errorf("Expected thin provisioning support to be false, got %v", sp.CapabilitiesThinProvisioningSupport)
	}
	if sp.CapabilitiesTimestamp != "2025-08-18T11:45:00.000000" {
		t.Errorf("Expected timestamp to be '2025-08-18T11:45:00.000000', got '%s'", sp.CapabilitiesTimestamp)
	}
	if sp.CapabilitiesTotalCapacityGB != 5000 {
		t.Errorf("Expected total capacity to be 5000, got %f", sp.CapabilitiesTotalCapacityGB)
	}
	if sp.CapabilitiesVendorName != "VMware" {
		t.Errorf("Expected vendor name to be 'VMware', got '%s'", sp.CapabilitiesVendorName)
	}
	if sp.CapabilitiesVolumeBackendName != "standard_hdd" {
		t.Errorf("Expected volume backend name to be 'standard_hdd', got '%s'", sp.CapabilitiesVolumeBackendName)
	}

	// Check VMware specific fields are nil
	checkOptionalStringField(t, sp.CapabilitiesCustomAttributeNetAppFQDN, nil)
	checkOptionalStringField(t, sp.CapabilitiesCustomAttributeCinderState, nil)
	checkOptionalStringField(t, sp.CapabilitiesCustomAttributeCinderAggregateID, nil)
	checkOptionalStringField(t, sp.CapabilitiesPoolDownReason, nil)
	checkOptionalStringField(t, sp.CapabilitiesPoolState, nil)

	// Check NetApp specific fields exist
	expectedCapabilitiesNetAppAggregate := "aggregate_1,aggregate_2"
	checkOptionalStringField(t, sp.CapabilitiesNetAppAggregate, &expectedCapabilitiesNetAppAggregate)
	expectedCapabilitiesNetAppAggregateUsedPercent := 30.0
	checkOptionalFloatField(t, sp.CapabilitiesNetAppAggregateUsedPercent, &expectedCapabilitiesNetAppAggregateUsedPercent)
	expectedCapabilitiesUtilization := 50.0
	checkOptionalFloatField(t, sp.CapabilitiesUtilization, &expectedCapabilitiesUtilization)
}
