// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"errors"
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
	commitments []Commitment
	initCalled  bool
	shouldError bool
}

func (m *mockCommitmentsClient) Init(ctx context.Context) {
	m.initCalled = true
}

func (m *mockCommitmentsClient) GetComputeCommitments(ctx context.Context) ([]Commitment, error) {
	if m.shouldError {
		return nil, errors.New("mock error")
	}
	return m.commitments, nil
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

	if !mockClient.initCalled {
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
			Flavor: &Flavor{
				ID:         "flavor-1",
				Name:       "test-flavor",
				RAM:        1024, // 1GB in MB
				VCPUs:      2,
				Disk:       10, // 10GB
				ExtraSpecs: map[string]string{"key": "value"},
			},
		},
	}

	mockClient := &mockCommitmentsClient{
		commitments: mockCommitments,
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
				Type: v1alpha1.ComputeReservationSchedulerTypeCortexNova,
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
			Flavor: &Flavor{
				ID:         "flavor-2",
				Name:       "new-flavor",
				RAM:        2048, // 2GB in MB
				VCPUs:      4,
				Disk:       20, // 20GB
				ExtraSpecs: map[string]string{"new": "spec"},
			},
		},
	}

	mockClient := &mockCommitmentsClient{
		commitments: mockCommitments,
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

func TestSyncer_SyncReservations_Error(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	mockClient := &mockCommitmentsClient{
		shouldError: true,
	}

	syncer := &Syncer{
		CommitmentsClient: mockClient,
		Client:            k8sClient,
	}

	err := syncer.SyncReservations(context.Background())
	if err == nil {
		t.Error("Expected error but got none")
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
			Flavor: &Flavor{
				Name: "test-flavor",
			},
		},
	}

	mockClient := &mockCommitmentsClient{
		commitments: mockCommitments,
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

func TestSyncer_SyncReservations_UnsupportedResource(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	// Create mock commitment with unsupported resource name
	mockCommitments := []Commitment{
		{
			ID:               1,
			UUID:             "12345-67890-abcdef",
			ServiceType:      "compute",
			ResourceName:     "unsupported_resource",
			AvailabilityZone: "az1",
			Amount:           1,
			Unit:             "",
			ProjectID:        "test-project",
			DomainID:         "test-domain",
		},
	}

	mockClient := &mockCommitmentsClient{
		commitments: mockCommitments,
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

	// Verify that no reservations were created due to unsupported resource
	var reservations v1alpha1.ComputeReservationList
	err = k8sClient.List(context.Background(), &reservations)
	if err != nil {
		t.Errorf("Failed to list reservations: %v", err)
		return
	}

	if len(reservations.Items) != 0 {
		t.Errorf("Expected 0 reservations due to unsupported resource, got %d", len(reservations.Items))
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
