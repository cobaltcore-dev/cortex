// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/reservations/api/v1alpha1"
)

// Mock CommitmentsClient for testing
type mockCommitmentsClient struct {
	initFunc                      func(ctx context.Context)
	initFuncCalled                bool
	listProjectsFunc              func(ctx context.Context) ([]Project, error)
	listProjectsFuncCalled        bool
	listFlavorsByNameFunc         func(ctx context.Context) (map[string]Flavor, error)
	listFlavorsByNameFuncCalled   bool
	listCommitmentsByIDFunc       func(ctx context.Context, projects ...Project) (map[string]Commitment, error)
	listCommitmentsByIDFuncCalled bool
	listServersFunc               func(ctx context.Context, projects ...Project) (map[string][]Server, error)
	listServersFuncCalled         bool
}

func (m *mockCommitmentsClient) Init(ctx context.Context) {
	m.initFuncCalled = true
	if m.initFunc != nil {
		m.initFunc(ctx)
	}
}
func (m *mockCommitmentsClient) ListProjects(ctx context.Context) ([]Project, error) {
	m.listProjectsFuncCalled = true
	if m.listProjectsFunc == nil {
		return []Project{}, nil
	}
	return m.listProjectsFunc(ctx)
}
func (m *mockCommitmentsClient) ListFlavorsByName(ctx context.Context) (map[string]Flavor, error) {
	m.listFlavorsByNameFuncCalled = true
	if m.listFlavorsByNameFunc == nil {
		return map[string]Flavor{}, nil
	}
	return m.listFlavorsByNameFunc(ctx)
}
func (m *mockCommitmentsClient) ListCommitmentsByID(ctx context.Context, projects ...Project) (map[string]Commitment, error) {
	m.listCommitmentsByIDFuncCalled = true
	if m.listCommitmentsByIDFunc == nil {
		return map[string]Commitment{}, nil
	}
	return m.listCommitmentsByIDFunc(ctx, projects...)
}
func (m *mockCommitmentsClient) ListServersByProjectID(ctx context.Context, projects ...Project) (map[string][]Server, error) {
	m.listServersFuncCalled = true
	if m.listServersFunc == nil {
		return map[string][]Server{}, nil
	}
	return m.listServersFunc(ctx, projects...)
}

func TestNewSyncer(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	// Create a syncer directly without using NewSyncer to avoid config file dependencies
	mockClient := &mockCommitmentsClient{}
	syncer := &Syncer{
		CommitmentsClient: mockClient,
		Client:            k8sClient,
	}

	if syncer.Client != k8sClient {
		t.Error("Expected syncer to have the correct k8s client")
	}

	if syncer.CommitmentsClient == nil {
		t.Error("Expected syncer to have a commitments client")
	}
}

func TestSyncer_Init(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	mockClient := &mockCommitmentsClient{}
	syncer := &Syncer{
		CommitmentsClient: mockClient,
		Client:            k8sClient,
	}

	syncer.Init(context.Background())

	if !mockClient.initFuncCalled {
		t.Error("Expected Init to be called on commitments client")
	}
}

