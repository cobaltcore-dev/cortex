// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/sap"
	sharedapi "github.com/cobaltcore-dev/cortex/extractor/api/features/shared"
	"github.com/cobaltcore-dev/cortex/extractor/internal/conf"
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/nova"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/placement"
	"github.com/cobaltcore-dev/cortex/testlib"
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
		Options:        libconf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(sap.HostDetails{}) {
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
		testDB.AddTable(sap.HostDetails{}),
		testDB.AddTable(sharedapi.HostAZ{}),
		testDB.AddTable(nova.Hypervisor{}),
		testDB.AddTable(placement.Trait{}),
		testDB.AddTable(sharedapi.HostPinnedProjects{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostPinnedProjects := []any{
		&sharedapi.HostPinnedProjects{ComputeHost: testlib.Ptr("nova-compute-bb01"), Label: testlib.Ptr("project-123")},
		&sharedapi.HostPinnedProjects{ComputeHost: testlib.Ptr("nova-compute-bb01"), Label: testlib.Ptr("project-456")},
		&sharedapi.HostPinnedProjects{ComputeHost: testlib.Ptr("node001-bb02"), Label: nil},
		// No entry for ironic-host-1 since it is excluded in the feature host pinned projects
		&sharedapi.HostPinnedProjects{ComputeHost: testlib.Ptr("node002-bb03"), Label: nil},
		&sharedapi.HostPinnedProjects{ComputeHost: testlib.Ptr("node003-bb03"), Label: nil},
		&sharedapi.HostPinnedProjects{ComputeHost: testlib.Ptr("node004-bb03"), Label: nil},
	}

	if err := testDB.Insert(hostPinnedProjects...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the hypervisors and traits tables
	hypervisors := []any{
		// VMware host
		&nova.Hypervisor{ID: "uuid1", ServiceHost: "nova-compute-bb01", HypervisorType: "vcenter", RunningVMs: 5, State: "up", Status: "enabled"},
		// KVM host
		&nova.Hypervisor{ID: "uuid2", ServiceHost: "node001-bb02", HypervisorType: "qemu", RunningVMs: 3, State: "down", Status: "enabled"},
		// Ironic host (should be skipped)
		&nova.Hypervisor{ID: "uuid3", ServiceHost: "ironic-host-01", HypervisorType: "ironic", RunningVMs: 0, State: "up", Status: "enabled"},
		// Host with no special traits
		&nova.Hypervisor{ID: "uuid4", ServiceHost: "node002-bb03", HypervisorType: "test", RunningVMs: 2, State: "up", Status: "enabled"},
		// Host with disabled status, no entry in the resource providers
		&nova.Hypervisor{ID: "uuid5", ServiceHost: "node003-bb03", HypervisorType: "test", RunningVMs: 2, State: "up", Status: "disabled", ServiceDisabledReason: testlib.Ptr("example reason")},
		// Host with disabled trait
		&nova.Hypervisor{ID: "uuid6", ServiceHost: "node004-bb03", HypervisorType: "test", RunningVMs: 2, State: "up", Status: "enabled", ServiceDisabledReason: testlib.Ptr("example reason")},
	}

	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	traits := []any{
		// VMware host traits
		&placement.Trait{ResourceProviderUUID: "uuid1", Name: "CUSTOM_HW_SAPPHIRE_RAPIDS"},
		&placement.Trait{ResourceProviderUUID: "uuid1", Name: "CUSTOM_HANA_EXCLUSIVE_HOST"},
		&placement.Trait{ResourceProviderUUID: "uuid1", Name: "CUSTOM_EXTERNAL_CUSTOMER_SUPPORTED"},
		// KVM host traits
		&placement.Trait{ResourceProviderUUID: "uuid2", Name: "CUSTOM_NUMASIZE_C48_M729"},
		// Ironic host traits
		&placement.Trait{ResourceProviderUUID: "uuid3", Name: "TRAIT_IGNORED"},
		// Disabled KVM host
		&placement.Trait{ResourceProviderUUID: "uuid4", Name: "CUSTOM_DECOMMISSIONING"},
		// Host with disabled trait
		&placement.Trait{ResourceProviderUUID: "uuid6", Name: "COMPUTE_STATUS_DISABLED"},
	}

	if err := testDB.Insert(traits...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostAvailabilityZones := []any{
		&sharedapi.HostAZ{AvailabilityZone: testlib.Ptr("az1"), ComputeHost: "nova-compute-bb01"},
		&sharedapi.HostAZ{AvailabilityZone: nil, ComputeHost: "node001-bb02"},
		&sharedapi.HostAZ{AvailabilityZone: testlib.Ptr("az2"), ComputeHost: "node002-bb03"},
		&sharedapi.HostAZ{AvailabilityZone: testlib.Ptr("az2"), ComputeHost: "ironic-host-01"},
		&sharedapi.HostAZ{AvailabilityZone: testlib.Ptr("az2"), ComputeHost: "node003-bb03"},
		&sharedapi.HostAZ{AvailabilityZone: testlib.Ptr("az2"), ComputeHost: "node004-bb03"},
	}

	if err := testDB.Insert(hostAvailabilityZones...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &HostDetailsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "sap_host_details_extractor",
		Options:        libconf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var hostDetails []sap.HostDetails
	_, err := testDB.Select(&hostDetails, "SELECT * FROM "+sap.HostDetails{}.TableName()+" ORDER BY compute_host")
	if err != nil {
		t.Fatalf("expected no error from Extract, got %v", err)
	}

	expected := []sap.HostDetails{
		{
			ComputeHost:      "ironic-host-01",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "ironic",
			HypervisorFamily: "unknown",
			WorkloadType:     "general-purpose",
			Enabled:          true,
			DisabledReason:   nil,
			RunningVMs:       0,
			PinnedProjects:   nil,
		},
		{
			ComputeHost:      "node001-bb02",
			AvailabilityZone: "unknown",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "qemu",
			HypervisorFamily: "kvm",
			WorkloadType:     "general-purpose",
			Enabled:          false,
			DisabledReason:   testlib.Ptr("[state: not up] --"),
			RunningVMs:       3,
			PinnedProjects:   nil,
		},
		{
			ComputeHost:      "node002-bb03",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorFamily: "kvm",
			HypervisorType:   "test",
			WorkloadType:     "general-purpose",
			Enabled:          false,
			DisabledReason:   testlib.Ptr("decommissioning"),
			RunningVMs:       2,
			PinnedProjects:   nil,
		},
		{
			ComputeHost:      "node003-bb03",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "test",
			HypervisorFamily: "kvm",
			WorkloadType:     "general-purpose",
			Enabled:          false,
			DisabledReason:   testlib.Ptr("[status: not enabled] example reason"),
			RunningVMs:       2,
			PinnedProjects:   nil,
		},
		{
			ComputeHost:      "node004-bb03",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "test",
			HypervisorFamily: "kvm",
			WorkloadType:     "general-purpose",
			Enabled:          false,
			DisabledReason:   testlib.Ptr("[compute status disabled trait] example reason"),
			RunningVMs:       2,
			PinnedProjects:   nil,
		},
		{
			ComputeHost:      "nova-compute-bb01",
			AvailabilityZone: "az1",
			CPUArchitecture:  "sapphire-rapids",
			HypervisorType:   "vcenter",
			HypervisorFamily: "vmware",
			WorkloadType:     "hana",
			Enabled:          false,
			DisabledReason:   testlib.Ptr("external customer"),
			RunningVMs:       5,
			PinnedProjects:   testlib.Ptr("project-123,project-456"),
		},
	}

	// Check if the expected details match the extracted ones
	if len(hostDetails) != len(expected) {
		t.Fatalf("expected %d host details, got %d", len(expected), len(hostDetails))
	}
	// Compare each expected detail with the extracted ones
	for idx, expectedDetail := range expected {
		details := hostDetails[idx]
		if !reflect.DeepEqual(details, expectedDetail) {
			t.Errorf("expected %v, got %v", expectedDetail, details)
		}
	}
}
