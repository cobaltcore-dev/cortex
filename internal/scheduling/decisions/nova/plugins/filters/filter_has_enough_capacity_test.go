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
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFilterHasEnoughCapacity_Run(t *testing.T) {
	// Build schemes for both Hypervisor and Reservation types
	scheme := runtime.NewScheme()
	if err := hv1.SchemeBuilder.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hypervisor scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add cortex scheme: %v", err)
	}

	tests := []struct {
		name          string
		hypervisors   []client.Object
		reservations  []client.Object
		request       api.ExternalSchedulerRequest
		options       FilterHasEnoughCapacityOpts
		expectedHosts []string
		filteredHosts []string
		expectError   bool
	}{
		{
			name: "Single instance with sufficient capacity",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("16"),
							HostMemory: resource.MustParse("32Gi"),
						},
						DomainInfos: []hv1.DomainInfo{
							{
								Name: "instance-1",
								Allocation: map[string]resource.Quantity{
									"cpu":    resource.MustParse("4"),
									"memory": resource.MustParse("8Gi"),
								},
							},
						},
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    4,
								MemoryMB: 4000,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
			expectError:   false,
		},
		{
			name: "Single instance with insufficient CPU",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("16"),
							HostMemory: resource.MustParse("32Gi"),
						},
						DomainInfos: []hv1.DomainInfo{
							{
								Name: "instance-1",
								Allocation: map[string]resource.Quantity{
									"cpu":    resource.MustParse("12"),
									"memory": resource.MustParse("8Gi"),
								},
							},
						},
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    8,
								MemoryMB: 4000,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1"},
			expectError:   false,
		},
		{
			name: "Single instance with insufficient memory",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("16"),
							HostMemory: resource.MustParse("32Gi"),
						},
						DomainInfos: []hv1.DomainInfo{
							{
								Name: "instance-1",
								Allocation: map[string]resource.Quantity{
									"cpu":    resource.MustParse("4"),
									"memory": resource.MustParse("28Gi"),
								},
							},
						},
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    4,
								MemoryMB: 8000,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1"},
			expectError:   false,
		},
		{
			name: "Multiple instances on single host - sufficient capacity",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("16"),
							HostMemory: resource.MustParse("32Gi"),
						},
						DomainInfos: []hv1.DomainInfo{},
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 4,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    4,
								MemoryMB: 8000,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
			expectError:   false,
		},
		{
			name: "Multiple instances - insufficient capacity for all",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("16"),
							HostMemory: resource.MustParse("32Gi"),
						},
						DomainInfos: []hv1.DomainInfo{},
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 5,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    4,
								MemoryMB: 8000,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1"},
			expectError:   false,
		},
		{
			name: "Multiple hosts - mixed capacity",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("16"),
							HostMemory: resource.MustParse("32Gi"),
						},
						DomainInfos: []hv1.DomainInfo{},
					},
				},
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("8"),
							HostMemory: resource.MustParse("16Gi"),
						},
						DomainInfos: []hv1.DomainInfo{},
					},
				},
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host3"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("32"),
							HostMemory: resource.MustParse("64Gi"),
						},
						DomainInfos: []hv1.DomainInfo{},
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    12,
								MemoryMB: 24000,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host2"},
			expectError:   false,
		},
		{
			name: "Active reservation - subtract reserved resources",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("16"),
							HostMemory: resource.MustParse("32Gi"),
						},
						DomainInfos: []hv1.DomainInfo{},
					},
				},
			},
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{Name: "reservation-1"},
					Spec: v1alpha1.ReservationSpec{
						Scheduler: v1alpha1.ReservationSchedulerSpec{
							CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
								ProjectID:  "different-project",
								FlavorName: "different-flavor",
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
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						ProjectID:    "test-project",
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "test-flavor",
								VCPUs:    8,
								MemoryMB: 16000,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
			expectError:   false,
		},
		{
			name: "Matching reservation - unlock reserved resources",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("16"),
							HostMemory: resource.MustParse("32Gi"),
						},
						DomainInfos: []hv1.DomainInfo{},
					},
				},
			},
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{Name: "reservation-1"},
					Spec: v1alpha1.ReservationSpec{
						Scheduler: v1alpha1.ReservationSchedulerSpec{
							CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
								ProjectID:  "test-project",
								FlavorName: "test-flavor",
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
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						ProjectID:    "test-project",
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "test-flavor",
								VCPUs:    8,
								MemoryMB: 16000,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			options:       FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
			expectError:   false,
		},
		{
			name: "Matching reservation with LockReserved option",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("16"),
							HostMemory: resource.MustParse("32Gi"),
						},
						DomainInfos: []hv1.DomainInfo{},
					},
				},
			},
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{Name: "reservation-1"},
					Spec: v1alpha1.ReservationSpec{
						Scheduler: v1alpha1.ReservationSchedulerSpec{
							CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
								ProjectID:  "test-project",
								FlavorName: "test-flavor",
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
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						ProjectID:    "test-project",
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "test-flavor",
								VCPUs:    8,
								MemoryMB: 16000,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			options:       FilterHasEnoughCapacityOpts{LockReserved: true},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
			expectError:   false,
		},
		{
			name: "Inactive reservation - do not subtract",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("16"),
							HostMemory: resource.MustParse("32Gi"),
						},
						DomainInfos: []hv1.DomainInfo{},
					},
				},
			},
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{Name: "reservation-1"},
					Spec: v1alpha1.ReservationSpec{
						Scheduler: v1alpha1.ReservationSchedulerSpec{
							CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
								ProjectID:  "test-project",
								FlavorName: "test-flavor",
							},
						},
						Requests: map[string]resource.Quantity{
							"cpu":    resource.MustParse("8"),
							"memory": resource.MustParse("16Gi"),
						},
					},
					Status: v1alpha1.ReservationStatus{
						Phase: v1alpha1.ReservationStatusPhaseFailed,
						Host:  "host1",
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						ProjectID:    "test-project",
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "test-flavor",
								VCPUs:    8,
								MemoryMB: 16000,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
			expectError:   false,
		},
		{
			name: "Reservation for different scheduler - do not consider",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("16"),
							HostMemory: resource.MustParse("32Gi"),
						},
						DomainInfos: []hv1.DomainInfo{},
					},
				},
			},
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{Name: "reservation-1"},
					Spec: v1alpha1.ReservationSpec{
						Scheduler: v1alpha1.ReservationSchedulerSpec{
							CortexNova: nil,
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
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    8,
								MemoryMB: 16000,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{},
			expectError:   false,
		},
		{
			name: "Host not in hypervisor list - filtered out",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("16"),
							HostMemory: resource.MustParse("32Gi"),
						},
						DomainInfos: []hv1.DomainInfo{},
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    4,
								MemoryMB: 8000,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2"},
			expectError:   false,
		},
		{
			name: "Empty host list",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("16"),
							HostMemory: resource.MustParse("32Gi"),
						},
						DomainInfos: []hv1.DomainInfo{},
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    4,
								MemoryMB: 8000,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
			expectError:   false,
		},
		{
			name: "Flavor with zero vCPUs - error",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("16"),
							HostMemory: resource.MustParse("32Gi"),
						},
						DomainInfos: []hv1.DomainInfo{},
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    0,
								MemoryMB: 8000,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
			expectError:   true,
		},
		{
			name: "Flavor with zero memory - error",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("16"),
							HostMemory: resource.MustParse("32Gi"),
						},
						DomainInfos: []hv1.DomainInfo{},
					},
				},
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    4,
								MemoryMB: 0,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
			expectError:   true,
		},
		{
			name: "Complex scenario - multiple hosts, VMs, and reservations",
			hypervisors: []client.Object{
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("32"),
							HostMemory: resource.MustParse("64Gi"),
						},
						DomainInfos: []hv1.DomainInfo{
							{
								Name: "instance-1",
								Allocation: map[string]resource.Quantity{
									"cpu":    resource.MustParse("8"),
									"memory": resource.MustParse("16Gi"),
								},
							},
							{
								Name: "instance-2",
								Allocation: map[string]resource.Quantity{
									"cpu":    resource.MustParse("4"),
									"memory": resource.MustParse("8Gi"),
								},
							},
						},
					},
				},
				&hv1.Hypervisor{
					ObjectMeta: v1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{
							HostCpus:   resource.MustParse("32"),
							HostMemory: resource.MustParse("64Gi"),
						},
						DomainInfos: []hv1.DomainInfo{
							{
								Name: "instance-3",
								Allocation: map[string]resource.Quantity{
									"cpu":    resource.MustParse("16"),
									"memory": resource.MustParse("32Gi"),
								},
							},
						},
					},
				},
			},
			reservations: []client.Object{
				&v1alpha1.Reservation{
					ObjectMeta: v1.ObjectMeta{Name: "reservation-1"},
					Spec: v1alpha1.ReservationSpec{
						Scheduler: v1alpha1.ReservationSchedulerSpec{
							CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
								ProjectID:  "other-project",
								FlavorName: "other-flavor",
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
			},
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						ProjectID:    "test-project",
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "test-flavor",
								VCPUs:    8,
								MemoryMB: 16000,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			//nolint:gocritic
			objects := append(tt.hypervisors, tt.reservations...)
			step := &FilterHasEnoughCapacity{}
			step.Client = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()
			step.Options = tt.options

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