func TestSyncer_SyncReservations_InstanceCommitments(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	// Create mock commitments with instance flavors
	mockCommitments := []Commitment{
		{
			ID:               1,
			UUID:             "12345-67890-abcdef",
			ServiceType:      "compute",
			ResourceName:     "instances_test-flavor",
			AvailabilityZone: "az1",
			Amount:           2, // 2 instances
			Unit:             "",
			ProjectID:        "test-project-1",
			DomainID:         "test-domain-1",
		},
	}

	mockClient := &mockCommitmentsClient{
		listCommitmentsByIDFunc: func(ctx context.Context, projects ...Project) (map[string]Commitment, error) {
			result := make(map[string]Commitment)
			for _, c := range mockCommitments {
				result[c.UUID] = c
			}
			return result, nil
		},
		listFlavorsByNameFunc: func(ctx context.Context) (map[string]Flavor, error) {
			return map[string]Flavor{
				"test-flavor": {
					ID:    "flavor-1",
					Name:  "test-flavor",
					RAM:   1024, // 1GB in MB
					VCPUs: 2,
					Disk:  20, // 20GB
					ExtraSpecs: map[string]string{
						"hw:cpu_policy":                         "dedicated",
						"hw:numa_nodes":                         "1",
						"aggregate_instance_extra_specs:pinned": "true",
					},
				},
			}, nil
		},
		listProjectsFunc: func(ctx context.Context) ([]Project, error) {
			return []Project{
				{ID: "test-project-1", DomainID: "test-domain-1", Name: "Test Project 1"},
			}, nil
		},
		listServersFunc: func(ctx context.Context, projects ...Project) (map[string][]Server, error) {
			return map[string][]Server{}, nil // No active servers
		},
		initFunc: func(ctx context.Context) {
			// No-op for init
		},
	}

	syncer := &Syncer{
		CommitmentsClient: mockClient,
		Client:            k8sClient,
	}

	err := syncer.SyncReservations(context.Background())
	if err != nil {
		t.Errorf("SyncReservations() error = %v", err)
		return
	}

	// Verify that reservations were created
	var reservations v1alpha1.ComputeReservationList
	err = k8sClient.List(context.Background(), &reservations)
	if err != nil {
		t.Errorf("Failed to list reservations: %v", err)
		return
	}

	// Should have 2 reservations (Amount = 2)
	if len(reservations.Items) != 2 {
		t.Errorf("Expected 2 reservations, got %d", len(reservations.Items))
		return
	}

	// Verify the first reservation
	res := reservations.Items[0]
	if res.Spec.Scheduler.CortexNova.ProjectID != "test-project-1" {
		t.Errorf("Expected project ID test-project-1, got %v", res.Spec.Scheduler.CortexNova.ProjectID)
	}

	if res.Spec.Scheduler.CortexNova.FlavorName != "test-flavor" {
		t.Errorf("Expected flavor test-flavor, got %v", res.Spec.Scheduler.CortexNova.FlavorName)
	}

	// Check resource values
	expectedMemory := resource.MustParse("1073741824") // 1024MB in bytes
	if !res.Spec.Requests["memory"].Equal(expectedMemory) {
		t.Errorf("Expected memory %v, got %v", expectedMemory, res.Spec.Requests["memory"])
	}

	expectedVCPUs := resource.MustParse("2")
	if !res.Spec.Requests["cpu"].Equal(expectedVCPUs) {
		t.Errorf("Expected vCPUs %v, got %v", expectedVCPUs, res.Spec.Requests["cpu"])
	}
}

