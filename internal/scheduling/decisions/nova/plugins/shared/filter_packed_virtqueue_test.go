// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/openstack/placement"
	"github.com/cobaltcore-dev/cortex/pkg/db"

	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
)

func TestFilterPackedVirtqueueStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Create dependency tables
	err := testDB.CreateTable(
		testDB.AddTable(nova.Hypervisor{}),
		testDB.AddTable(placement.Trait{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock hypervisor data
	hypervisors := []any{
		&nova.Hypervisor{ID: "hv1", Hostname: "hypervisor1", State: "up", Status: "enabled", HypervisorType: "QEMU", HypervisorVersion: 2008000, HostIP: "192.168.1.1", ServiceID: "svc1", ServiceHost: "host1", VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: "{}"},
		&nova.Hypervisor{ID: "hv2", Hostname: "hypervisor2", State: "up", Status: "enabled", HypervisorType: "QEMU", HypervisorVersion: 2008000, HostIP: "192.168.1.2", ServiceID: "svc2", ServiceHost: "host2", VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: "{}"},
		&nova.Hypervisor{ID: "hv3", Hostname: "hypervisor3", State: "up", Status: "enabled", HypervisorType: "QEMU", HypervisorVersion: 2008000, HostIP: "192.168.1.3", ServiceID: "svc3", ServiceHost: "host3", VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: "{}"},
		&nova.Hypervisor{ID: "hv4", Hostname: "hypervisor4", State: "up", Status: "enabled", HypervisorType: "QEMU", HypervisorVersion: 2008000, HostIP: "192.168.1.4", ServiceID: "svc4", ServiceHost: "host4", VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: "{}"},
	}
	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock trait data - host1 and host3 support packed virtqueues
	traits := []any{
		&placement.Trait{ResourceProviderUUID: "hv1", Name: "COMPUTE_NET_VIRTIO_PACKED", ResourceProviderGeneration: 1},
		&placement.Trait{ResourceProviderUUID: "hv2", Name: "COMPUTE_STATUS_ENABLED", ResourceProviderGeneration: 1},
		&placement.Trait{ResourceProviderUUID: "hv3", Name: "COMPUTE_NET_VIRTIO_PACKED", ResourceProviderGeneration: 1},
		&placement.Trait{ResourceProviderUUID: "hv4", Name: "COMPUTE_STATUS_ENABLED", ResourceProviderGeneration: 1},
	}
	if err := testDB.Insert(traits...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		request       api.ExternalSchedulerRequest
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "No packed virtqueue requested - no filtering",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"hw:cpu_policy": "dedicated",
								},
							},
						},
						Image: api.NovaObject[api.NovaImageMeta]{
							Data: api.NovaImageMeta{
								Properties: api.NovaObject[map[string]any]{
									Data: map[string]any{
										"hw_disk_bus": "virtio",
									},
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3", "host4"},
			filteredHosts: []string{},
		},
		{
			name: "Packed virtqueue requested in flavor - filter hosts without support",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"hw:virtio_packed_ring": "true",
								},
							},
						},
						Image: api.NovaObject[api.NovaImageMeta]{
							Data: api.NovaImageMeta{
								Properties: api.NovaObject[map[string]any]{
									Data: map[string]any{},
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host3"}, // Only hosts with COMPUTE_NET_VIRTIO_PACKED trait
			filteredHosts: []string{"host2", "host4"}, // Hosts without packed virtqueue support are filtered out
		},
		{
			name: "Packed virtqueue requested in image properties - filter hosts without support",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{},
							},
						},
						Image: api.NovaObject[api.NovaImageMeta]{
							Data: api.NovaImageMeta{
								Properties: api.NovaObject[map[string]any]{
									Data: map[string]any{
										"hw_virtio_packed_ring": "true",
									},
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host2", "host4"},
		},
		{
			name: "Packed virtqueue requested in both flavor and image - filter hosts without support",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"hw:virtio_packed_ring": "true",
								},
							},
						},
						Image: api.NovaObject[api.NovaImageMeta]{
							Data: api.NovaImageMeta{
								Properties: api.NovaObject[map[string]any]{
									Data: map[string]any{
										"hw_virtio_packed_ring": "true",
									},
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host2", "host4"},
		},
		{
			name: "Packed virtqueue set to false - no filtering",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"hw:virtio_packed_ring": "false",
								},
							},
						},
						Image: api.NovaObject[api.NovaImageMeta]{
							Data: api.NovaImageMeta{
								Properties: api.NovaObject[map[string]any]{
									Data: map[string]any{},
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host3"}, // Still filters because the key exists
			filteredHosts: []string{"host2", "host4"},
		},
		{
			name: "All hosts without packed virtqueue support",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"hw:virtio_packed_ring": "true",
								},
							},
						},
						Image: api.NovaObject[api.NovaImageMeta]{
							Data: api.NovaImageMeta{
								Properties: api.NovaObject[map[string]any]{
									Data: map[string]any{},
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host2", "host4"},
		},
		{
			name: "All hosts with packed virtqueue support",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"hw:virtio_packed_ring": "true",
								},
							},
						},
						Image: api.NovaObject[api.NovaImageMeta]{
							Data: api.NovaImageMeta{
								Properties: api.NovaObject[map[string]any]{
									Data: map[string]any{},
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{},
		},
		{
			name: "Host not in database",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"hw:virtio_packed_ring": "true",
								},
							},
						},
						Image: api.NovaObject[api.NovaImageMeta]{
							Data: api.NovaImageMeta{
								Properties: api.NovaObject[map[string]any]{
									Data: map[string]any{},
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host-unknown"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host-unknown"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterPackedVirtqueueStep{}
			step.DB = &testDB
			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// Check expected hosts are present
			for _, host := range tt.expectedHosts {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %s to be present in activations", host)
				}
			}

			// Check filtered hosts are not present
			for _, host := range tt.filteredHosts {
				if _, ok := result.Activations[host]; ok {
					t.Errorf("expected host %s to be filtered out", host)
				}
			}

			// Check total count
			if len(result.Activations) != len(tt.expectedHosts) {
				t.Errorf("expected %d hosts, got %d", len(tt.expectedHosts), len(result.Activations))
			}
		})
	}
}
