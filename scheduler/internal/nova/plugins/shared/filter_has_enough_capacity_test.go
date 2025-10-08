// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/shared"
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/reservations/api/v1alpha1"
	delegationAPI "github.com/cobaltcore-dev/cortex/scheduler/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/nova/api"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Create a runtime scheme with all cortex CRDs registered.
func testScheme() *runtime.Scheme {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		panic(err)
	}
	return scheme
}

// Create a fake kubernetes client with no runtime objects.
func testClient() client.Client {
	var runtimeObjects []runtime.Object // None
	return fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithRuntimeObjects(runtimeObjects...).
		Build()
}

func TestFilterHasEnoughCapacity_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(
		testDB.AddTable(shared.HostUtilization{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the feature_host_utilization table
	hostUtilizations := []any{
		&shared.HostUtilization{ComputeHost: "host1", RAMUtilizedPct: 50.0, VCPUsUtilizedPct: 40.0, DiskUtilizedPct: 30.0, TotalRAMAllocatableMB: 32768, TotalVCPUsAllocatable: 16, TotalDiskAllocatableGB: 1000}, // High capacity host
		&shared.HostUtilization{ComputeHost: "host2", RAMUtilizedPct: 80.0, VCPUsUtilizedPct: 70.0, DiskUtilizedPct: 60.0, TotalRAMAllocatableMB: 16384, TotalVCPUsAllocatable: 8, TotalDiskAllocatableGB: 500},   // Medium capacity host
		&shared.HostUtilization{ComputeHost: "host3", RAMUtilizedPct: 90.0, VCPUsUtilizedPct: 85.0, DiskUtilizedPct: 75.0, TotalRAMAllocatableMB: 8192, TotalVCPUsAllocatable: 4, TotalDiskAllocatableGB: 250},    // Low capacity host
		&shared.HostUtilization{ComputeHost: "host4", RAMUtilizedPct: 20.0, VCPUsUtilizedPct: 15.0, DiskUtilizedPct: 10.0, TotalRAMAllocatableMB: 65536, TotalVCPUsAllocatable: 32, TotalDiskAllocatableGB: 2000}, // Very high capacity host
		&shared.HostUtilization{ComputeHost: "host5", RAMUtilizedPct: 95.0, VCPUsUtilizedPct: 90.0, DiskUtilizedPct: 85.0, TotalRAMAllocatableMB: 4096, TotalVCPUsAllocatable: 2, TotalDiskAllocatableGB: 100},    // Very low capacity host
		&shared.HostUtilization{ComputeHost: "host6", RAMUtilizedPct: 0.0, VCPUsUtilizedPct: 0.0, DiskUtilizedPct: 0.0, TotalRAMAllocatableMB: 0, TotalVCPUsAllocatable: 0, TotalDiskAllocatableGB: 0},            // Zero capacity host (edge case)
	}
	if err := testDB.Insert(hostUtilizations...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		request       api.PipelineRequest
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "Small flavor - most hosts have capacity",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								VCPUs:    2,
								MemoryMB: 4096,
								RootGB:   50,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3", "host4", "host5"}, // All except host6 (0 capacity) - host5 has exactly 2 vCPUs
			filteredHosts: []string{"host6"},
		},
		{
			name: "Medium flavor - some hosts filtered",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								VCPUs:    8,
								MemoryMB: 16384,
								RootGB:   200,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host4"}, // Only hosts with >= 8 vCPUs, >= 16384 MB RAM, >= 200 GB disk
			filteredHosts: []string{"host3", "host5", "host6"}, // host3 has only 4 vCPUs, host5 has only 2 vCPUs, host6 has 0
		},
		{
			name: "Large flavor - only high capacity hosts",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								VCPUs:    16,
								MemoryMB: 32768,
								RootGB:   500,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1", "host4"}, // Only hosts with >= 16 vCPUs, >= 32768 MB RAM, >= 500 GB disk
			filteredHosts: []string{"host2", "host3", "host5", "host6"},
		},
		{
			name: "Very large flavor - only very high capacity host",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								VCPUs:    32,
								MemoryMB: 65536,
								RootGB:   1000,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host4"}, // Only host4 has enough capacity
			filteredHosts: []string{"host1", "host2", "host3", "host5", "host6"},
		},
		{
			name: "Impossible flavor - no hosts have capacity",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								VCPUs:    64,
								MemoryMB: 131072,
								RootGB:   5000,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{}, // No hosts have enough capacity
			filteredHosts: []string{"host1", "host2", "host3", "host4", "host5", "host6"},
		},
		{
			name: "CPU constraint only",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								VCPUs:    10, // More than host3 (4) and host5 (2)
								MemoryMB: 1024,
								RootGB:   10,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1", "host4"}, // Only hosts with >= 10 vCPUs
			filteredHosts: []string{"host2", "host3", "host5", "host6"},
		},
		{
			name: "Memory constraint only",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								VCPUs:    1,
								MemoryMB: 20000, // More than host3 (8192) and host5 (4096)
								RootGB:   10,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1", "host4"}, // Only hosts with >= 20000 MB RAM
			filteredHosts: []string{"host2", "host3", "host5", "host6"},
		},
		{
			name: "Very small flavor",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								VCPUs:    1,
								MemoryMB: 512,
								RootGB:   10,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3", "host4", "host5"}, // All except host6 (0 capacity)
			filteredHosts: []string{"host6"},
		},
		{
			name: "Host not in database",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								VCPUs:    2,
								MemoryMB: 4096,
								RootGB:   50,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host-unknown"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host-unknown"}, // Host not in database gets filtered out
		},
		{
			name: "Empty host list",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								VCPUs:    2,
								MemoryMB: 4096,
								RootGB:   50,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Exact capacity match",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								VCPUs:    8, // Exactly matches host2
								MemoryMB: 16384,
								RootGB:   500,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host4"}, // host2 exactly matches, host1 and host4 exceed
			filteredHosts: []string{"host3"},                   // host3 has insufficient capacity
		},
		{
			name: "Boundary test - just over capacity",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								VCPUs:    9,     // Just over host2's 8 vCPUs
								MemoryMB: 16385, // Just over host2's 16384 MB
								RootGB:   501,   // Just over host2's 500 GB
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host4"}, // Only hosts that exceed the requirements
			filteredHosts: []string{"host2", "host3"}, // host2 is just under, host3 is well under
		},
		{
			name: "Edge case - exactly enough total slots",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 8,
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								VCPUs:    1,
								MemoryMB: 4096,
								RootGB:   20,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"}, // 32768/4096 = 8 memory slots, 16/1 = 16 vcpu slots
					{ComputeHost: "host5"}, // 4096/4096 = 1 memory slot, 2/1 = 2 vcpu slots
				},
			},
			expectedHosts: []string{"host1"}, // Should pass as memorySlotsTotal (8+1=9) == numInstances (9)
			filteredHosts: []string{"host5"},
		},
		{
			name: "Edge case - 1 vm more than available slots",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 9, // 1 more than available.
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								VCPUs:    1,
								MemoryMB: 4096,
								RootGB:   20,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"}, // 32768/4096 = 8 memory slots, 16/1 = 16 vcpu slots
					{ComputeHost: "host5"}, // 4096/4096 = 1 memory slot, 2/1 = 2 vcpu slots
				},
			},
			expectedHosts: []string{}, // Should fail as memorySlotsTotal (8+1=9) < numInstances (10)
			filteredHosts: []string{"host1", "host5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Running test case: %s", tt.name)
			step := &FilterHasEnoughCapacity{}
			step.Client = testClient() // Override the real client with our fake client
			if err := step.Init("", testDB, conf.NewRawOpts("{}")); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			// Override the real client with our fake client after Init()
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