func TestSyncer_SyncReservations_UpdateExisting(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// Create an existing reservation
	existingReservation := &v1alpha1.ComputeReservation{
		ObjectMeta: ctrl.ObjectMeta{
			Name: "commitment-12345-0", // Instance commitments have -0 suffix
		},
		Spec: v1alpha1.ComputeReservationSpec{
			Scheduler: v1alpha1.ComputeReservationSchedulerSpec{
				CortexNova: &v1alpha1.ComputeReservationSchedulerSpecCortexNova{
					ProjectID:  "old-project",
					FlavorName: "old-flavor",
				},
			},
			Requests: map[string]resource.Quantity{
				"memory": resource.MustParse("512Mi"),
				"cpu":    resource.MustParse("1"),
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existingReservation).
		Build()

	// Create mock commitment that should update the existing reservation
	mockCommitments := []Commitment{
		{
			ID:               1,
			UUID:             "12345-67890-abcdef",
			ServiceType:      "compute",
			ResourceName:     "instances_new-flavor",
			AvailabilityZone: "az1",
			Amount:           1,
			Unit:             "",
			ProjectID:        "new-project",
			DomainID:         "new-domain",
		},
	}

	mockClient := &mockCommitmentsClient{
		listCommitmentsByIDFunc: func(ctx context.Context, projects ...Project) (map[string]Commitment, error) {
			result := make(map[string]Commitment)
			for _, c := range mockCommitments {
				result[c.UUID] = c
			}
			return result, nil
		},
		listFlavorsByNameFunc: func(ctx context.Context) (map[string]Flavor, error) {
			return map[string]Flavor{
				"new-flavor": {
					ID:    "flavor-2",
					Name:  "new-flavor",
					RAM:   2048, // 2GB in MB
					VCPUs: 4,
					Disk:  40, // 40GB
					ExtraSpecs: map[string]string{
						"hw:cpu_policy":                         "shared",
						"hw:numa_nodes":                         "2",
						"aggregate_instance_extra_specs:pinned": "false",
					},
				},
			}, nil
		},
		listProjectsFunc: func(ctx context.Context) ([]Project, error) {
			return []Project{
				{ID: "new-project", DomainID: "new-domain", Name: "New Project"},
			}, nil
		},
		listServersFunc: func(ctx context.Context, projects ...Project) (map[string][]Server, error) {
			return map[string][]Server{}, nil // No active servers
		},
		initFunc: func(ctx context.Context) {
			// No-op for init
		},
	}

	syncer := &Syncer{
		CommitmentsClient: mockClient,
		Client:            k8sClient,
	}

	err := syncer.SyncReservations(context.Background())
	if err != nil {
		t.Errorf("SyncReservations() error = %v", err)
		return
	}

	// Verify that the reservation was updated
	var updatedReservation v1alpha1.ComputeReservation
	err = k8sClient.Get(context.Background(), client.ObjectKey{Name: "commitment-12345-0"}, &updatedReservation)
	if err != nil {
		t.Errorf("Failed to get updated reservation: %v", err)
		return
	}

	// Verify the reservation was updated with new values
	if updatedReservation.Spec.Scheduler.CortexNova.ProjectID != "new-project" {
		t.Errorf("Expected project ID new-project, got %v", updatedReservation.Spec.Scheduler.CortexNova.ProjectID)
	}

	if updatedReservation.Spec.Scheduler.CortexNova.FlavorName != "new-flavor" {
		t.Errorf("Expected flavor new-flavor, got %v", updatedReservation.Spec.Scheduler.CortexNova.FlavorName)
	}
}

func TestSyncer_SyncReservations_ShortUUID(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	// Create mock commitment with short UUID (should be skipped)
	mockCommitments := []Commitment{
		{
			ID:               1,
			UUID:             "123", // Too short
			ServiceType:      "compute",
			ResourceName:     "instances_test-flavor",
			AvailabilityZone: "az1",
			Amount:           1,
			Unit:             "",
			ProjectID:        "test-project",
			DomainID:         "test-domain",
		},
	}

	mockClient := &mockCommitmentsClient{
		listCommitmentsByIDFunc: func(ctx context.Context, projects ...Project) (map[string]Commitment, error) {
			result := make(map[string]Commitment)
			for _, c := range mockCommitments {
				result[c.UUID] = c
			}
			return result, nil
		},
		listFlavorsByNameFunc: func(ctx context.Context) (map[string]Flavor, error) {
			return map[string]Flavor{
				"test-flavor": {
					ID:    "flavor-1",
					Name:  "test-flavor",
					RAM:   1024, // 1GB in MB
					VCPUs: 2,
					Disk:  20, // 20GB
					ExtraSpecs: map[string]string{
						"hw:cpu_policy":                         "dedicated",
						"hw:numa_nodes":                         "1",
						"aggregate_instance_extra_specs:pinned": "true",
					},
				},
			}, nil
		},
		listProjectsFunc: func(ctx context.Context) ([]Project, error) {
			return []Project{
				{ID: "test-project", DomainID: "test-domain", Name: "Test Project"},
			}, nil
		},
		listServersFunc: func(ctx context.Context, projects ...Project) (map[string][]Server, error) {
			return map[string][]Server{}, nil // No active servers
		},
		initFunc: func(ctx context.Context) {
			// No-op for init
		},
	}

	syncer := &Syncer{
		CommitmentsClient: mockClient,
		Client:            k8sClient,
	}

	err := syncer.SyncReservations(context.Background())
	if err != nil {
		t.Errorf("SyncReservations() error = %v", err)
		return
	}

	// Verify that no reservations were created due to short UUID
	var reservations v1alpha1.ComputeReservationList
	err = k8sClient.List(context.Background(), &reservations)
	if err != nil {
		t.Errorf("Failed to list reservations: %v", err)
		return
	}

	if len(reservations.Items) != 0 {
		t.Errorf("Expected 0 reservations due to short UUID, got %d", len(reservations.Items))
	}
}

// Note: The Run method starts a goroutine and runs indefinitely, so it's difficult to test
// in a unit test without complex synchronization. In a real-world scenario, you might
// want to add a context cancellation mechanism or a way to stop the sync loop for testing.
func TestSyncer_Run(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	mockClient := &mockCommitmentsClient{}

	syncer := &Syncer{
		CommitmentsClient: mockClient,
		Client:            k8sClient,
	}

	// Test that Run doesn't panic when called
	// We can't easily test the actual loop behavior without complex timing
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately to avoid infinite loop

	// This should not panic
	syncer.Run(ctx)
}
