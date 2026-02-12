// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

// Test helpers to reduce boilerplate

func buildTestScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := hv1.SchemeBuilder.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hv1 scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 scheme: %v", err)
	}
	return scheme
}

func newHypervisor(name, cpuCap, cpuAlloc, memCap, memAlloc string) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: hv1.HypervisorStatus{
			Capacity:   map[string]resource.Quantity{"cpu": resource.MustParse(cpuCap), "memory": resource.MustParse(memCap)},
			Allocation: map[string]resource.Quantity{"cpu": resource.MustParse(cpuAlloc), "memory": resource.MustParse(memAlloc)},
		},
	}
}

// newReservation creates a reservation with configurable Spec.TargetHost and Status.ObservedHost.
// Use specHost="" or observedHost="" to leave them unset.
func newReservation(name, specHost, observedHost, projectID, flavorName, cpu, mem string, resType v1alpha1.ReservationType, connectTo map[string]string) *v1alpha1.Reservation {
	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReservationSpec{
			Type:       resType,
			TargetHost: specHost,
			Resources:  map[string]resource.Quantity{"cpu": resource.MustParse(cpu), "memory": resource.MustParse(mem)},
		},
		Status: v1alpha1.ReservationStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.ReservationConditionReady,
					Status: metav1.ConditionTrue,
					Reason: "ReservationActive",
				},
			},
			ObservedHost: observedHost,
		},
	}

	// Set type-specific fields
	switch resType {
	case v1alpha1.ReservationTypeCommittedResource, "":
		res.Spec.CommittedResourceReservation = &v1alpha1.CommittedResourceReservationSpec{
			ProjectID:    projectID,
			ResourceName: flavorName,
		}
	case v1alpha1.ReservationTypeFailover:
		res.Spec.FailoverReservation = &v1alpha1.FailoverReservationSpec{}
		if connectTo != nil {
			res.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{
				Allocations: connectTo,
			}
		}
	}

	return res
}

// Convenience wrappers for common reservation types
func newCommittedReservation(name, host, projectID, flavorName, cpu, mem string) *v1alpha1.Reservation {
	return newReservation(name, host, host, projectID, flavorName, cpu, mem, v1alpha1.ReservationTypeCommittedResource, nil)
}

//nolint:unparam // projectID kept as parameter for test readability and consistency with newCommittedReservation
func newFailoverReservation(name, host, projectID, flavorName, cpu, mem string, usedBy map[string]string) *v1alpha1.Reservation {
	return newReservation(name, host, host, projectID, flavorName, cpu, mem, v1alpha1.ReservationTypeFailover, usedBy)
}

//nolint:unparam // vcpus kept as parameter for test readability
func newRequest(projectID, instanceUUID, flavorName string, vcpus, memMB int, evacuation bool, hosts ...string) api.ExternalSchedulerRequest {
	hostList := make([]api.ExternalSchedulerHost, len(hosts))
	for i, h := range hosts {
		hostList[i] = api.ExternalSchedulerHost{ComputeHost: h}
	}
	spec := api.NovaSpec{
		ProjectID: projectID, InstanceUUID: instanceUUID, NumInstances: 1,
		Flavor: api.NovaObject[api.NovaFlavor]{Data: api.NovaFlavor{Name: flavorName, VCPUs: uint64(vcpus), MemoryMB: uint64(memMB)}}, //nolint:gosec // test code
	}
	if evacuation {
		spec.SchedulerHints = map[string]any{"_nova_check_type": []any{"evacuate"}}
	}
	return api.ExternalSchedulerRequest{
		Spec:  api.NovaObject[api.NovaSpec]{Data: spec},
		Hosts: hostList,
	}
}

