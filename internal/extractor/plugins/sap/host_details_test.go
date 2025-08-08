// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/placement"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestHostDetailsExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &HostDetailsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_details_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(HostDetails{}) {
		t.Error("expected table to be created")
	}
}
func TestHostDetailsExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(HostDetails{}),
		testDB.AddTable(nova.Hypervisor{}),
		testDB.AddTable(nova.Aggregate{}),
		testDB.AddTable(placement.Trait{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the hypervisors and traits tables
	hypervisors := []any{
		// VMware host
		&nova.Hypervisor{ID: "uuid1", ServiceHost: "nova-compute-bb01", HypervisorType: "vcenter", RunningVMs: 5, State: "up"},
		// KVM host
		&nova.Hypervisor{ID: "uuid2", ServiceHost: "node001-bb02", HypervisorType: "qemu", RunningVMs: 3, State: "down"},
		// Ironic host (should be skipped)
		&nova.Hypervisor{ID: "uuid3", ServiceHost: "ironic-host-01", HypervisorType: "ironic", RunningVMs: 0, State: "up"},
		// Host with no special traits
		&nova.Hypervisor{ID: "uuid4", ServiceHost: "node002-bb03", HypervisorType: "test", RunningVMs: 2, State: "up"},
	}
	traits := []any{
		// VMware host traits
		&placement.Trait{ResourceProviderUUID: "uuid1", Name: "CUSTOM_HW_SAPPHIRE_RAPIDS"},
		&placement.Trait{ResourceProviderUUID: "uuid1", Name: "CUSTOM_HANA_EXCLUSIVE_HOST"},
		&placement.Trait{ResourceProviderUUID: "uuid1", Name: "CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED"},
		// KVM host traits
		&placement.Trait{ResourceProviderUUID: "uuid2", Name: "CUSTOM_NUMASIZE_C48_M729"},
		// Ironic host traits (should be ignored)
		&placement.Trait{ResourceProviderUUID: "uuid3", Name: "TRAIT_IGNORED"},
		// Disabled KVM host
		&placement.Trait{ResourceProviderUUID: "uuid4", Name: "CUSTOM_DECOMMISIONING_TRAIT"},
	}
	availabilityZone1 := "az1"
	availabilityZone2 := "az2"
	computeHost1 := "nova-compute-bb01"
	computeHost2 := "node001-bb02"
	computeHost3 := "node002-bb03"

	aggregates := []any{
		&nova.Aggregate{
			UUID:             "agg-uuid-1",
			Name:             "agg1",
			ComputeHost:      &computeHost2,
			AvailabilityZone: &availabilityZone1,
		},
		&nova.Aggregate{
			UUID:             "agg-uuid-1",
			Name:             "agg1",
			ComputeHost:      &computeHost1,
			AvailabilityZone: &availabilityZone1,
		},
		&nova.Aggregate{
			UUID:             "agg-uuid-2",
			Name:             "agg2",
			ComputeHost:      &computeHost3,
			AvailabilityZone: &availabilityZone2,
		},
	}
	if err := testDB.Insert(aggregates...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := testDB.Insert(traits...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &HostDetailsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_details_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var hostDetails []HostDetails
	_, err := testDB.Select(&hostDetails, "SELECT * FROM "+HostDetails{}.TableName())
	if err != nil {
		t.Fatalf("expected no error from Extract, got %v", err)
	}

	// Only non-ironic hosts should be present
	if len(hostDetails) != 4 {
		t.Fatalf("expected 4 host details, got %d", len(hostDetails))
	}

	disabledReasonExternal := "external customer"
	disabledReasonDecommissioning := "decommissioning"
	disabledReasonStateNotUp := "not up"

	expected := []HostDetails{
		{
			ComputeHost:      "nova-compute-bb01",
			AvailabilityZone: "az1",
			CPUArchitecture:  "sapphire-rapids",
			HypervisorType:   "vcenter",
			HypervisorFamily: "vmware",
			WorkloadType:     "hana",
			Enabled:          false,
			DisabledReason:   &disabledReasonExternal,
			RunningVMs:       5,
		},
		{
			ComputeHost:      "node001-bb02",
			AvailabilityZone: "az1",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "qemu",
			HypervisorFamily: "kvm",
			WorkloadType:     "general-purpose",
			Enabled:          false,
			DisabledReason:   &disabledReasonStateNotUp,
			RunningVMs:       3,
		},
		{
			ComputeHost:      "node002-bb03",
			AvailabilityZone: "az2",
			CPUArchitecture:  "unknown",
			HypervisorFamily: "kvm",
			HypervisorType:   "test",
			WorkloadType:     "general-purpose",
			Enabled:          false,
			DisabledReason:   &disabledReasonDecommissioning,
			RunningVMs:       2,
		},
		{
			ComputeHost:      "ironic-host-01",
			AvailabilityZone: "unknown",
			CPUArchitecture:  "unknown",
			HypervisorType:   "ironic",
			HypervisorFamily: "unknown",
			WorkloadType:     "general-purpose",
			Enabled:          true,
			DisabledReason:   nil,
			RunningVMs:       0,
		},
	}

	// Map the host details by compute host name for easier comparison
	hostDetailsMap := make(map[string]HostDetails)
	for _, details := range hostDetails {
		hostDetailsMap[details.ComputeHost] = details
	}
	// Check if the expected details match the extracted ones
	if len(hostDetailsMap) != len(expected) {
		t.Fatalf("expected %d host details, got %d", len(expected), len(hostDetailsMap))
	}
	// Compare each expected detail with the extracted ones
	for _, expectedDetail := range expected {
		details, exists := hostDetailsMap[expectedDetail.ComputeHost]
		if !exists {
			t.Errorf("expected host details for %s not found", expectedDetail.ComputeHost)
			continue
		}
		// Compare all fields
		if details.ComputeHost != expectedDetail.ComputeHost {
			t.Errorf("ComputeHost: expected %s, got %s", expectedDetail.ComputeHost, details.ComputeHost)
		}
		if details.AvailabilityZone != expectedDetail.AvailabilityZone {
			t.Errorf("%s[AvailabilityZone]: expected %s, got %s", expectedDetail.ComputeHost, expectedDetail.AvailabilityZone, details.AvailabilityZone)
		}
		if details.CPUArchitecture != expectedDetail.CPUArchitecture {
			t.Errorf("%s[CPUArchitecture]: expected %s, got %s", expectedDetail.ComputeHost, expectedDetail.CPUArchitecture, details.CPUArchitecture)
		}
		if details.HypervisorType != expectedDetail.HypervisorType {
			t.Errorf("%s[HypervisorType]: expected %s, got %s", expectedDetail.ComputeHost, expectedDetail.HypervisorType, details.HypervisorType)
		}
		if details.HypervisorFamily != expectedDetail.HypervisorFamily {
			t.Errorf("%s[HypervisorFamily]: expected %s, got %s", expectedDetail.ComputeHost, expectedDetail.HypervisorFamily, details.HypervisorFamily)
		}
		if details.WorkloadType != expectedDetail.WorkloadType {
			t.Errorf("%s[WorkloadType]: expected %s, got %s", expectedDetail.ComputeHost, expectedDetail.WorkloadType, details.WorkloadType)
		}
		if details.Enabled != expectedDetail.Enabled {
			t.Errorf("%s[Enabled]: expected %v, got %v", expectedDetail.ComputeHost, expectedDetail.Enabled, details.Enabled)
		}
		if details.RunningVMs != expectedDetail.RunningVMs {
			t.Errorf("%s[RunningVMs]: expected %d, got %d", expectedDetail.ComputeHost, expectedDetail.RunningVMs, details.RunningVMs)
		}
		// Compare DisabledReason pointer values
		if (details.DisabledReason == nil) != (expectedDetail.DisabledReason == nil) ||
			(details.DisabledReason != nil && expectedDetail.DisabledReason != nil && *details.DisabledReason != *expectedDetail.DisabledReason) {
			t.Errorf("%s[DisabledReason]: expected %s, got %s", expectedDetail.ComputeHost, ptrToString(expectedDetail.DisabledReason), ptrToString(details.DisabledReason))
		}
	}
}

func ptrToString(s *string) string {
	if s == nil {
		return "nil"
	}
	return *s
}
