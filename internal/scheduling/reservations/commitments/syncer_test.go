// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"sort"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// FlavorGroupData holds test data for creating a flavor group
type FlavorGroupData struct {
	LargestFlavorName      string
	LargestFlavorVCPUs     uint64
	LargestFlavorMemoryMB  uint64
	SmallestFlavorName     string
	SmallestFlavorVCPUs    uint64
	SmallestFlavorMemoryMB uint64
}

// createFlavorGroupKnowledge creates a Knowledge CRD with flavor group data for testing
func createFlavorGroupKnowledge(t *testing.T, groups map[string]FlavorGroupData) *v1alpha1.Knowledge {
	t.Helper()

	// Sort group names for deterministic iteration
	sortedGroupNames := make([]string, 0, len(groups))
	for groupName := range groups {
		sortedGroupNames = append(sortedGroupNames, groupName)
	}
	sort.Strings(sortedGroupNames)

	// Build flavor group features with sorted iteration
	features := make([]compute.FlavorGroupFeature, 0, len(groups))
	for _, groupName := range sortedGroupNames {
		data := groups[groupName]
		features = append(features, compute.FlavorGroupFeature{
			Name: groupName,
			Flavors: []compute.FlavorInGroup{
				{
					Name:     data.LargestFlavorName,
					VCPUs:    data.LargestFlavorVCPUs,
					MemoryMB: data.LargestFlavorMemoryMB,
				},
				{
					Name:     data.SmallestFlavorName,
					VCPUs:    data.SmallestFlavorVCPUs,
					MemoryMB: data.SmallestFlavorMemoryMB,
				},
			},
			LargestFlavor: compute.FlavorInGroup{
				Name:     data.LargestFlavorName,
				VCPUs:    data.LargestFlavorVCPUs,
				MemoryMB: data.LargestFlavorMemoryMB,
			},
			SmallestFlavor: compute.FlavorInGroup{
				Name:     data.SmallestFlavorName,
				VCPUs:    data.SmallestFlavorVCPUs,
				MemoryMB: data.SmallestFlavorMemoryMB,
			},
		})
	}

	// Box the features
	rawFeatures, err := v1alpha1.BoxFeatureList(features)
	if err != nil {
		t.Fatalf("Failed to box flavor group features: %v", err)
	}

	return &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "flavor-groups",
		},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor: v1alpha1.KnowledgeExtractorSpec{
				Name: "flavor_groups",
			},
		},
		Status: v1alpha1.KnowledgeStatus{
			Raw: rawFeatures,
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.KnowledgeConditionReady,
					Status: metav1.ConditionTrue,
					Reason: "ExtractorSucceeded",
				},
			},
		},
	}
}