func TestFilterHasEnoughCapacity_ReservationTypes(t *testing.T) {
	scheme := buildTestScheme(t)

	// 4 hypervisors: 3 with capacity, 1 without
	// host1: 8 CPU free, 16Gi free | host2: 4 CPU free, 8Gi free | host3: 16 CPU free, 32Gi free | host4: 0 free
	hvs := []client.Object{
		newHypervisor("host1", "16", "8", "32Gi", "16Gi"),
		newHypervisor("host2", "8", "4", "16Gi", "8Gi"),
		newHypervisor("host3", "32", "16", "64Gi", "32Gi"),
		newHypervisor("host4", "8", "8", "16Gi", "16Gi"), // no capacity
	}

	tests := []struct {
		name          string
		reservations  []client.Object
		request       api.ExternalSchedulerRequest
		opts          FilterHasEnoughCapacityOpts
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "CommittedResourceReservation blocks some hosts when project/flavor don't match",
			reservations: []client.Object{
				newCommittedReservation("res-1", "host1", "project-A", "m1.large", "8", "16Gi"),
				newCommittedReservation("res-2", "host2", "project-A", "m1.large", "4", "8Gi"),
			},
			request:       newRequest("project-B", "instance-123", "m1.small", 4, 8000, false, "host1", "host2", "host3", "host4"),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "CommittedResourceReservation unlocks all reserved hosts when project and flavor match",
			reservations: []client.Object{
				newCommittedReservation("res-1", "host1", "project-A", "m1.large", "4", "8Gi"),
				newCommittedReservation("res-2", "host2", "project-A", "m1.large", "4", "8Gi"),
			},
			request:       newRequest("project-A", "instance-123", "m1.large", 4, 8000, false, "host1", "host2", "host3", "host4"),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			name: "CommittedResourceReservation stays locked when LockReserved is true",
			reservations: []client.Object{
				newCommittedReservation("res-1", "host1", "project-A", "m1.large", "8", "16Gi"),
				newCommittedReservation("res-2", "host3", "project-A", "m1.large", "16", "32Gi"),
			},
			request:       newRequest("project-A", "instance-123", "m1.large", 4, 8000, false, "host1", "host2", "host3", "host4"),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: true},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{"host1", "host3", "host4"},
		},
		{
			name: "Empty reservation type defaults to CommittedResourceReservation behavior",
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{Name: "legacy-res"},
					Spec: v1alpha1.ReservationSpec{
						TargetHost: "host1",
						Resources:  map[string]resource.Quantity{"cpu": resource.MustParse("04"), "memory": resource.MustParse("08Gi")},
						CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
							ProjectID:    "project-A",
							ResourceName: "m1.large",
						},
					},
					Status: v1alpha1.ReservationStatus{
						Conditions: []metav1.Condition{
							{
								Type:   v1alpha1.ReservationConditionReady,
								Status: metav1.ConditionTrue,
								Reason: "ReservationActive",
							},
						},
						ObservedHost: "host1",
					},
				},
			},
			request:       newRequest("project-A", "instance-123", "m1.large", 4, 8000, false, "host1", "host2"),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name: "FailoverReservation blocks hosts for non-evacuation request even when instance is in UsedBy",
			reservations: []client.Object{
				newFailoverReservation("failover-1", "host1", "project-A", "m1.large", "8", "16Gi", map[string]string{"instance-123": "host5"}),
				newFailoverReservation("failover-2", "host2", "project-A", "m1.large", "4", "8Gi", map[string]string{"instance-123": "host6"}),
			},
			request:       newRequest("project-A", "instance-123", "m1.large", 4, 8000, false, "host1", "host2", "host3", "host4"),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"}, // Failover reservations stay locked for non-evacuation
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "FailoverReservation unlocks hosts during evacuation when instance is in UsedBy",
			reservations: []client.Object{
				newFailoverReservation("failover-1", "host1", "project-A", "m1.large", "4", "8Gi", map[string]string{"instance-123": "host5"}),
				newFailoverReservation("failover-2", "host2", "project-A", "m1.large", "4", "8Gi", map[string]string{"instance-123": "host6"}),
			},
			request:       newRequest("project-A", "instance-123", "m1.large", 4, 8000, true, "host1", "host2", "host3", "host4"),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"}, // Unlocked during evacuation
			filteredHosts: []string{"host4"},
		},
		{
			name: "FailoverReservation blocks hosts during evacuation when instance not in UsedBy",
			reservations: []client.Object{
				newFailoverReservation("failover-1", "host1", "project-A", "m1.large", "8", "16Gi", map[string]string{"other-instance": "host5"}),
				newFailoverReservation("failover-2", "host2", "project-A", "m1.large", "4", "8Gi", map[string]string{"another-instance": "host6"}),
			},
			request:       newRequest("project-A", "instance-123", "m1.large", 4, 8000, true, "host1", "host2", "host3", "host4"),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "FailoverReservation with empty UsedBy blocks reserved host",
			reservations: []client.Object{
				newFailoverReservation("failover-1", "host1", "project-A", "m1.large", "8", "16Gi", map[string]string{}),
			},
			request:       newRequest("project-A", "instance-123", "m1.large", 4, 8000, true, "host1", "host2", "host3"),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host2", "host3"},
			filteredHosts: []string{"host1"},
		},
		{
			name: "FailoverReservation with multiple instances in UsedBy unlocks for matching instance during evacuation",
			reservations: []client.Object{
				newFailoverReservation("failover-1", "host1", "project-A", "m1.large", "4", "8Gi", map[string]string{"instance-111": "host5", "instance-222": "host6", "instance-333": "host7"}),
			},
			request:       newRequest("project-A", "instance-222", "m1.large", 4, 8000, true, "host1", "host2", "host3", "host4"),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			name:          "No reservations - all hosts with capacity pass",
			reservations:  []client.Object{},
			request:       newRequest("project-A", "instance-123", "m1.small", 4, 8000, false, "host1", "host2", "host3", "host4"),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			name: "All hosts blocked by reservations - none pass",
			reservations: []client.Object{
				newCommittedReservation("res-1", "host1", "project-X", "m1.xlarge", "8", "16Gi"),
				newCommittedReservation("res-2", "host2", "project-X", "m1.xlarge", "4", "8Gi"),
				newCommittedReservation("res-3", "host3", "project-X", "m1.xlarge", "16", "32Gi"),
			},
			request:       newRequest("project-A", "instance-123", "m1.small", 4, 8000, false, "host1", "host2", "host3", "host4"),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2", "host3", "host4"},
		},
		// ============================================================================
		// Tests for Spec.Host vs Status.ObservedHost behavior
		// ============================================================================
		{
			name: "Pending reservation (only Spec.Host set) blocks capacity on desired host",
			reservations: []client.Object{
				// Pending reservation: Spec.Host is set, but ObservedHost is empty
				newReservation("pending-res", "host1", "", "project-X", "m1.large", "8", "16Gi", v1alpha1.ReservationTypeCommittedResource, nil),
			},
			request:       newRequest("project-A", "instance-123", "m1.small", 4, 8000, false, "host1", "host2", "host3", "host4"),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host2", "host3"}, // host1 blocked by pending reservation
			filteredHosts: []string{"host1", "host4"},
		},
		{
			name: "Reservation with different Spec.Host and ObservedHost blocks BOTH hosts",
			reservations: []client.Object{
				// Reservation was requested for host1 but placed on host2 - blocks BOTH
				// host1: 8 CPU free - 4 CPU reserved = 4 CPU free (still has capacity for 4 CPU request)
				// host2: 4 CPU free - 4 CPU reserved = 0 CPU free (blocked)
				newReservation("moved-res", "host1", "host2", "project-X", "m1.large", "4", "8Gi", v1alpha1.ReservationTypeCommittedResource, nil),
			},
			request:       newRequest("project-A", "instance-123", "m1.small", 4, 8000, false, "host1", "host2", "host3", "host4"),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host3"}, // host1 still has capacity (4 CPU), host2 blocked (0 CPU)
			filteredHosts: []string{"host2", "host4"},
		},
		{
			name: "Multiple reservations: pending and placed block different hosts",
			reservations: []client.Object{
				// Pending reservation blocks host1 (via Spec.Host only)
				// host1: 8 CPU free - 8 CPU reserved = 0 CPU free (blocked)
				newReservation("pending-res", "host1", "", "project-X", "m1.large", "8", "16Gi", v1alpha1.ReservationTypeCommittedResource, nil),
				// Placed reservation blocks host2 AND host3 (via both Spec.Host and ObservedHost)
				// host2: 4 CPU free - 4 CPU reserved = 0 CPU free (blocked)
				// host3: 16 CPU free - 4 CPU reserved = 12 CPU free (still has capacity)
				newReservation("placed-res", "host2", "host3", "project-X", "m1.large", "4", "8Gi", v1alpha1.ReservationTypeCommittedResource, nil),
			},
			request:       newRequest("project-A", "instance-123", "m1.small", 4, 8000, false, "host1", "host2", "host3", "host4"),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"}, // host1 blocked by pending, host2 blocked by placed, host3 still has capacity
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "Reservation with no host (neither Spec.Host nor ObservedHost) is skipped",
			reservations: []client.Object{
				newReservation("no-host-res", "", "", "project-X", "m1.large", "8", "16Gi", v1alpha1.ReservationTypeCommittedResource, nil),
			},
			request:       newRequest("project-A", "instance-123", "m1.small", 4, 8000, false, "host1", "host2", "host3", "host4"),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"}, // No hosts blocked - reservation has no host
			filteredHosts: []string{"host4"},                   // Only filtered due to no capacity
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(hvs)+len(tt.reservations))
			objects = append(objects, hvs...)
			objects = append(objects, tt.reservations...)
			step := &FilterHasEnoughCapacity{}
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			step.Options = tt.opts

			result, err := step.Run(slog.Default(), tt.request)
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
