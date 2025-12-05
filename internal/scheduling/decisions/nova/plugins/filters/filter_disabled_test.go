// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/openstack/placement"
	"github.com/cobaltcore-dev/cortex/pkg/db"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"

	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
	testlib "github.com/cobaltcore-dev/cortex/pkg/testing"
)

func TestFilterDisabledStep_Run(t *testing.T) {
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
		&nova.Hypervisor{ID: "hv1", Hostname: "hypervisor1", State: "up", Status: "enabled", HypervisorType: "QEMU", HypervisorVersion: 2008000, HostIP: "192.168.1.1", ServiceID: "svc1", ServiceHost: "host1", ServiceDisabledReason: nil, VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: "{}"},
		&nova.Hypervisor{ID: "hv2", Hostname: "hypervisor2", State: "up", Status: "disabled", HypervisorType: "QEMU", HypervisorVersion: 2008000, HostIP: "192.168.1.2", ServiceID: "svc2", ServiceHost: "host2", ServiceDisabledReason: testlib.Ptr("maintenance"), VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: "{}"},
		&nova.Hypervisor{ID: "hv3", Hostname: "hypervisor3", State: "down", Status: "enabled", HypervisorType: "QEMU", HypervisorVersion: 2008000, HostIP: "192.168.1.3", ServiceID: "svc3", ServiceHost: "host3", ServiceDisabledReason: nil, VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: "{}"},
		&nova.Hypervisor{ID: "hv4", Hostname: "hypervisor4", State: "up", Status: "enabled", HypervisorType: "QEMU", HypervisorVersion: 2008000, HostIP: "192.168.1.4", ServiceID: "svc4", ServiceHost: "host4", ServiceDisabledReason: nil, VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: "{}"},
		&nova.Hypervisor{ID: "hv5", Hostname: "hypervisor5", State: "up", Status: "enabled", HypervisorType: "QEMU", HypervisorVersion: 2008000, HostIP: "192.168.1.5", ServiceID: "svc5", ServiceHost: "host5", ServiceDisabledReason: nil, VCPUs: 16, MemoryMB: 32768, LocalGB: 1000, VCPUsUsed: 4, MemoryMBUsed: 8192, LocalGBUsed: 100, FreeRAMMB: 24576, FreeDiskGB: 900, CurrentWorkload: 0, RunningVMs: 2, DiskAvailableLeast: &[]int{900}[0], CPUInfo: "{}"},
	}
	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock trait data
	traits := []any{
		&placement.Trait{ResourceProviderUUID: "hv1", Name: "COMPUTE_STATUS_ENABLED", ResourceProviderGeneration: 1},
		&placement.Trait{ResourceProviderUUID: "hv2", Name: "COMPUTE_STATUS_DISABLED", ResourceProviderGeneration: 1},
		&placement.Trait{ResourceProviderUUID: "hv3", Name: "COMPUTE_STATUS_ENABLED", ResourceProviderGeneration: 1},
		&placement.Trait{ResourceProviderUUID: "hv4", Name: "COMPUTE_STATUS_ENABLED", ResourceProviderGeneration: 1},
		&placement.Trait{ResourceProviderUUID: "hv5", Name: "COMPUTE_STATUS_DISABLED", ResourceProviderGeneration: 1},
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
			name: "Filter enabled hosts only",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						AvailabilityZone: "az-1",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host4"}, // Only enabled, up hosts without COMPUTE_STATUS_DISABLED trait
			filteredHosts: []string{"host2", "host3", "host5"},
		},
		{
			name: "All hosts disabled or down",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						AvailabilityZone: "az-1",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"}, // disabled
					{ComputeHost: "host3"}, // down
					{ComputeHost: "host5"}, // has COMPUTE_STATUS_DISABLED trait
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host2", "host3", "host5"},
		},
		{
			name: "Only enabled hosts",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						AvailabilityZone: "az-1",
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host4"},
			filteredHosts: []string{},
		},
		{
			name: "Empty host list",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						AvailabilityZone: "az-1",
					},
				},
				Hosts: []api.ExternalSchedulerHost{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Host not in database",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						AvailabilityZone: "az-1",
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
			step := &FilterDisabledStep{}
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