// Mock CommitmentsClient for testing
type mockCommitmentsClient struct {
	initFunc                      func(ctx context.Context, client client.Client, conf SyncerConfig) error
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

func (m *mockCommitmentsClient) Init(ctx context.Context, client client.Client, conf SyncerConfig) error {
	m.initFuncCalled = true
	if m.initFunc != nil {
		return m.initFunc(ctx, client, conf)
	}
	return nil
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

	if err := syncer.Init(context.Background(), SyncerConfig{}); err != nil {
		t.Errorf("Syncer.Init() error = %v", err)
	}

	if !mockClient.initFuncCalled {
		t.Error("Expected Init to be called on commitments client")
	}
}

func TestSyncer_SyncReservations_InstanceCommitments(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// Create flavor group knowledge CRD
	flavorGroupsKnowledge := createFlavorGroupKnowledge(t, map[string]FlavorGroupData{
		"test_group_v1": {
			LargestFlavorName:      "test-flavor",
			LargestFlavorVCPUs:     2,
			LargestFlavorMemoryMB:  1024,
			SmallestFlavorName:     "test-flavor",
			SmallestFlavorVCPUs:    2,
			SmallestFlavorMemoryMB: 1024,
		},
	})

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(flavorGroupsKnowledge).
		Build()

	// Create mock commitments with flavor group resources (using ram_ prefix)
	mockCommitments := []Commitment{
		{
			ID:               1,
			UUID:             "12345-67890-abcdef",
			ServiceType:      "compute",
			ResourceName:     "hw_version_test_group_v1_ram",
			AvailabilityZone: "az1",
			Amount:           2, // 2 multiples of smallest flavor (2 * 1024MB = 2048MB total)
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
		listProjectsFunc: func(ctx context.Context) ([]Project, error) {
			return []Project{
				{ID: "test-project-1", DomainID: "test-domain-1", Name: "Test Project 1"},
			}, nil
		},
		listServersFunc: func(ctx context.Context, projects ...Project) (map[string][]Server, error) {
			return map[string][]Server{}, nil // No active servers
		},
		initFunc: func(ctx context.Context, client client.Client, conf SyncerConfig) error {
			// No-op for init
			return nil
		},
	}

	syncer := &Syncer{
		CommitmentsClient: mockClient,
		Client:            k8sClient,
		crMutex:           &CRMutex{},
	}

	err := syncer.SyncReservations(context.Background())
	if err != nil {
		t.Errorf("SyncReservations() error = %v", err)
		return
	}

	// Verify that reservations were created
	var reservations v1alpha1.ReservationList
	err = k8sClient.List(context.Background(), &reservations)
	if err != nil {
		t.Errorf("Failed to list reservations: %v", err)
		return
	}

	// Should have 2 reservations (Amount = 2, each for smallest flavor)
	if len(reservations.Items) != 2 {
		t.Errorf("Expected 2 reservations, got %d", len(reservations.Items))
		return
	}

	// Verify the first reservation
	res := reservations.Items[0]
	if res.Spec.CommittedResourceReservation == nil {
		t.Errorf("Expected CommittedResourceReservation to be set")
		return
	}
	if res.Spec.CommittedResourceReservation.ProjectID != "test-project-1" {
		t.Errorf("Expected project ID test-project-1, got %v", res.Spec.CommittedResourceReservation.ProjectID)
	}

	if res.Spec.CommittedResourceReservation.ResourceGroup != "test_group_v1" {
		t.Errorf("Expected resource group test_group_v1, got %v", res.Spec.CommittedResourceReservation.ResourceGroup)
	}

	// Check resource values - should be sized for the flavor that fits
	// With 2048MB total capacity, we can fit 2x 1024MB flavors
	expectedMemory := resource.MustParse("1073741824") // 1024MB in bytes
	if !res.Spec.Resources[hv1.ResourceMemory].Equal(expectedMemory) {
		t.Errorf("Expected memory %v, got %v", expectedMemory, res.Spec.Resources[hv1.ResourceMemory])
	}

	expectedVCPUs := resource.MustParse("2")
	if !res.Spec.Resources[hv1.ResourceCPU].Equal(expectedVCPUs) {
		t.Errorf("Expected vCPUs %v, got %v", expectedVCPUs, res.Spec.Resources[hv1.ResourceCPU])
	}
}

func TestSyncer_SyncReservations_UpdateExisting(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// Create flavor group knowledge CRD
	flavorGroupsKnowledge := createFlavorGroupKnowledge(t, map[string]FlavorGroupData{
		"new_group_v1": {
			LargestFlavorName:      "new-flavor",
			LargestFlavorVCPUs:     4,
			LargestFlavorMemoryMB:  2048,
			SmallestFlavorName:     "new-flavor-small",
			SmallestFlavorVCPUs:    2,
			SmallestFlavorMemoryMB: 1024,
		},
	})

	// Create an existing reservation with mismatched project/flavor group
	// The ReservationManager will delete this and create a new one
	existingReservation := &v1alpha1.Reservation{
		ObjectMeta: ctrl.ObjectMeta{
			Name: "commitment-12345-67890-abcdef-0",
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Type: v1alpha1.ReservationTypeCommittedResource,
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:     "old-project",
				ResourceName:  "old-flavor",
				ResourceGroup: "old_group",
				Creator:       CreatorValue,
			},
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse("512Mi"),
				hv1.ResourceCPU:    resource.MustParse("1"),
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existingReservation, flavorGroupsKnowledge).
		Build()

	// Create mock commitment that will replace the existing reservation
	mockCommitments := []Commitment{
		{
			ID:               1,
			UUID:             "12345-67890-abcdef",
			ServiceType:      "compute",
			ResourceName:     "hw_version_new_group_v1_ram",
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
		listProjectsFunc: func(ctx context.Context) ([]Project, error) {
			return []Project{
				{ID: "new-project", DomainID: "new-domain", Name: "New Project"},
			}, nil
		},
		listServersFunc: func(ctx context.Context, projects ...Project) (map[string][]Server, error) {
			return map[string][]Server{}, nil // No active servers
		},
		initFunc: func(ctx context.Context, client client.Client, conf SyncerConfig) error {
			// No-op for init
			return nil
		},
	}

	syncer := &Syncer{
		CommitmentsClient: mockClient,
		Client:            k8sClient,
		crMutex:           &CRMutex{},
	}

	err := syncer.SyncReservations(context.Background())
	if err != nil {
		t.Errorf("SyncReservations() error = %v", err)
		return
	}

	// Verify that reservations were updated (old one deleted, new one created)
	// The new reservation will be at index 0 since the old one was deleted first
	var reservations v1alpha1.ReservationList
	err = k8sClient.List(context.Background(), &reservations)
	if err != nil {
		t.Errorf("Failed to list reservations: %v", err)
		return
	}

	if len(reservations.Items) != 1 {
		t.Errorf("Expected 1 reservation, got %d", len(reservations.Items))
		return
	}

	newReservation := reservations.Items[0]

	// Verify the new reservation has correct values
	if newReservation.Spec.CommittedResourceReservation == nil {
		t.Errorf("Expected CommittedResourceReservation to be set")
		return
	}
	if newReservation.Spec.CommittedResourceReservation.ProjectID != "new-project" {
		t.Errorf("Expected project ID new-project, got %v", newReservation.Spec.CommittedResourceReservation.ProjectID)
	}

	if newReservation.Spec.CommittedResourceReservation.ResourceGroup != "new_group_v1" {
		t.Errorf("Expected resource group new_group_v1, got %v", newReservation.Spec.CommittedResourceReservation.ResourceGroup)
	}
}

func TestSyncer_SyncReservations_UnitMismatch(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// Create flavor group knowledge CRD with smallest flavor of 1024MB
	flavorGroupsKnowledge := createFlavorGroupKnowledge(t, map[string]FlavorGroupData{
		"test_group_v1": {
			LargestFlavorName:      "test-flavor-large",
			LargestFlavorVCPUs:     8,
			LargestFlavorMemoryMB:  8192,
			SmallestFlavorName:     "test-flavor-small",
			SmallestFlavorVCPUs:    2,
			SmallestFlavorMemoryMB: 1024,
		},
	})

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(flavorGroupsKnowledge).
		Build()

	// Create mock commitment with a unit that doesn't match Cortex's understanding
	// Limes says "2048 MiB" but Cortex's smallest flavor is 1024 MB
	mockCommitments := []Commitment{
		{
			ID:               1,
			UUID:             "unit-mismatch-test-uuid",
			ServiceType:      "compute",
			ResourceName:     "hw_version_test_group_v1_ram",
			AvailabilityZone: "az1",
			Amount:           2,
			Unit:             "2048 MiB", // Mismatched unit - should be "1024 MiB"
			ProjectID:        "test-project",
			DomainID:         "test-domain",
		},
	}

	// Create monitor to capture the mismatch metric
	monitor := NewSyncerMonitor()

	mockClient := &mockCommitmentsClient{
		listCommitmentsByIDFunc: func(ctx context.Context, projects ...Project) (map[string]Commitment, error) {
			result := make(map[string]Commitment)
			for _, c := range mockCommitments {
				result[c.UUID] = c
			}
			return result, nil
		},
		listProjectsFunc: func(ctx context.Context) ([]Project, error) {
			return []Project{
				{ID: "test-project", DomainID: "test-domain", Name: "Test Project"},
			}, nil
		},
	}

	syncer := &Syncer{
		CommitmentsClient: mockClient,
		Client:            k8sClient,
		monitor:           monitor,
		crMutex:           &CRMutex{},
	}

	err := syncer.SyncReservations(context.Background())
	if err != nil {
		t.Errorf("SyncReservations() error = %v", err)
		return
	}

	// Verify that NO reservations were created due to unit mismatch
	// The commitment is skipped and Cortex trusts existing CRDs
	var reservations v1alpha1.ReservationList
	err = k8sClient.List(context.Background(), &reservations)
	if err != nil {
		t.Errorf("Failed to list reservations: %v", err)
		return
	}

	// Should have 0 reservations - commitment is skipped due to unit mismatch
	// Cortex waits for Limes to update the unit before processing
	if len(reservations.Items) != 0 {
		t.Errorf("Expected 0 reservations (commitment skipped due to unit mismatch), got %d", len(reservations.Items))
	}
}

func TestSyncer_SyncReservations_UnitMatch(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// Create flavor group knowledge CRD with smallest flavor of 1024MB
	flavorGroupsKnowledge := createFlavorGroupKnowledge(t, map[string]FlavorGroupData{
		"test_group_v1": {
			LargestFlavorName:      "test-flavor-large",
			LargestFlavorVCPUs:     8,
			LargestFlavorMemoryMB:  8192,
			SmallestFlavorName:     "test-flavor-small",
			SmallestFlavorVCPUs:    2,
			SmallestFlavorMemoryMB: 1024,
		},
	})

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(flavorGroupsKnowledge).
		Build()

	// Create mock commitment with correct unit matching Cortex's smallest flavor
	mockCommitments := []Commitment{
		{
			ID:               1,
			UUID:             "unit-match-test-uuid",
			ServiceType:      "compute",
			ResourceName:     "hw_version_test_group_v1_ram",
			AvailabilityZone: "az1",
			Amount:           2,
			Unit:             "1024 MiB", // Correct unit matching smallest flavor
			ProjectID:        "test-project",
			DomainID:         "test-domain",
		},
	}

	monitor := NewSyncerMonitor()

	mockClient := &mockCommitmentsClient{
		listCommitmentsByIDFunc: func(ctx context.Context, projects ...Project) (map[string]Commitment, error) {
			result := make(map[string]Commitment)
			for _, c := range mockCommitments {
				result[c.UUID] = c
			}
			return result, nil
		},
		listProjectsFunc: func(ctx context.Context) ([]Project, error) {
			return []Project{
				{ID: "test-project", DomainID: "test-domain", Name: "Test Project"},
			}, nil
		},
	}

	syncer := &Syncer{
		CommitmentsClient: mockClient,
		Client:            k8sClient,
		monitor:           monitor,
		crMutex:           &CRMutex{},
	}

	err := syncer.SyncReservations(context.Background())
	if err != nil {
		t.Errorf("SyncReservations() error = %v", err)
		return
	}

	// Verify that reservations were created
	var reservations v1alpha1.ReservationList
	err = k8sClient.List(context.Background(), &reservations)
	if err != nil {
		t.Errorf("Failed to list reservations: %v", err)
		return
	}

	if len(reservations.Items) != 2 {
		t.Errorf("Expected 2 reservations, got %d", len(reservations.Items))
	}
}

func TestSyncer_SyncReservations_EmptyUUID(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// Create flavor group knowledge CRD
	flavorGroupsKnowledge := createFlavorGroupKnowledge(t, map[string]FlavorGroupData{
		"test_group_v1": {
			LargestFlavorName:      "test-flavor",
			LargestFlavorVCPUs:     2,
			LargestFlavorMemoryMB:  1024,
			SmallestFlavorName:     "test-flavor",
			SmallestFlavorVCPUs:    2,
			SmallestFlavorMemoryMB: 1024,
		},
	})

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(flavorGroupsKnowledge).
		Build()

	// Create mock commitment with empty UUID (should be skipped)
	mockCommitments := []Commitment{
		{
			ID:               1,
			UUID:             "", // Empty UUID
			ServiceType:      "compute",
			ResourceName:     "hw_version_test_group_v1_ram",
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
		listProjectsFunc: func(ctx context.Context) ([]Project, error) {
			return []Project{
				{ID: "test-project", DomainID: "test-domain", Name: "Test Project"},
			}, nil
		},
		listServersFunc: func(ctx context.Context, projects ...Project) (map[string][]Server, error) {
			return map[string][]Server{}, nil // No active servers
		},
		initFunc: func(ctx context.Context, client client.Client, conf SyncerConfig) error {
			// No-op for init
			return nil
		},
	}

	syncer := &Syncer{
		CommitmentsClient: mockClient,
		Client:            k8sClient,
		crMutex:           &CRMutex{},
	}

	err := syncer.SyncReservations(context.Background())
	if err != nil {
		t.Errorf("SyncReservations() error = %v", err)
		return
	}

	// Verify that no reservations were created due to empty UUID
	var reservations v1alpha1.ReservationList
	err = k8sClient.List(context.Background(), &reservations)
	if err != nil {
		t.Errorf("Failed to list reservations: %v", err)
		return
	}

	if len(reservations.Items) != 0 {
		t.Errorf("Expected 0 reservations due to empty UUID, got %d", len(reservations.Items))
	}
}