func TestFilterHasEnoughCapacity_WithReservations(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(
		testDB.AddTable(shared.HostUtilization{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the feature_host_utilization table
	hostUtilizations := []any{
		&shared.HostUtilization{ComputeHost: "host1", RAMUtilizedPct: 50.0, VCPUsUtilizedPct: 40.0, DiskUtilizedPct: 30.0, TotalRAMAllocatableMB: 32768, TotalVCPUsAllocatable: 16, TotalDiskAllocatableGB: 1000}, // High capacity host
		&shared.HostUtilization{ComputeHost: "host2", RAMUtilizedPct: 80.0, VCPUsUtilizedPct: 70.0, DiskUtilizedPct: 60.0, TotalRAMAllocatableMB: 16384, TotalVCPUsAllocatable: 8, TotalDiskAllocatableGB: 500},   // Medium capacity host
	}
	if err := testDB.Insert(hostUtilizations...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create active reservations that consume resources on hosts
	reservations := []v1alpha1.ComputeReservation{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "reservation-host1-1",
				Namespace: "test-namespace",
			},
			Spec: v1alpha1.ComputeReservationSpec{
				Scheduler: v1alpha1.ComputeReservationSchedulerSpec{
					CortexNova: &v1alpha1.ComputeReservationSchedulerSpecCortexNova{
						FlavorName: "test-flavor",
						ProjectID:  "test-project",
						DomainID:   "test-domain",
					},
				},
				Requests: map[string]resource.Quantity{
					"memory": *resource.NewQuantity(4*1024*1024*1024, resource.BinarySI), // 4GB
					"cpu":    *resource.NewQuantity(4, resource.DecimalSI),
				},
			},
			Status: v1alpha1.ComputeReservationStatus{
				Phase: v1alpha1.ComputeReservationStatusPhaseActive,
				Host:  "host1",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "reservation-host2-1",
				Namespace: "test-namespace",
			},
			Spec: v1alpha1.ComputeReservationSpec{
				Scheduler: v1alpha1.ComputeReservationSchedulerSpec{
					CortexNova: &v1alpha1.ComputeReservationSchedulerSpecCortexNova{
						FlavorName: "test-flavor",
						ProjectID:  "test-project",
						DomainID:   "test-domain",
					},
				},
				Requests: map[string]resource.Quantity{
					"memory": *resource.NewQuantity(4*1024*1024*1024, resource.BinarySI), // 4GB
					"cpu":    *resource.NewQuantity(4, resource.DecimalSI),
				},
			},
			Status: v1alpha1.ComputeReservationStatus{
				Phase: v1alpha1.ComputeReservationStatusPhaseActive,
				Host:  "host2",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "reservation-inactive",
				Namespace: "test-namespace",
			},
			Spec: v1alpha1.ComputeReservationSpec{
				Scheduler: v1alpha1.ComputeReservationSchedulerSpec{
					CortexNova: &v1alpha1.ComputeReservationSchedulerSpecCortexNova{
						FlavorName: "test-flavor",
						ProjectID:  "test-project",
						DomainID:   "test-domain",
					},
				},
				Requests: map[string]resource.Quantity{
					"memory": *resource.NewQuantity(16*1024*1024*1024, resource.BinarySI), // 16GB
					"cpu":    *resource.NewQuantity(8, resource.DecimalSI),
				},
			},
			Status: v1alpha1.ComputeReservationStatus{
				Phase: v1alpha1.ComputeReservationStatusPhaseFailed, // Not active, should be ignored
				Host:  "host1",
			},
		},
	}

	// Create fake Kubernetes client with reservations
	scheme := testScheme()
	var runtimeObjects []runtime.Object
	for i := range reservations {
		runtimeObjects = append(runtimeObjects, &reservations[i])
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(runtimeObjects...).
		Build()

	step := &FilterHasEnoughCapacity{}
	step.Client = fakeClient // Override the real client with our fake client
	if err := step.Init("", testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Test case: Request that would fit on host1 without reservations, but not with reservations
	request := api.PipelineRequest{
		Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
			Data: delegationAPI.NovaSpec{
				NumInstances: 1,
				Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
					Data: delegationAPI.NovaFlavor{
						VCPUs:    14,    // host1 has 16 total, 4 reserved = 12 available, so this should fail
						MemoryMB: 16384, // host1 has 32768 total, 4000 reserved = 28768 available, so this should pass
						RootGB:   500,   // host1 has 1000 total, 100 reserved = 900 available, so this should pass
					},
				},
			},
		},
		Hosts: []delegationAPI.ExternalSchedulerHost{
			{ComputeHost: "host1"},
			{ComputeHost: "host2"},
		},
	}

	result, err := step.Run(slog.Default(), request)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Debug: Print the result to see what's happening
	t.Logf("Result activations: %v", result.Activations)

	// host1 should be filtered out due to insufficient vCPUs after reservations (16 - 4 = 12 < 14)
	if _, ok := result.Activations["host1"]; ok {
		t.Error("expected host1 to be filtered out due to reservations consuming vCPUs")
	}

	// host2 should be filtered out due to insufficient vCPUs (8 - 4 = 4 < 14)
	if _, ok := result.Activations["host2"]; ok {
		t.Error("expected host2 to be filtered out due to insufficient vCPUs")
	}

	// Test case: Request that fits after accounting for reservations
	request2 := api.PipelineRequest{
		Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
			Data: delegationAPI.NovaSpec{
				NumInstances: 1,
				Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
					Data: delegationAPI.NovaFlavor{
						VCPUs:    10,    // host1 has 16 - 4 = 12 available, so this should pass
						MemoryMB: 20480, // host1 has 32768 - 4096 = 28672 available, so this should pass
						RootGB:   800,   // host1 has 1000 - 100 = 900 available, so this should pass
					},
				},
			},
		},
		Hosts: []delegationAPI.ExternalSchedulerHost{
			{ComputeHost: "host1"},
			{ComputeHost: "host2"},
		},
	}

	result2, err := step.Run(slog.Default(), request2)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// host1 should pass (16-4=12 vCPUs >= 10, 32768-4096=28672 MB >= 20480, 1000-100=900 GB >= 800)
	if _, ok := result2.Activations["host1"]; !ok {
		t.Error("expected host1 to be available after accounting for reservations")
	}

	// host2 should be filtered out (8-4=4 vCPUs < 10)
	if _, ok := result2.Activations["host2"]; ok {
		t.Error("expected host2 to be filtered out due to insufficient vCPUs after reservations")
	}
}

