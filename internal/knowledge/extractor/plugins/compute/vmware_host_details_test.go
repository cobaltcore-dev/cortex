// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/placement"
)

func TestVMwareHostDetailsExtractor_Extract(t *testing.T) {
	memory := func(uuid string, total, reserved, maxUnit int) placement.InventoryUsage {
		return placement.InventoryUsage{ResourceProviderUUID: uuid, InventoryClassName: "MEMORY_MB", Total: total, Reserved: reserved, MaxUnit: maxUnit}
	}
	in := VMwareHostDetailsInputs{
		// Only VMware hosts (nova-compute-* but not nova-compute-ironic-*) are
		// extracted; KVM and ironic hosts are dropped entirely.
		Hypervisors: []nova.Hypervisor{
			{ID: "uuid1", ServiceHost: "nova-compute-bb01", HypervisorType: "vcenter", RunningVMs: 5, State: "up", Status: "enabled"},
			{ID: "uuid2", ServiceHost: "node001-bb02", HypervisorType: "qemu", RunningVMs: 3, State: "down", Status: "enabled"},
			{ID: "uuid3", ServiceHost: "nova-compute-ironic-01", HypervisorType: "ironic", State: "up", Status: "enabled"},
			{ID: "uuid7", ServiceHost: "nova-compute-bb04", HypervisorType: "vcenter", RunningVMs: 1, State: "up", Status: "enabled"},
			{ID: "uuid8", ServiceHost: "nova-compute-bb05", HypervisorType: "vcenter", RunningVMs: 1, State: "up", Status: "enabled"},
			// Disabled via status.
			{ID: "uuid9", ServiceHost: "nova-compute-bb06", HypervisorType: "vcenter", RunningVMs: 2, State: "up", Status: "disabled", ServiceDisabledReason: new("example reason")},
			// Disabled via trait, which takes precedence over the down state.
			{ID: "uuid10", ServiceHost: "nova-compute-bb07", HypervisorType: "vcenter", RunningVMs: 2, State: "down", Status: "enabled", ServiceDisabledReason: new("example reason")},
			// Down without disabled reason.
			{ID: "uuid11", ServiceHost: "nova-compute-bb08", HypervisorType: "vcenter", State: "down", Status: "enabled"},
			{ID: "uuid12", ServiceHost: "nova-compute-bb09", HypervisorType: "vcenter", State: "up", Status: "enabled"},
		},
		Traits: []placement.Trait{
			{ResourceProviderUUID: "uuid1", Name: "CUSTOM_HW_SAPPHIRE_RAPIDS"},
			{ResourceProviderUUID: "uuid1", Name: "CUSTOM_HANA_EXCLUSIVE_HOST"},
			{ResourceProviderUUID: "uuid1", Name: "CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE"},
			{ResourceProviderUUID: "uuid2", Name: "CUSTOM_NUMASIZE_C48_M729"},
			{ResourceProviderUUID: "uuid3", Name: "TRAIT_IGNORED"},
			{ResourceProviderUUID: "uuid10", Name: "COMPUTE_STATUS_DISABLED"},
			// Traits must match exactly.
			{ResourceProviderUUID: "uuid12", Name: "CUSTOM_DECOMMISSIONING"},
			{ResourceProviderUUID: "uuid12", Name: "CUSTOM_HW_SAPPHIRE_RAPIDS_V2"},
			{ResourceProviderUUID: "uuid12", Name: "CUSTOM_HANA_EXCLUSIVE_HOST_LEGACY"},
			{ResourceProviderUUID: "uuid12", Name: "COMPUTE_STATUS_DISABLED_PENDING"},
		},
		InventoryUsages: []placement.InventoryUsage{
			// 3 pooled hosts of 4 TiB (4194304 MiB): (12582912 - 0) / 4194304 = 3 hosts.
			memory("uuid1", 12582912, 0, 4194304),
			{ResourceProviderUUID: "uuid1", InventoryClassName: "VCPU", Total: 100, MaxUnit: 10},
			// 2 pooled hosts of 512 GiB, reported in GiB since smaller than 1 TiB.
			memory("uuid7", 1048576, 0, 524288),
			// 1023.5 GiB rounds (half to even) to 1024 GiB and is reported as 1TiB.
			memory("uuid8", 1048064, 0, 1048064),
			// 2 pooled hosts with reserved memory: 2 hosts, 512 GiB + 1024 MiB / 2 = 512.5 GiB, rounded to 512GiB.
			memory("uuid11", 1049600, 1024, 524288),
			// 2.5 TiB rounds half to even to 2TiB.
			memory("uuid12", 2621440, 0, 2621440),
		},
		Aggregates: []nova.Aggregate{
			{UUID: "agg1", Name: "vc-a-0", ComputeHost: new("nova-compute-bb01")},
			{UUID: "agg2", Name: "some-other-aggregate", ComputeHost: new("nova-compute-bb01")},
			{UUID: "agg3", Name: "not-a-vcenter", ComputeHost: new("nova-compute-bb04")},
			// The lowest vc-* aggregate name is used.
			{UUID: "agg4", Name: "vc-b-1", ComputeHost: new("nova-compute-bb05")},
			{UUID: "agg5", Name: "vc-a-1", ComputeHost: new("nova-compute-bb05")},
		},
		PinnedProjects: []HostPinnedProjects{
			{ComputeHost: new("nova-compute-bb01"), Label: new("project-123")},
			{ComputeHost: new("nova-compute-bb01"), Label: new("project-456")},
			// Unrestricted hosts without label are ignored.
			{ComputeHost: new("nova-compute-bb04")},
		},
		HostAZs: []HostAZ{
			{AvailabilityZone: new("az1"), ComputeHost: "nova-compute-bb01"},
			{AvailabilityZone: new("az1"), ComputeHost: "nova-compute-bb04"},
			{AvailabilityZone: new("az1"), ComputeHost: "nova-compute-bb05"},
			{AvailabilityZone: new("az2"), ComputeHost: "nova-compute-bb06"},
			{AvailabilityZone: new("az2"), ComputeHost: "nova-compute-bb07"},
			{ComputeHost: "nova-compute-bb08"},
		},
	}
	features, err := (&VMwareHostDetailsExtractor{}).Extract(t.Context(), in)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	host := func(name string, modify func(*VMwareHostDetails)) VMwareHostDetails {
		d := VMwareHostDetails{
			ComputeHost:      name,
			AvailabilityZone: "unknown",
			CPUArchitecture:  "cascade-lake",
			WorkloadType:     "general-purpose",
			Enabled:          true,
			PhysicalHostSize: "unknown",
			VCenter:          "unknown",
		}
		modify(&d)
		return d
	}
	expected := []VMwareHostDetails{
		host("nova-compute-bb01", func(d *VMwareHostDetails) {
			d.AvailabilityZone = "az1"
			d.CPUArchitecture = "sapphire-rapids"
			d.WorkloadType = "hana"
			d.ExternalCustomer = true
			d.RunningVMs = 5
			d.PinnedProjects = new("project-123,project-456")
			d.PhysicalHostSize = "4TiB"
			d.VCenter = "vc-a-0"
		}),
		host("nova-compute-bb04", func(d *VMwareHostDetails) {
			d.AvailabilityZone = "az1"
			d.RunningVMs = 1
			d.PhysicalHostSize = "512GiB"
		}),
		host("nova-compute-bb05", func(d *VMwareHostDetails) {
			d.AvailabilityZone = "az1"
			d.RunningVMs = 1
			d.PhysicalHostSize = "1TiB"
			d.VCenter = "vc-a-1"
		}),
		host("nova-compute-bb06", func(d *VMwareHostDetails) {
			d.AvailabilityZone = "az2"
			d.Enabled = false
			d.DisabledReason = new("[disabled] example reason")
			d.RunningVMs = 2
		}),
		host("nova-compute-bb07", func(d *VMwareHostDetails) {
			d.AvailabilityZone = "az2"
			d.Enabled = false
			d.DisabledReason = new("[disabled] example reason")
			d.RunningVMs = 2
		}),
		host("nova-compute-bb08", func(d *VMwareHostDetails) {
			d.Enabled = false
			d.DisabledReason = new("[down] --")
			d.PhysicalHostSize = "512GiB"
		}),
		host("nova-compute-bb09", func(d *VMwareHostDetails) {
			d.Decommissioned = true
			d.PhysicalHostSize = "2TiB"
		}),
	}
	if len(features) != len(expected) {
		t.Fatalf("expected %d host details, got %d", len(expected), len(features))
	}
	for i := range expected {
		if !reflect.DeepEqual(features[i], expected[i]) {
			t.Errorf("mismatch for host %s:\nexpected: %+v\ngot:      %+v", expected[i].ComputeHost, expected[i], features[i])
		}
	}
}
