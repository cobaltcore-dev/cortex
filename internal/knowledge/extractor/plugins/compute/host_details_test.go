// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/placement"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHostDetailsExtractor_Init(t *testing.T) {
	extractor := &HostDetailsExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(nil, nil, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHostDetailsExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()

	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Hypervisor{}),
		testDB.AddTable(placement.Trait{}),
		testDB.AddTable(placement.InventoryUsage{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostPinnedProjects, err := v1alpha1.BoxFeatureList([]any{
		&HostPinnedProjects{ComputeHost: new("nova-compute-bb01"), Label: new("project-123")},
		&HostPinnedProjects{ComputeHost: new("nova-compute-bb01"), Label: new("project-456")},
		&HostPinnedProjects{ComputeHost: new("node001-bb02"), Label: nil},
		// No entry for ironic-host-1 since it is excluded in the feature host pinned projects
		&HostPinnedProjects{ComputeHost: new("node002-bb03"), Label: nil},
		&HostPinnedProjects{ComputeHost: new("node003-bb03"), Label: nil},
		&HostPinnedProjects{ComputeHost: new("node004-bb03"), Label: nil},
	})
	if err != nil {
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
		&nova.Hypervisor{ID: "uuid5", ServiceHost: "node003-bb03", HypervisorType: "test", RunningVMs: 2, State: "up", Status: "disabled", ServiceDisabledReason: new("example reason")},
		// Host with disabled trait
		&nova.Hypervisor{ID: "uuid6", ServiceHost: "node004-bb03", HypervisorType: "test", RunningVMs: 2, State: "up", Status: "enabled", ServiceDisabledReason: new("example reason")},
		// VMware host with a sub-TiB physical host size
		&nova.Hypervisor{ID: "uuid7", ServiceHost: "nova-compute-bb04", HypervisorType: "vcenter", RunningVMs: 1, State: "up", Status: "enabled"},
		// VMware host just under 1 TiB that rounds up to a full TiB
		&nova.Hypervisor{ID: "uuid8", ServiceHost: "nova-compute-bb05", HypervisorType: "vcenter", RunningVMs: 1, State: "up", Status: "enabled"},
	}

	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	traits := []any{
		// VMware host traits
		&placement.Trait{ResourceProviderUUID: "uuid1", Name: "CUSTOM_HW_SAPPHIRE_RAPIDS"},
		&placement.Trait{ResourceProviderUUID: "uuid1", Name: "CUSTOM_HANA_EXCLUSIVE_HOST"},
		&placement.Trait{ResourceProviderUUID: "uuid1", Name: "CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE"},
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

	// Memory inventory for the VMware building block host (uuid1). The building
	// block pools 3 physical hosts of 4 TiB each (4 TiB = 4194304 MiB):
	//   amount_hosts       = (total - reserved) / max_unit = 12582912 / 4194304 = 3
	//   physical_host_size = max_unit + reserved / amount_hosts = 4194304 MiB = 4 TiB
	inventories := []any{
		&placement.InventoryUsage{
			ResourceProviderUUID: "uuid1",
			InventoryClassName:   "MEMORY_MB",
			Total:                12582912,
			Reserved:             0,
			MaxUnit:              4194304,
		},
		// Building block of 2 physical hosts of 512 GiB each (512 GiB = 524288 MiB):
		//   amount_hosts       = (1048576 - 0) / 524288 = 2
		//   physical_host_size = 524288 MiB = 512 GiB (< 1 TiB, reported in GiB)
		&placement.InventoryUsage{
			ResourceProviderUUID: "uuid7",
			InventoryClassName:   "MEMORY_MB",
			Total:                1048576,
			Reserved:             0,
			MaxUnit:              524288,
		},
		// Single host of 1048064 MiB (1023.5 GiB) which rounds to 1024 GiB and is
		// therefore reported as "1TiB" rather than "1024GiB".
		&placement.InventoryUsage{
			ResourceProviderUUID: "uuid8",
			InventoryClassName:   "MEMORY_MB",
			Total:                1048064,
			Reserved:             0,
			MaxUnit:              1048064,
		},
	}
	if err := testDB.Insert(inventories...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostAvailabilityZones, err := v1alpha1.BoxFeatureList([]any{
		&HostAZ{AvailabilityZone: new("az1"), ComputeHost: "nova-compute-bb01"},
		&HostAZ{AvailabilityZone: nil, ComputeHost: "node001-bb02"},
		&HostAZ{AvailabilityZone: new("az2"), ComputeHost: "node002-bb03"},
		&HostAZ{AvailabilityZone: new("az2"), ComputeHost: "ironic-host-01"},
		&HostAZ{AvailabilityZone: new("az2"), ComputeHost: "node003-bb03"},
		&HostAZ{AvailabilityZone: new("az2"), ComputeHost: "node004-bb03"},
		&HostAZ{AvailabilityZone: new("az1"), ComputeHost: "nova-compute-bb04"},
		&HostAZ{AvailabilityZone: new("az1"), ComputeHost: "nova-compute-bb05"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &HostDetailsExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: "host-pinned-projects"},
			Status:     v1alpha1.KnowledgeStatus{Raw: hostPinnedProjects},
		}).
		WithObjects(&v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: "host-az"},
			Status:     v1alpha1.KnowledgeStatus{Raw: hostAvailabilityZones},
		}).
		Build()
	if err := extractor.Init(&testDB, client, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	features, err := extractor.Extract()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := []HostDetails{
		{
			ComputeHost:      "ironic-host-01",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "ironic",
			HypervisorFamily: "unknown",
			WorkloadType:     "general-purpose",
			Enabled:          true,
			ExternalCustomer: false,
			Decommissioned:   false,
			DisabledReason:   nil,
			RunningVMs:       0,
			PinnedProjects:   nil,
			PhysicalHostSize: "unknown",
		},
		{
			ComputeHost:      "node001-bb02",
			AvailabilityZone: "unknown",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "qemu",
			HypervisorFamily: "kvm",
			WorkloadType:     "general-purpose",
			Enabled:          false,
			Decommissioned:   false,
			ExternalCustomer: false,
			DisabledReason:   new("[down] --"),
			RunningVMs:       3,
			PinnedProjects:   nil,
			PhysicalHostSize: "unknown",
		},
		{
			ComputeHost:      "node002-bb03",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorFamily: "kvm",
			HypervisorType:   "test",
			WorkloadType:     "general-purpose",
			Enabled:          true,
			Decommissioned:   true,
			ExternalCustomer: false,
			DisabledReason:   nil,
			RunningVMs:       2,
			PinnedProjects:   nil,
			PhysicalHostSize: "unknown",
		},
		{
			ComputeHost:      "node003-bb03",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "test",
			HypervisorFamily: "kvm",
			WorkloadType:     "general-purpose",
			Enabled:          false,
			Decommissioned:   false,
			ExternalCustomer: false,
			DisabledReason:   new("[disabled] example reason"),
			RunningVMs:       2,
			PinnedProjects:   nil,
			PhysicalHostSize: "unknown",
		},
		{
			ComputeHost:      "node004-bb03",
			AvailabilityZone: "az2",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "test",
			HypervisorFamily: "kvm",
			WorkloadType:     "general-purpose",
			Enabled:          false,
			Decommissioned:   false,
			ExternalCustomer: false,
			DisabledReason:   new("[disabled] example reason"),
			RunningVMs:       2,
			PinnedProjects:   nil,
			PhysicalHostSize: "unknown",
		},
		{
			ComputeHost:      "nova-compute-bb01",
			AvailabilityZone: "az1",
			CPUArchitecture:  "sapphire-rapids",
			HypervisorType:   "vcenter",
			HypervisorFamily: "vmware",
			WorkloadType:     "hana",
			Enabled:          true,
			Decommissioned:   false,
			ExternalCustomer: true,
			DisabledReason:   nil,
			RunningVMs:       5,
			PinnedProjects:   new("project-123,project-456"),
			PhysicalHostSize: "4TiB",
		},
		{
			ComputeHost:      "nova-compute-bb04",
			AvailabilityZone: "az1",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "vcenter",
			HypervisorFamily: "vmware",
			WorkloadType:     "general-purpose",
			Enabled:          true,
			Decommissioned:   false,
			ExternalCustomer: false,
			DisabledReason:   nil,
			RunningVMs:       1,
			PinnedProjects:   nil,
			PhysicalHostSize: "512GiB",
		},
		{
			ComputeHost:      "nova-compute-bb05",
			AvailabilityZone: "az1",
			CPUArchitecture:  "cascade-lake",
			HypervisorType:   "vcenter",
			HypervisorFamily: "vmware",
			WorkloadType:     "general-purpose",
			Enabled:          true,
			Decommissioned:   false,
			ExternalCustomer: false,
			DisabledReason:   nil,
			RunningVMs:       1,
			PinnedProjects:   nil,
			PhysicalHostSize: "1TiB",
		},
	}

	// Check if the expected details match the extracted ones
	if len(features) != len(expected) {
		t.Fatalf("expected %d host details, got %d", len(expected), len(features))
	}
	// Compare each expected detail with the extracted ones
	for _, expectedDetail := range expected {
		found := false
		for _, feature := range features {
			extractedDetail := feature.(HostDetails)
			if extractedDetail.ComputeHost == expectedDetail.ComputeHost {
				found = true
				if !reflect.DeepEqual(extractedDetail, expectedDetail) {
					t.Errorf("mismatch for host %s:\nexpected: %+v\ngot:      %+v",
						expectedDetail.ComputeHost, expectedDetail, extractedDetail)
				}
				break
			}
		}
		if !found {
			t.Errorf("expected host detail for %s not found", expectedDetail.ComputeHost)
		}
	}
}
