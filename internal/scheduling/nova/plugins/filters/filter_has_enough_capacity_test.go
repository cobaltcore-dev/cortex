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

// ============================================================================
// Test Helpers
// ============================================================================

func buildTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 scheme: %v", err)
	}
	if err := hv1.SchemeBuilder.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hv1 scheme: %v", err)
	}
	return scheme
}

func newHypervisor(name, cpuCap, cpuAlloc, memCap, memAlloc string) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: hv1.HypervisorStatus{
			Capacity: map[string]resource.Quantity{
				"cpu":    resource.MustParse(cpuCap),
				"memory": resource.MustParse(memCap),
			},
			Allocation: map[string]resource.Quantity{
				"cpu":    resource.MustParse(cpuAlloc),
				"memory": resource.MustParse(memAlloc),
			},
		},
	}
}

func newCommittedReservation(name, targetHost, observedHost, projectID, resourceName, cpu, memory string) *v1alpha1.Reservation {
	if observedHost == "" {
		observedHost = targetHost
	}
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: targetHost,
			Resources: map[string]resource.Quantity{
				"cpu":    resource.MustParse(cpu),
				"memory": resource.MustParse(memory),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:    projectID,
				ResourceName: resourceName,
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
			Host: observedHost,
		},
	}
}

func newFailoverReservation(name, targetHost, cpu, memory string, allocations map[string]string) *v1alpha1.Reservation {
	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailover,
			TargetHost: targetHost,
			Resources: map[string]resource.Quantity{
				"cpu":    resource.MustParse(cpu),
				"memory": resource.MustParse(memory),
			},
			FailoverReservation: &v1alpha1.FailoverReservationSpec{
				ResourceGroup: "m1.large",
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
			Host: targetHost,
		},
	}
	if allocations != nil {
		res.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{
			Allocations: allocations,
		}
	}
	return res
}

// parseMemoryToMB converts a memory string (e.g., "8Gi", "4096Mi") to megabytes.
func parseMemoryToMB(memory string) uint64 {
	q := resource.MustParse(memory)
	bytes := q.Value()
	return uint64(bytes / (1024 * 1024)) //nolint:gosec // test code
}

func newNovaRequest(instanceUUID, projectID, flavorName string, vcpus int, memory string, evacuation bool, hosts []string) api.ExternalSchedulerRequest { //nolint:unparam // vcpus varies in real usage
	hostList := make([]api.ExternalSchedulerHost, len(hosts))
	for i, h := range hosts {
		hostList[i] = api.ExternalSchedulerHost{ComputeHost: h}
	}

	extraSpecs := map[string]string{
		"capabilities:hypervisor_type": "qemu",
	}

	var schedulerHints map[string]any
	if evacuation {
		schedulerHints = map[string]any{
			"_nova_check_type": []any{"evacuate"},
		}
	}

	memoryMB := parseMemoryToMB(memory)

	spec := api.NovaSpec{
		ProjectID:      projectID,
		InstanceUUID:   instanceUUID,
		NumInstances:   1,
		SchedulerHints: schedulerHints,
		Flavor: api.NovaObject[api.NovaFlavor]{
			Data: api.NovaFlavor{
				Name:       flavorName,
				VCPUs:      uint64(vcpus), //nolint:gosec // test code
				MemoryMB:   memoryMB,
				ExtraSpecs: extraSpecs,
			},
		},
	}

	weights := make(map[string]float64)
	for _, h := range hosts {
		weights[h] = 1.0
	}

	return api.ExternalSchedulerRequest{
		Spec:    api.NovaObject[api.NovaSpec]{Data: spec},
		Hosts:   hostList,
		Weights: weights,
	}
}

