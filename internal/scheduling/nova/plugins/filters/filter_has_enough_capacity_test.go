// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	th "github.com/cobaltcore-dev/cortex/internal/scheduling/nova/testhelpers"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFilterHasEnoughCapacityOpts_Validate(t *testing.T) {
	tests := []struct {
		name        string
		opts        FilterHasEnoughCapacityOpts
		expectError bool
	}{
		{"valid options with lock reserved true", FilterHasEnoughCapacityOpts{LockReserved: true}, false},
		{"valid options with lock reserved false", FilterHasEnoughCapacityOpts{LockReserved: false}, false},
		{"valid options with default values", FilterHasEnoughCapacityOpts{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

func TestFilterHasEnoughCapacity_ReservationTypes(t *testing.T) {
	scheme := th.BuildTestScheme(t)

	// 4 hypervisors: 3 with capacity, 1 without
	// host1: 8 CPU free, 16Gi free | host2: 4 CPU free, 8Gi free | host3: 16 CPU free, 32Gi free | host4: 0 free
	hvs := []th.HypervisorArgs{
		{Name: "host1", CPUCap: "16", CPUAlloc: "8", MemCap: "32Gi", MemAlloc: "16Gi"},
		{Name: "host2", CPUCap: "8", CPUAlloc: "4", MemCap: "16Gi", MemAlloc: "8Gi"},
		{Name: "host3", CPUCap: "32", CPUAlloc: "16", MemCap: "64Gi", MemAlloc: "32Gi"},
		{Name: "host4", CPUCap: "8", CPUAlloc: "8", MemCap: "16Gi", MemAlloc: "16Gi"}, // no capacity
	}

	tests := []struct {
		name          string
		reservations  []th.ReservationArgs
		request       th.NovaRequestArgs
		opts          FilterHasEnoughCapacityOpts
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "CommittedResourceReservation blocks some hosts when project/flavor don't match",
			reservations: []th.ReservationArgs{
				{Name: "res-1", TargetHost: "host1", ProjectID: "project-A", ResourceName: "m1.large", CPU: "8", Memory: "16Gi"},
				{Name: "res-2", TargetHost: "host2", ProjectID: "project-A", ResourceName: "m1.large", CPU: "4", Memory: "8Gi"},
			},
			request: th.NovaRequestArgs{
				ProjectID: "project-B", InstanceUUID: "instance-123", FlavorName: "m1.small", VCPUs: 4, Memory: "8Gi",
				Hosts: []string{"host1", "host2", "host3", "host4"},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "CommittedResourceReservation unlocks all reserved hosts when project and flavor match",
			reservations: []th.ReservationArgs{
				{Name: "res-1", TargetHost: "host1", ProjectID: "project-A", ResourceName: "m1.large", CPU: "4", Memory: "8Gi"},
				{Name: "res-2", TargetHost: "host2", ProjectID: "project-A", ResourceName: "m1.large", CPU: "4", Memory: "8Gi"},
			},
			request: th.NovaRequestArgs{
				ProjectID: "project-A", InstanceUUID: "instance-123", FlavorName: "m1.large", VCPUs: 4, Memory: "8Gi",
				Hosts: []string{"host1", "host2", "host3", "host4"},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			name: "CommittedResourceReservation stays locked when LockReserved is true",
			reservations: []th.ReservationArgs{
				{Name: "res-1", TargetHost: "host1", ProjectID: "project-A", ResourceName: "m1.large", CPU: "8", Memory: "16Gi"},
				{Name: "res-2", TargetHost: "host3", ProjectID: "project-A", ResourceName: "m1.large", CPU: "16", Memory: "32Gi"},
			},
			request: th.NovaRequestArgs{
				ProjectID: "project-A", InstanceUUID: "instance-123", FlavorName: "m1.large", VCPUs: 4, Memory: "8Gi",
				Hosts: []string{"host1", "host2", "host3", "host4"},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: true},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{"host1", "host3", "host4"},
		},
		{
			name: "Empty reservation type defaults to CommittedResourceReservation behavior",
			reservations: []th.ReservationArgs{
				{
					Name: "legacy-res", TargetHost: "host1", ObservedHost: "host1",
					ProjectID: "project-A", ResourceName: "m1.large", CPU: "4", Memory: "8Gi",
					Type: v1alpha1.ReservationTypeCommittedResource,
				},
			},
			request: th.NovaRequestArgs{
				ProjectID: "project-A", InstanceUUID: "instance-123", FlavorName: "m1.large", VCPUs: 4, Memory: "8Gi",
				Hosts: []string{"host1", "host2"},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name: "FailoverReservation blocks hosts for non-evacuation request even when instance is in Allocations",
			reservations: []th.ReservationArgs{
				{Name: "failover-1", TargetHost: "host1", CPU: "8", Memory: "16Gi", Type: v1alpha1.ReservationTypeFailover, Allocations: map[string]string{"instance-123": "host5"}},
				{Name: "failover-2", TargetHost: "host2", CPU: "4", Memory: "8Gi", Type: v1alpha1.ReservationTypeFailover, Allocations: map[string]string{"instance-123": "host6"}},
			},
			request: th.NovaRequestArgs{
				ProjectID: "project-A", InstanceUUID: "instance-123", FlavorName: "m1.large", VCPUs: 4, Memory: "8Gi",
				Evacuation: false, Hosts: []string{"host1", "host2", "host3", "host4"},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "FailoverReservation unlocks hosts during evacuation when instance is in Allocations",
			reservations: []th.ReservationArgs{
				{Name: "failover-1", TargetHost: "host1", CPU: "4", Memory: "8Gi", Type: v1alpha1.ReservationTypeFailover, Allocations: map[string]string{"instance-123": "host5"}},
				{Name: "failover-2", TargetHost: "host2", CPU: "4", Memory: "8Gi", Type: v1alpha1.ReservationTypeFailover, Allocations: map[string]string{"instance-123": "host6"}},
			},
			request: th.NovaRequestArgs{
				ProjectID: "project-A", InstanceUUID: "instance-123", FlavorName: "m1.large", VCPUs: 4, Memory: "8Gi",
				Evacuation: true, Hosts: []string{"host1", "host2", "host3", "host4"},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			name: "FailoverReservation blocks hosts during evacuation when instance not in Allocations",
			reservations: []th.ReservationArgs{
				{Name: "failover-1", TargetHost: "host1", CPU: "8", Memory: "16Gi", Type: v1alpha1.ReservationTypeFailover, Allocations: map[string]string{"other-instance": "host5"}},
				{Name: "failover-2", TargetHost: "host2", CPU: "4", Memory: "8Gi", Type: v1alpha1.ReservationTypeFailover, Allocations: map[string]string{"another-instance": "host6"}},
			},
			request: th.NovaRequestArgs{
				ProjectID: "project-A", InstanceUUID: "instance-123", FlavorName: "m1.large", VCPUs: 4, Memory: "8Gi",
				Evacuation: true, Hosts: []string{"host1", "host2", "host3", "host4"},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "FailoverReservation with empty Allocations blocks reserved host",
			reservations: []th.ReservationArgs{
				{Name: "failover-1", TargetHost: "host1", CPU: "8", Memory: "16Gi", Type: v1alpha1.ReservationTypeFailover, Allocations: map[string]string{}},
			},
			request: th.NovaRequestArgs{
				ProjectID: "project-A", InstanceUUID: "instance-123", FlavorName: "m1.large", VCPUs: 4, Memory: "8Gi",
				Evacuation: true, Hosts: []string{"host1", "host2", "host3"},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host2", "host3"},
			filteredHosts: []string{"host1"},
		},
		{
			name: "FailoverReservation with multiple instances in Allocations unlocks for matching instance during evacuation",
			reservations: []th.ReservationArgs{
				{Name: "failover-1", TargetHost: "host1", CPU: "4", Memory: "8Gi", Type: v1alpha1.ReservationTypeFailover, Allocations: map[string]string{"instance-111": "host5", "instance-222": "host6", "instance-333": "host7"}},
			},
			request: th.NovaRequestArgs{
				ProjectID: "project-A", InstanceUUID: "instance-222", FlavorName: "m1.large", VCPUs: 4, Memory: "8Gi",
				Evacuation: true, Hosts: []string{"host1", "host2", "host3", "host4"},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			name:         "No reservations - all hosts with capacity pass",
			reservations: []th.ReservationArgs{},
			request: th.NovaRequestArgs{
				ProjectID: "project-A", InstanceUUID: "instance-123", FlavorName: "m1.small", VCPUs: 4, Memory: "8Gi",
				Hosts: []string{"host1", "host2", "host3", "host4"},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			name: "All hosts blocked by reservations - none pass",
			reservations: []th.ReservationArgs{
				{Name: "res-1", TargetHost: "host1", ProjectID: "project-X", ResourceName: "m1.xlarge", CPU: "8", Memory: "16Gi"},
				{Name: "res-2", TargetHost: "host2", ProjectID: "project-X", ResourceName: "m1.xlarge", CPU: "4", Memory: "8Gi"},
				{Name: "res-3", TargetHost: "host3", ProjectID: "project-X", ResourceName: "m1.xlarge", CPU: "16", Memory: "32Gi"},
			},
			request: th.NovaRequestArgs{
				ProjectID: "project-A", InstanceUUID: "instance-123", FlavorName: "m1.small", VCPUs: 4, Memory: "8Gi",
				Hosts: []string{"host1", "host2", "host3", "host4"},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2", "host3", "host4"},
		},
		{
			name: "Pending reservation (only TargetHost set) blocks capacity on desired host",
			reservations: []th.ReservationArgs{
				{
					Name: "pending-res", TargetHost: "host1", ObservedHost: "",
					ProjectID: "project-X", ResourceName: "m1.large", CPU: "8", Memory: "16Gi",
					Type: v1alpha1.ReservationTypeCommittedResource,
				},
			},
			request: th.NovaRequestArgs{
				ProjectID: "project-A", InstanceUUID: "instance-123", FlavorName: "m1.small", VCPUs: 4, Memory: "8Gi",
				Hosts: []string{"host1", "host2", "host3", "host4"},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host2", "host3"}, // host1 blocked by pending reservation
			filteredHosts: []string{"host1", "host4"},
		},
		{
			name: "Reservation with different TargetHost and ObservedHost blocks BOTH hosts",
			reservations: []th.ReservationArgs{
				{
					Name: "moved-res", TargetHost: "host1", ObservedHost: "host2",
					ProjectID: "project-X", ResourceName: "m1.large", CPU: "4", Memory: "8Gi",
					Type: v1alpha1.ReservationTypeCommittedResource,
				},
			},
			request: th.NovaRequestArgs{
				ProjectID: "project-A", InstanceUUID: "instance-123", FlavorName: "m1.small", VCPUs: 4, Memory: "8Gi",
				Hosts: []string{"host1", "host2", "host3", "host4"},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host3"}, // host1 still has capacity (4 CPU), host2 blocked (0 CPU)
			filteredHosts: []string{"host2", "host4"},
		},
		{
			name: "Multiple reservations: pending and placed block different hosts",
			reservations: []th.ReservationArgs{
				{
					Name: "pending-res", TargetHost: "host1", ObservedHost: "",
					ProjectID: "project-X", ResourceName: "m1.large", CPU: "8", Memory: "16Gi",
					Type: v1alpha1.ReservationTypeCommittedResource,
				},
				{
					Name: "placed-res", TargetHost: "host2", ObservedHost: "host3",
					ProjectID: "project-X", ResourceName: "m1.large", CPU: "4", Memory: "8Gi",
					Type: v1alpha1.ReservationTypeCommittedResource,
				},
			},
			request: th.NovaRequestArgs{
				ProjectID: "project-A", InstanceUUID: "instance-123", FlavorName: "m1.small", VCPUs: 4, Memory: "8Gi",
				Hosts: []string{"host1", "host2", "host3", "host4"},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"}, // host1 blocked by pending, host2 blocked by placed, host3 still has capacity
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "Reservation with no host is skipped",
			reservations: []th.ReservationArgs{
				{
					Name: "no-host-res", TargetHost: "", ObservedHost: "",
					ProjectID: "project-X", ResourceName: "m1.large", CPU: "8", Memory: "16Gi",
					Type: v1alpha1.ReservationTypeCommittedResource,
				},
			},
			request: th.NovaRequestArgs{
				ProjectID: "project-A", InstanceUUID: "instance-123", FlavorName: "m1.small", VCPUs: 4, Memory: "8Gi",
				Hosts: []string{"host1", "host2", "host3", "host4"},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(hvs)+len(tt.reservations))
			for _, h := range hvs {
				objects = append(objects, th.NewHypervisor(h))
			}
			for _, r := range th.NewReservations(tt.reservations) {
				objects = append(objects, r)
			}

			step := &FilterHasEnoughCapacity{}
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			step.Options = tt.opts

			result, err := step.Run(slog.Default(), th.NewNovaRequest(tt.request))
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			for _, host := range tt.expectedHosts {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %s to be present in activations", host)
				}
			}

			for _, host := range tt.filteredHosts {
				if _, ok := result.Activations[host]; ok {
					t.Errorf("expected host %s to be filtered out", host)
				}
			}
		})
	}
}
