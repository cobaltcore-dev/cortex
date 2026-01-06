// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFilterHasEnoughCapacity_Run(t *testing.T) {
	scheme, err := hv1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error building hypervisor scheme, got %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("expected no error adding v1alpha1 to scheme, got %v", err)
	}

	// Define hypervisors with various capacity configurations
	hvs := []client.Object{
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host1",
			},
			Status: hv1.HypervisorStatus{
				Capacity: map[string]resource.Quantity{
					"cpu":    resource.MustParse("32"),   // 32 vCPUs
					"memory": resource.MustParse("64Gi"), // 64 GiB = 68719476736 bytes
				},
				Allocation: map[string]resource.Quantity{
					"cpu":    resource.MustParse("8"),    // 8 vCPUs used
					"memory": resource.MustParse("16Gi"), // 16 GiB used
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host2",
			},
			Status: hv1.HypervisorStatus{
				Capacity: map[string]resource.Quantity{
					"cpu":    resource.MustParse("16"),
					"memory": resource.MustParse("32Gi"),
				},
				Allocation: map[string]resource.Quantity{
					"cpu":    resource.MustParse("14"),
					"memory": resource.MustParse("28Gi"),
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host3",
			},
			Status: hv1.HypervisorStatus{
				Capacity: map[string]resource.Quantity{
					"cpu":    resource.MustParse("64"),
					"memory": resource.MustParse("128Gi"),
				},
				Allocation: map[string]resource.Quantity{
					"cpu":    resource.MustParse("0"),
					"memory": resource.MustParse("0"),
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host4",
			},
			Status: hv1.HypervisorStatus{
				Capacity: map[string]resource.Quantity{
					"cpu":    resource.MustParse("8"),
					"memory": resource.MustParse("16Gi"),
				},
				Allocation: map[string]resource.Quantity{
					"cpu":    resource.MustParse("4"),
					"memory": resource.MustParse("12Gi"),
				},
			},
		},
		&hv1.Hypervisor{
			ObjectMeta: v1.ObjectMeta{
				Name: "host5",
			},
			Status: hv1.HypervisorStatus{
				Capacity: map[string]resource.Quantity{
					"cpu":    resource.MustParse("48"),
					"memory": resource.MustParse("96Gi"),
				},
				Allocation: map[string]resource.Quantity{
					"cpu":    resource.MustParse("40"),
					"memory": resource.MustParse("80Gi"),
				},
			},
		},
	}

	tests := []struct {
		name          string
		request       api.ExternalSchedulerRequest
		reservations  []client.Object
		opts          FilterHasEnoughCapacityOpts
		expectedHosts []string
		filteredHosts []string
		expectError   bool
	}{
		{
			name: "Single instance with sufficient capacity on all hosts",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.small",
								VCPUs:    2,
								MemoryMB: 2048, // 2 GB
							},
						},
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
			expectedHosts: []string{"host1", "host2", "host3", "host4", "host5"},
			filteredHosts: []string{},
		},
		{
			name: "Single instance - filter host with insufficient CPU",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.large",
								VCPUs:    4,
								MemoryMB: 4096,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host4"},
			filteredHosts: []string{"host2"},
		},
		{
			name: "Single instance - filter host with insufficient memory",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.xlarge",
								VCPUs:    2,
								MemoryMB: 20480, // 20 GB
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2", "host5"},
		},
		{
			name: "Multiple instances - require capacity for all on same host",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 3,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.medium",
								VCPUs:    4,
								MemoryMB: 8192, // 8 GB
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
					{ComputeHost: "host5"},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host5"},
		},
		{
			name: "No hosts have sufficient capacity",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.huge",
								VCPUs:    32,
								MemoryMB: 65536, // 64 GB
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "Active reservation locks resources - filter host",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID:    "project-1",
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.small",
								VCPUs:    2,
								MemoryMB: 2048,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host4"},
				},
			},
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{
						Name: "reservation-1",
					},
					Spec: v1alpha1.ReservationSpec{
						Scheduler: v1alpha1.ReservationSchedulerSpec{
							CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
								ProjectID:  "project-2",
								FlavorName: "m1.medium",
							},
						},
						Requests: map[string]resource.Quantity{
							"cpu":    resource.MustParse("2"),
							"memory": resource.MustParse("2Gi"),
						},
					},
					Status: v1alpha1.ReservationStatus{
						Phase: v1alpha1.ReservationStatusPhaseActive,
						Host:  "host4",
					},
				},
			},
			expectedHosts: []string{"host4"},
			filteredHosts: []string{},
		},
		{
			name: "Matching reservation unlocks resources - host passes",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID:    "project-1",
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.small",
								VCPUs:    2,
								MemoryMB: 2048,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host4"},
				},
			},
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{
						Name: "reservation-matching",
					},
					Spec: v1alpha1.ReservationSpec{
						Scheduler: v1alpha1.ReservationSchedulerSpec{
							CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
								ProjectID:  "project-1",
								FlavorName: "m1.small",
							},
						},
						Requests: map[string]resource.Quantity{
							"cpu":    resource.MustParse("2"),
							"memory": resource.MustParse("2Gi"),
						},
					},
					Status: v1alpha1.ReservationStatus{
						Phase: v1alpha1.ReservationStatusPhaseActive,
						Host:  "host4",
					},
				},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host4"},
			filteredHosts: []string{},
		},
		{
			name: "LockReserved option - matching reservation still locks resources",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID:    "project-1",
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.small",
								VCPUs:    2,
								MemoryMB: 2048,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host4"},
				},
			},
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{
						Name: "reservation-locked",
					},
					Spec: v1alpha1.ReservationSpec{
						Scheduler: v1alpha1.ReservationSchedulerSpec{
							CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
								ProjectID:  "project-1",
								FlavorName: "m1.small",
							},
						},
						Requests: map[string]resource.Quantity{
							"cpu":    resource.MustParse("2"),
							"memory": resource.MustParse("2Gi"),
						},
					},
					Status: v1alpha1.ReservationStatus{
						Phase: v1alpha1.ReservationStatusPhaseActive,
						Host:  "host4",
					},
				},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: true},
			expectedHosts: []string{"host4"},
			filteredHosts: []string{},
		},
		{
			name: "Inactive reservation does not affect capacity",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID:    "project-1",
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.small",
								VCPUs:    2,
								MemoryMB: 2048,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host4"},
				},
			},
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{
						Name: "reservation-inactive",
					},
					Spec: v1alpha1.ReservationSpec{
						Scheduler: v1alpha1.ReservationSchedulerSpec{
							CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
								ProjectID:  "project-2",
								FlavorName: "m1.medium",
							},
						},
						Requests: map[string]resource.Quantity{
							"cpu":    resource.MustParse("2"),
							"memory": resource.MustParse("2Gi"),
						},
					},
					Status: v1alpha1.ReservationStatus{
						Phase: v1alpha1.ReservationStatusPhaseFailed,
						Host:  "host4",
					},
				},
			},
			expectedHosts: []string{"host4"},
			filteredHosts: []string{},
		},
		{
			name: "Reservation without CortexNova scheduler is ignored",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID:    "project-1",
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.small",
								VCPUs:    2,
								MemoryMB: 2048,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host4"},
				},
			},
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{
						Name: "reservation-other-scheduler",
					},
					Spec: v1alpha1.ReservationSpec{
						Scheduler: v1alpha1.ReservationSchedulerSpec{
							CortexNova: nil,
						},
						Requests: map[string]resource.Quantity{
							"cpu":    resource.MustParse("2"),
							"memory": resource.MustParse("2Gi"),
						},
					},
					Status: v1alpha1.ReservationStatus{
						Phase: v1alpha1.ReservationStatusPhaseActive,
						Host:  "host4",
					},
				},
			},
			expectedHosts: []string{"host4"},
			filteredHosts: []string{},
		},
		{
			name: "Multiple reservations on different hosts",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID:    "project-1",
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.small",
								VCPUs:    2,
								MemoryMB: 2048,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{
						Name: "reservation-host2",
					},
					Spec: v1alpha1.ReservationSpec{
						Scheduler: v1alpha1.ReservationSchedulerSpec{
							CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
								ProjectID:  "project-2",
								FlavorName: "m1.medium",
							},
						},
						Requests: map[string]resource.Quantity{
							"cpu":    resource.MustParse("2"),
							"memory": resource.MustParse("4Gi"),
						},
					},
					Status: v1alpha1.ReservationStatus{
						Phase: v1alpha1.ReservationStatusPhaseActive,
						Host:  "host2",
					},
				},
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{
						Name: "reservation-host5",
					},
					Spec: v1alpha1.ReservationSpec{
						Scheduler: v1alpha1.ReservationSchedulerSpec{
							CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
								ProjectID:  "project-3",
								FlavorName: "m1.large",
							},
						},
						Requests: map[string]resource.Quantity{
							"cpu":    resource.MustParse("4"),
							"memory": resource.MustParse("8Gi"),
						},
					},
					Status: v1alpha1.ReservationStatus{
						Phase: v1alpha1.ReservationStatusPhaseActive,
						Host:  "host5",
					},
				},
			},
			expectedHosts: []string{"host4", "host5"},
			filteredHosts: []string{"host2"},
		},
		{
			name: "Empty host list",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.small",
								VCPUs:    2,
								MemoryMB: 2048,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Host not in database is filtered out",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.small",
								VCPUs:    2,
								MemoryMB: 2048,
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
		{
			name: "Large number of instances - edge case",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 10,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.tiny",
								VCPUs:    1,
								MemoryMB: 512,
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
			name: "Flavor with zero VCPUs - error case",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "invalid-flavor",
								VCPUs:    0,
								MemoryMB: 2048,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectError: true,
		},
		{
			name: "Flavor with zero memory - error case",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "invalid-flavor",
								VCPUs:    2,
								MemoryMB: 0,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectError: true,
		},
		{
			name: "Memory boundary - exactly enough memory",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.exact",
								VCPUs:    2,
								MemoryMB: 4096, // Exactly 4 GB
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host4"}, // Has 4 GB free (16-12)
				},
			},
			expectedHosts: []string{"host4"},
			filteredHosts: []string{},
		},
		{
			name: "CPU boundary - exactly enough CPU",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.exact-cpu",
								VCPUs:    2, // Exactly 2 vCPUs
								MemoryMB: 1024,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host2"}, // Has 2 vCPUs free (16-14)
				},
			},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{},
		},
		{
			name: "Complex scenario with multiple hosts and reservations",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID:    "project-test",
						NumInstances: 2,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.test",
								VCPUs:    4,
								MemoryMB: 8192,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
					{ComputeHost: "host5"},
				},
			},
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{
						Name: "reservation-host1-matching",
					},
					Spec: v1alpha1.ReservationSpec{
						Scheduler: v1alpha1.ReservationSchedulerSpec{
							CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
								ProjectID:  "project-test",
								FlavorName: "m1.test",
							},
						},
						Requests: map[string]resource.Quantity{
							"cpu":    resource.MustParse("8"),
							"memory": resource.MustParse("16Gi"),
						},
					},
					Status: v1alpha1.ReservationStatus{
						Phase: v1alpha1.ReservationStatusPhaseActive,
						Host:  "host1",
					},
				},
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{
						Name: "reservation-host5-nonmatching",
					},
					Spec: v1alpha1.ReservationSpec{
						Scheduler: v1alpha1.ReservationSchedulerSpec{
							CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
								ProjectID:  "project-other",
								FlavorName: "m1.other",
							},
						},
						Requests: map[string]resource.Quantity{
							"cpu":    resource.MustParse("4"),
							"memory": resource.MustParse("8Gi"),
						},
					},
					Status: v1alpha1.ReservationStatus{
						Phase: v1alpha1.ReservationStatusPhaseActive,
						Host:  "host5",
					},
				},
			},
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build the fake client with hypervisors and reservations
			objects := make([]client.Object, 0, len(hvs)+len(tt.reservations))
			objects = append(objects, hvs...)
			objects = append(objects, tt.reservations...)

			step := &FilterHasEnoughCapacity{}
			step.Options = tt.opts
			step.Client = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			result, err := step.Run(slog.Default(), tt.request)

			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}

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