// ============================================================================
// Tests
// ============================================================================

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
	scheme := buildTestScheme(t)

	// 4 hypervisors: 3 with capacity, 1 without
	// host1: 8 CPU free, 16Gi free | host2: 4 CPU free, 8Gi free | host3: 16 CPU free, 32Gi free | host4: 0 free
	hypervisors := []*hv1.Hypervisor{
		newHypervisor("host1", "16", "8", "32Gi", "16Gi"),
		newHypervisor("host2", "8", "4", "16Gi", "8Gi"),
		newHypervisor("host3", "32", "16", "64Gi", "32Gi"),
		newHypervisor("host4", "8", "8", "16Gi", "16Gi"), // no capacity
	}

	tests := []struct {
		name          string
		reservations  []*v1alpha1.Reservation
		request       api.ExternalSchedulerRequest
		opts          FilterHasEnoughCapacityOpts
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "CommittedResourceReservation blocks some hosts when project/flavor don't match",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("res-1", "host1", "host1", "project-A", "m1.large", "8", "16Gi"),
				newCommittedReservation("res-2", "host2", "host2", "project-A", "m1.large", "4", "8Gi"),
			},
			request:       newNovaRequest("instance-123", "project-B", "m1.small", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "CommittedResourceReservation unlocks all reserved hosts when project and flavor match",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("res-1", "host1", "host1", "project-A", "m1.large", "4", "8Gi"),
				newCommittedReservation("res-2", "host2", "host2", "project-A", "m1.large", "4", "8Gi"),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.large", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			name: "CommittedResourceReservation stays locked when LockReserved is true",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("res-1", "host1", "host1", "project-A", "m1.large", "8", "16Gi"),
				newCommittedReservation("res-2", "host3", "host3", "project-A", "m1.large", "16", "32Gi"),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.large", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: true},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{"host1", "host3", "host4"},
		},
		{
			name: "Empty reservation type defaults to CommittedResourceReservation behavior",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("legacy-res", "host1", "host1", "project-A", "m1.large", "4", "8Gi"),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.large", 4, "8Gi", false, []string{"host1", "host2"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name: "FailoverReservation blocks hosts for non-evacuation request even when instance is in Allocations",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", "8", "16Gi", map[string]string{"instance-123": "host5"}),
				newFailoverReservation("failover-2", "host2", "4", "8Gi", map[string]string{"instance-123": "host6"}),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.large", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "FailoverReservation unlocks hosts during evacuation when instance is in Allocations",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", "4", "8Gi", map[string]string{"instance-123": "host5"}),
				newFailoverReservation("failover-2", "host2", "4", "8Gi", map[string]string{"instance-123": "host6"}),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.large", 4, "8Gi", true, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			name: "FailoverReservation blocks hosts during evacuation when instance not in Allocations",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", "8", "16Gi", map[string]string{"other-instance": "host5"}),
				newFailoverReservation("failover-2", "host2", "4", "8Gi", map[string]string{"another-instance": "host6"}),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.large", 4, "8Gi", true, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "FailoverReservation with empty Allocations blocks reserved host",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", "8", "16Gi", map[string]string{}),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.large", 4, "8Gi", true, []string{"host1", "host2", "host3"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host2", "host3"},
			filteredHosts: []string{"host1"},
		},
		{
			name: "FailoverReservation with multiple instances in Allocations unlocks for matching instance during evacuation",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", "4", "8Gi", map[string]string{"instance-111": "host5", "instance-222": "host6", "instance-333": "host7"}),
			},
			request:       newNovaRequest("instance-222", "project-A", "m1.large", 4, "8Gi", true, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			name:          "No reservations - all hosts with capacity pass",
			reservations:  []*v1alpha1.Reservation{},
			request:       newNovaRequest("instance-123", "project-A", "m1.small", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			name: "All hosts blocked by reservations - none pass",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("res-1", "host1", "host1", "project-X", "m1.xlarge", "8", "16Gi"),
				newCommittedReservation("res-2", "host2", "host2", "project-X", "m1.xlarge", "4", "8Gi"),
				newCommittedReservation("res-3", "host3", "host3", "project-X", "m1.xlarge", "16", "32Gi"),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.small", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2", "host3", "host4"},
		},
		{
			name: "Pending reservation (only TargetHost set) blocks capacity on desired host",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("pending-res", "host1", "", "project-X", "m1.large", "8", "16Gi"),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.small", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host2", "host3"}, // host1 blocked by pending reservation
			filteredHosts: []string{"host1", "host4"},
		},
		{
			name: "Reservation with different TargetHost and ObservedHost blocks BOTH hosts",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("moved-res", "host1", "host2", "project-X", "m1.large", "4", "8Gi"),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.small", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host3"}, // host1 still has capacity (4 CPU), host2 blocked (0 CPU)
			filteredHosts: []string{"host2", "host4"},
		},
		{
			name: "Multiple reservations: pending and placed block different hosts",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("pending-res", "host1", "", "project-X", "m1.large", "8", "16Gi"),
				newCommittedReservation("placed-res", "host2", "host3", "project-X", "m1.large", "4", "8Gi"),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.small", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"}, // host1 blocked by pending, host2 blocked by placed, host3 still has capacity
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "Reservation with no host is skipped",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("no-host-res", "", "", "project-X", "m1.large", "8", "16Gi"),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.small", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(hypervisors)+len(tt.reservations))
			for _, h := range hypervisors {
				objects = append(objects, h)
			}
			for _, r := range tt.reservations {
				objects = append(objects, r)
			}

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