func TestFilterHasEnoughCapacity_ReservationMatching(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(
		testDB.AddTable(shared.HostUtilization{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the feature_host_utilization table
	hostUtilizations := []any{
		&shared.HostUtilization{ComputeHost: "host1", RAMUtilizedPct: 50.0, VCPUsUtilizedPct: 40.0, DiskUtilizedPct: 30.0, TotalRAMAllocatableMB: 16384, TotalVCPUsAllocatable: 8, TotalDiskAllocatableGB: 500}, // Limited capacity host
	}
	if err := testDB.Insert(hostUtilizations...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name                string
		reservations        []v1alpha1.ComputeReservation
		request             api.PipelineRequest
		expectedHostPresent bool
		description         string
	}{
		{
			name: "Reservation matches request - resources should be unlocked",
			reservations: []v1alpha1.ComputeReservation{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "matching-reservation",
						Namespace: "test-namespace",
					},
					Spec: v1alpha1.ComputeReservationSpec{
						Scheduler: v1alpha1.ComputeReservationSchedulerSpec{
							CortexNova: &v1alpha1.ComputeReservationSchedulerSpecCortexNova{
								FlavorName: "test-flavor",  // Matches request
								ProjectID:  "test-project", // Matches request
								DomainID:   "test-domain",
							},
						},
						Requests: map[string]resource.Quantity{
							"memory": *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI), // 8GB - consumes all memory
							"cpu":    *resource.NewQuantity(4, resource.DecimalSI),               // 4 vCPUs - consumes half vCPUs
						},
					},
					Status: v1alpha1.ComputeReservationStatus{
						Phase: v1alpha1.ComputeReservationStatusPhaseActive,
						Host:  "host1",
					},
				},
			},
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						ProjectID:    "test-project", // Matches reservation
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								Name:     "test-flavor", // Matches reservation
								VCPUs:    6,             // Would normally fail (8 - 4 = 4 < 6), but reservation should be unlocked
								MemoryMB: 12288,         // Would normally fail (16384 - 8192 = 8192 < 12288), but reservation should be unlocked
								RootGB:   200,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectedHostPresent: true,
			description:         "When ProjectID and FlavorName match, reservation resources should be unlocked allowing the request to succeed",
		},
		{
			name: "Reservation does not match ProjectID - resources remain reserved",
			reservations: []v1alpha1.ComputeReservation{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "non-matching-project-reservation",
						Namespace: "test-namespace",
					},
					Spec: v1alpha1.ComputeReservationSpec{
						Scheduler: v1alpha1.ComputeReservationSchedulerSpec{
							CortexNova: &v1alpha1.ComputeReservationSchedulerSpecCortexNova{
								FlavorName: "test-flavor",       // Matches request
								ProjectID:  "different-project", // Does NOT match request
								DomainID:   "test-domain",
							},
						},
						Requests: map[string]resource.Quantity{
							"memory": *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI), // 8GB
							"cpu":    *resource.NewQuantity(4, resource.DecimalSI),               // 4 vCPUs
						},
					},
					Status: v1alpha1.ComputeReservationStatus{
						Phase: v1alpha1.ComputeReservationStatusPhaseActive,
						Host:  "host1",
					},
				},
			},
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						ProjectID:    "test-project", // Does NOT match reservation
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								Name:     "test-flavor", // Matches reservation
								VCPUs:    6,             // Should fail (8 - 4 = 4 < 6)
								MemoryMB: 12288,         // Should fail (16384 - 8192 = 8192 < 12288)
								RootGB:   200,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectedHostPresent: false,
			description:         "When ProjectID does not match, reservation resources should remain reserved and request should fail",
		},
		{
			name: "Reservation does not match FlavorName - resources remain reserved",
			reservations: []v1alpha1.ComputeReservation{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "non-matching-flavor-reservation",
						Namespace: "test-namespace",
					},
					Spec: v1alpha1.ComputeReservationSpec{
						Scheduler: v1alpha1.ComputeReservationSchedulerSpec{
							CortexNova: &v1alpha1.ComputeReservationSchedulerSpecCortexNova{
								FlavorName: "different-flavor", // Does NOT match request
								ProjectID:  "test-project",     // Matches request
								DomainID:   "test-domain",
							},
						},
						Requests: map[string]resource.Quantity{
							"memory": *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI), // 8GB
							"cpu":    *resource.NewQuantity(4, resource.DecimalSI),               // 4 vCPUs
						},
					},
					Status: v1alpha1.ComputeReservationStatus{
						Phase: v1alpha1.ComputeReservationStatusPhaseActive,
						Host:  "host1",
					},
				},
			},
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						ProjectID:    "test-project", // Matches reservation
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								Name:     "test-flavor", // Does NOT match reservation
								VCPUs:    6,             // Should fail (8 - 4 = 4 < 6)
								MemoryMB: 12288,         // Should fail (16384 - 8192 = 8192 < 12288)
								RootGB:   200,
							},
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			expectedHostPresent: false,
			description:         "When FlavorName does not match, reservation resources should remain reserved and request should fail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake Kubernetes client with reservations
			scheme := testScheme()
			var runtimeObjects []runtime.Object
			for i := range tt.reservations {
				runtimeObjects = append(runtimeObjects, &tt.reservations[i])
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(runtimeObjects...).
				Build()

			step := &FilterHasEnoughCapacity{}
			step.Client = fakeClient // Override the real client with our fake client
			if err := step.Init("", testDB, conf.NewRawOpts("{}")); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// Check if host is present or absent as expected
			_, hostPresent := result.Activations["host1"]
			if hostPresent != tt.expectedHostPresent {
				t.Errorf("Test case: %s\nExpected host1 present: %v, got: %v\nDescription: %s",
					tt.name, tt.expectedHostPresent, hostPresent, tt.description)
			}

			// Debug information
			t.Logf("Test: %s, Host present: %v, Activations: %v", tt.name, hostPresent, result.Activations)
		})
	}
}
