// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/placement"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestFilterPackedVirtqueueStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
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
	_, err = testDB.Exec(`
		INSERT INTO openstack_hypervisors (id, hostname, state, status, hypervisor_type, hypervisor_version, host_ip, service_id, service_host, service_disabled_reason, vcpus, memory_mb, local_gb, vcpus_used, memory_mb_used, local_gb_used, free_ram_mb, free_disk_gb, current_workload, running_vms, disk_available_least, cpu_info)
		VALUES
			('hv1', 'hypervisor1', 'up', 'enabled', 'QEMU', 2008000, '192.168.1.1', 'svc1', 'host1', NULL, 16, 32768, 1000, 4, 8192, 100, 24576, 900, 0, 2, 900, '{}'),
			('hv2', 'hypervisor2', 'up', 'enabled', 'QEMU', 2008000, '192.168.1.2', 'svc2', 'host2', NULL, 16, 32768, 1000, 4, 8192, 100, 24576, 900, 0, 2, 900, '{}'),
			('hv3', 'hypervisor3', 'up', 'enabled', 'QEMU', 2008000, '192.168.1.3', 'svc3', 'host3', NULL, 16, 32768, 1000, 4, 8192, 100, 24576, 900, 0, 2, 900, '{}'),
			('hv4', 'hypervisor4', 'up', 'enabled', 'QEMU', 2008000, '192.168.1.4', 'svc4', 'host4', NULL, 16, 32768, 1000, 4, 8192, 100, 24576, 900, 0, 2, 900, '{}')
	`)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock trait data - host1 and host3 support packed virtqueues
	_, err = testDB.Exec(`
		INSERT INTO openstack_resource_provider_traits (resource_provider_uuid, name, resource_provider_generation)
		VALUES
			('hv1', 'COMPUTE_NET_VIRTIO_PACKED', 1),
			('hv2', 'COMPUTE_STATUS_ENABLED', 1),
			('hv3', 'COMPUTE_NET_VIRTIO_PACKED', 1),
			('hv4', 'COMPUTE_STATUS_ENABLED', 1)
	`)
	if err != nil {
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
			if err := step.Init("", testDB, conf.NewRawOpts("{}")); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
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
