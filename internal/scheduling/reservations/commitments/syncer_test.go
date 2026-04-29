// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
			Status:           "confirmed",
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
	}

	err := syncer.SyncReservations(context.Background())
	if err != nil {
		t.Errorf("SyncReservations() error = %v", err)
		return
	}

	// Verify one CommittedResource CRD was created with the correct spec
	var crList v1alpha1.CommittedResourceList
	if err := k8sClient.List(context.Background(), &crList); err != nil {
		t.Fatalf("Failed to list committed resources: %v", err)
	}
	if len(crList.Items) != 1 {
		t.Fatalf("Expected 1 CommittedResource, got %d", len(crList.Items))
	}
	cr := crList.Items[0]
	if cr.Name != "commitment-12345-67890-abcdef" {
		t.Errorf("Expected name commitment-12345-67890-abcdef, got %s", cr.Name)
	}
	if cr.Spec.ProjectID != "test-project-1" {
		t.Errorf("Expected projectID test-project-1, got %s", cr.Spec.ProjectID)
	}
	if cr.Spec.FlavorGroupName != "test_group_v1" {
		t.Errorf("Expected flavorGroupName test_group_v1, got %s", cr.Spec.FlavorGroupName)
	}
	if cr.Spec.State != v1alpha1.CommitmentStatusConfirmed {
		t.Errorf("Expected state confirmed, got %s", cr.Spec.State)
	}
	// Amount = 2 slots × 1024 MiB = 2 GiB
	expectedAmount := resource.NewQuantity(2*1024*1024*1024, resource.BinarySI)
	if !cr.Spec.Amount.Equal(*expectedAmount) {
		t.Errorf("Expected amount %v, got %v", expectedAmount, cr.Spec.Amount)
	}
}

func TestSyncer_SyncReservations_UpdateExisting(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

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

	// Pre-existing CommittedResource CRD with stale spec; syncer should update it.
	existingCR := &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{Name: "commitment-12345-67890-abcdef"},
		Spec: v1alpha1.CommittedResourceSpec{
			CommitmentUUID:   "12345-67890-abcdef",
			FlavorGroupName:  "old_group",
			ResourceType:     v1alpha1.CommittedResourceTypeMemory,
			Amount:           *resource.NewQuantity(512*1024*1024, resource.BinarySI),
			ProjectID:        "old-project",
			DomainID:         "old-domain",
			AvailabilityZone: "az1",
			State:            v1alpha1.CommitmentStatusConfirmed,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existingCR, flavorGroupsKnowledge).
		Build()

	mockCommitments := []Commitment{
		{
			ID:               1,
			UUID:             "12345-67890-abcdef",
			ServiceType:      "compute",
			ResourceName:     "hw_version_new_group_v1_ram",
			AvailabilityZone: "az1",
			Amount:           1,
			Unit:             "",
			Status:           "confirmed",
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
			return []Project{{ID: "new-project", DomainID: "new-domain"}}, nil
		},
	}

	syncer := &Syncer{CommitmentsClient: mockClient, Client: k8sClient}

	if err := syncer.SyncReservations(context.Background()); err != nil {
		t.Fatalf("SyncReservations() error = %v", err)
	}

	var crList v1alpha1.CommittedResourceList
	if err := k8sClient.List(context.Background(), &crList); err != nil {
		t.Fatalf("Failed to list committed resources: %v", err)
	}
	if len(crList.Items) != 1 {
		t.Fatalf("Expected 1 CommittedResource, got %d", len(crList.Items))
	}
	cr := crList.Items[0]
	if cr.Spec.ProjectID != "new-project" {
		t.Errorf("Expected projectID new-project, got %s", cr.Spec.ProjectID)
	}
	if cr.Spec.FlavorGroupName != "new_group_v1" {
		t.Errorf("Expected flavorGroupName new_group_v1, got %s", cr.Spec.FlavorGroupName)
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
			Status:           "confirmed",
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
	}

	err := syncer.SyncReservations(context.Background())
	if err != nil {
		t.Errorf("SyncReservations() error = %v", err)
		return
	}

	// Verify that NO CommittedResource CRDs were created due to unit mismatch.
	// The commitment is skipped and Cortex trusts existing CRDs.
	var crList v1alpha1.CommittedResourceList
	if err := k8sClient.List(context.Background(), &crList); err != nil {
		t.Fatalf("Failed to list committed resources: %v", err)
	}
	if len(crList.Items) != 0 {
		t.Errorf("Expected 0 CommittedResource CRDs (commitment skipped due to unit mismatch), got %d", len(crList.Items))
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
			Status:           "confirmed",
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
	}

	err := syncer.SyncReservations(context.Background())
	if err != nil {
		t.Errorf("SyncReservations() error = %v", err)
		return
	}

	// Verify that one CommittedResource CRD was created
	var crList v1alpha1.CommittedResourceList
	if err := k8sClient.List(context.Background(), &crList); err != nil {
		t.Fatalf("Failed to list committed resources: %v", err)
	}
	if len(crList.Items) != 1 {
		t.Errorf("Expected 1 CommittedResource CRD, got %d", len(crList.Items))
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
			Status:           "confirmed",
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
	}

	err := syncer.SyncReservations(context.Background())
	if err != nil {
		t.Errorf("SyncReservations() error = %v", err)
		return
	}

	// Verify that no CommittedResource CRDs were created due to empty UUID
	var crList v1alpha1.CommittedResourceList
	if err := k8sClient.List(context.Background(), &crList); err != nil {
		t.Fatalf("Failed to list committed resources: %v", err)
	}
	if len(crList.Items) != 0 {
		t.Errorf("Expected 0 CommittedResource CRDs due to empty UUID, got %d", len(crList.Items))
	}
}

func TestSyncer_SyncReservations_StatusFilter(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

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

	tests := []struct {
		name     string
		status   string
		expectCR bool
	}{
		{"confirmed creates CR", "confirmed", true},
		{"guaranteed creates CR", "guaranteed", true},
		{"planned creates CR", "planned", true},
		{"pending creates CR", "pending", true},
		{"superseded does not create CR", "superseded", false},
		{"expired does not create CR", "expired", false},
		{"empty status is skipped", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(flavorGroupsKnowledge).
				Build()

			mockCommitments := []Commitment{
				{
					ID:               1,
					UUID:             "test-uuid-status-filter",
					ServiceType:      "compute",
					ResourceName:     "hw_version_test_group_v1_ram",
					AvailabilityZone: "az1",
					Amount:           1,
					Status:           tc.status,
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
					return []Project{{ID: "test-project", DomainID: "test-domain"}}, nil
				},
			}

			syncer := &Syncer{
				CommitmentsClient: mockClient,
				Client:            k8sClient,
				monitor:           monitor,
			}

			if err := syncer.SyncReservations(context.Background()); err != nil {
				t.Fatalf("SyncReservations() error = %v", err)
			}

			var crList v1alpha1.CommittedResourceList
			if err := k8sClient.List(context.Background(), &crList); err != nil {
				t.Fatalf("Failed to list committed resources: %v", err)
			}

			if tc.expectCR && len(crList.Items) == 0 {
				t.Errorf("status=%q: expected CommittedResource CRD to be created, got none", tc.status)
			}
			if !tc.expectCR && len(crList.Items) != 0 {
				t.Errorf("status=%q: expected no CommittedResource CRD, got %d", tc.status, len(crList.Items))
			}
		})
	}
}

func TestSyncer_SyncReservations_StaleCRCount(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

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

	// Pre-existing CRD whose commitment no longer appears in Limes
	staleCR := &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{Name: "commitment-stale-uuid-1234"},
		Spec: v1alpha1.CommittedResourceSpec{
			CommitmentUUID:   "stale-uuid-1234",
			FlavorGroupName:  "test_group_v1",
			ResourceType:     v1alpha1.CommittedResourceTypeMemory,
			Amount:           *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			ProjectID:        "test-project",
			DomainID:         "test-domain",
			AvailabilityZone: "az1",
			State:            v1alpha1.CommitmentStatusConfirmed,
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(staleCR, flavorGroupsKnowledge).
		Build()

	// Limes returns no commitments (stale-uuid-1234 is gone)
	mockClient := &mockCommitmentsClient{
		listCommitmentsByIDFunc: func(ctx context.Context, projects ...Project) (map[string]Commitment, error) {
			return map[string]Commitment{}, nil
		},
		listProjectsFunc: func(ctx context.Context) ([]Project, error) {
			return []Project{{ID: "test-project", DomainID: "test-domain"}}, nil
		},
	}

	monitor := NewSyncerMonitor()
	syncer := &Syncer{CommitmentsClient: mockClient, Client: k8sClient, monitor: monitor}

	if err := syncer.SyncReservations(context.Background()); err != nil {
		t.Fatalf("SyncReservations() error = %v", err)
	}

	// Stale CRD must still exist (syncer does not delete)
	var crList v1alpha1.CommittedResourceList
	if err := k8sClient.List(context.Background(), &crList); err != nil {
		t.Fatalf("Failed to list committed resources: %v", err)
	}
	if len(crList.Items) != 1 {
		t.Errorf("Expected stale CRD to be preserved, got %d CRDs", len(crList.Items))
	}

	// Gauge must reflect the stale count
	ch := make(chan prometheus.Metric, 10)
	monitor.staleCRs.Collect(ch)
	close(ch)
	m := <-ch
	var dto dto.Metric
	if err := m.Write(&dto); err != nil {
		t.Fatalf("failed to read metric: %v", err)
	}
	if got := dto.GetGauge().GetValue(); got != 1 {
		t.Errorf("Expected staleCRs gauge=1, got %v", got)
	}
}

func TestSyncer_SyncReservations_TerminalState_NoCRDExists(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	flavorGroupsKnowledge := createFlavorGroupKnowledge(t, map[string]FlavorGroupData{
		"test_group_v1": {SmallestFlavorName: "f", SmallestFlavorVCPUs: 2, SmallestFlavorMemoryMB: 1024,
			LargestFlavorName: "f", LargestFlavorVCPUs: 2, LargestFlavorMemoryMB: 1024},
	})
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(flavorGroupsKnowledge).Build()

	for _, status := range []string{"superseded", "expired"} {
		t.Run(status, func(t *testing.T) {
			mockClient := &mockCommitmentsClient{
				listCommitmentsByIDFunc: func(ctx context.Context, projects ...Project) (map[string]Commitment, error) {
					return map[string]Commitment{
						"term-uuid-1234": {
							ID: 1, UUID: "term-uuid-1234", ServiceType: "compute",
							ResourceName: "hw_version_test_group_v1_ram", AvailabilityZone: "az1",
							Amount: 1, Status: status, ProjectID: "p", DomainID: "d",
						},
					}, nil
				},
				listProjectsFunc: func(ctx context.Context) ([]Project, error) {
					return []Project{{ID: "p", DomainID: "d"}}, nil
				},
			}
			syncer := &Syncer{CommitmentsClient: mockClient, Client: k8sClient}
			if err := syncer.SyncReservations(context.Background()); err != nil {
				t.Fatalf("SyncReservations() error = %v", err)
			}
			var crList v1alpha1.CommittedResourceList
			if err := k8sClient.List(context.Background(), &crList); err != nil {
				t.Fatalf("Failed to list: %v", err)
			}
			if len(crList.Items) != 0 {
				t.Errorf("status=%q: expected no CRD to be created, got %d", status, len(crList.Items))
			}
		})
	}
}

func TestSyncer_SyncReservations_TerminalState_ExistingCRDUpdated(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	flavorGroupsKnowledge := createFlavorGroupKnowledge(t, map[string]FlavorGroupData{
		"test_group_v1": {SmallestFlavorName: "f", SmallestFlavorVCPUs: 2, SmallestFlavorMemoryMB: 1024,
			LargestFlavorName: "f", LargestFlavorVCPUs: 2, LargestFlavorMemoryMB: 1024},
	})

	existingCR := &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{Name: "commitment-term-uuid-1234"},
		Spec: v1alpha1.CommittedResourceSpec{
			CommitmentUUID: "term-uuid-1234", FlavorGroupName: "test_group_v1",
			ResourceType: v1alpha1.CommittedResourceTypeMemory,
			Amount:       *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			ProjectID:    "p", DomainID: "d", AvailabilityZone: "az1",
			State: v1alpha1.CommitmentStatusConfirmed,
		},
	}

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingCR, flavorGroupsKnowledge).Build()

	mockClient := &mockCommitmentsClient{
		listCommitmentsByIDFunc: func(ctx context.Context, projects ...Project) (map[string]Commitment, error) {
			return map[string]Commitment{
				"term-uuid-1234": {
					ID: 1, UUID: "term-uuid-1234", ServiceType: "compute",
					ResourceName: "hw_version_test_group_v1_ram", AvailabilityZone: "az1",
					Amount: 1, Status: "superseded", ProjectID: "p", DomainID: "d",
				},
			}, nil
		},
		listProjectsFunc: func(ctx context.Context) ([]Project, error) {
			return []Project{{ID: "p", DomainID: "d"}}, nil
		},
	}

	syncer := &Syncer{CommitmentsClient: mockClient, Client: k8sClient}
	if err := syncer.SyncReservations(context.Background()); err != nil {
		t.Fatalf("SyncReservations() error = %v", err)
	}

	var crList v1alpha1.CommittedResourceList
	if err := k8sClient.List(context.Background(), &crList); err != nil {
		t.Fatalf("Failed to list: %v", err)
	}
	if len(crList.Items) != 1 {
		t.Fatalf("Expected CRD to be preserved, got %d", len(crList.Items))
	}
	if crList.Items[0].Spec.State != v1alpha1.CommitmentStatusSuperseded {
		t.Errorf("Expected state superseded, got %s", crList.Items[0].Spec.State)
	}
}

func TestSyncer_SyncReservations_ExpiredByTime_NoCRDCreated(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	flavorGroupsKnowledge := createFlavorGroupKnowledge(t, map[string]FlavorGroupData{
		"test_group_v1": {SmallestFlavorName: "f", SmallestFlavorVCPUs: 2, SmallestFlavorMemoryMB: 1024,
			LargestFlavorName: "f", LargestFlavorVCPUs: 2, LargestFlavorMemoryMB: 1024},
	})
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(flavorGroupsKnowledge).Build()

	pastTime := uint64(1) // Unix epoch — well in the past
	mockClient := &mockCommitmentsClient{
		listCommitmentsByIDFunc: func(ctx context.Context, projects ...Project) (map[string]Commitment, error) {
			return map[string]Commitment{
				"exp-uuid-1234": {
					ID: 1, UUID: "exp-uuid-1234", ServiceType: "compute",
					ResourceName: "hw_version_test_group_v1_ram", AvailabilityZone: "az1",
					Amount: 1, Status: "confirmed", ExpiresAt: pastTime,
					ProjectID: "p", DomainID: "d",
				},
			}, nil
		},
		listProjectsFunc: func(ctx context.Context) ([]Project, error) {
			return []Project{{ID: "p", DomainID: "d"}}, nil
		},
	}

	syncer := &Syncer{CommitmentsClient: mockClient, Client: k8sClient}
	if err := syncer.SyncReservations(context.Background()); err != nil {
		t.Fatalf("SyncReservations() error = %v", err)
	}

	var crList v1alpha1.CommittedResourceList
	if err := k8sClient.List(context.Background(), &crList); err != nil {
		t.Fatalf("Failed to list: %v", err)
	}
	if len(crList.Items) != 0 {
		t.Errorf("Expected no CRD created for past-expiry confirmed commitment, got %d", len(crList.Items))
	}
}

func TestSyncer_SyncReservations_GC_ExpiredEndTime(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	flavorGroupsKnowledge := createFlavorGroupKnowledge(t, map[string]FlavorGroupData{
		"test_group_v1": {SmallestFlavorName: "f", SmallestFlavorVCPUs: 2, SmallestFlavorMemoryMB: 1024,
			LargestFlavorName: "f", LargestFlavorVCPUs: 2, LargestFlavorMemoryMB: 1024},
	})

	pastTime := metav1.NewTime(time.Now().Add(-time.Hour))
	expiredCR := &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{Name: "commitment-gc-uuid-1234"},
		Spec: v1alpha1.CommittedResourceSpec{
			CommitmentUUID: "gc-uuid-1234", FlavorGroupName: "test_group_v1",
			ResourceType: v1alpha1.CommittedResourceTypeMemory,
			Amount:       *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			ProjectID:    "p", DomainID: "d", AvailabilityZone: "az1",
			State:            v1alpha1.CommitmentStatusConfirmed,
			EndTime:          &pastTime,
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
		},
	}

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(expiredCR, flavorGroupsKnowledge).Build()

	// Limes no longer returns this commitment
	mockClient := &mockCommitmentsClient{
		listCommitmentsByIDFunc: func(ctx context.Context, projects ...Project) (map[string]Commitment, error) {
			return map[string]Commitment{}, nil
		},
		listProjectsFunc: func(ctx context.Context) ([]Project, error) {
			return []Project{{ID: "p", DomainID: "d"}}, nil
		},
	}

	syncer := &Syncer{CommitmentsClient: mockClient, Client: k8sClient}
	if err := syncer.SyncReservations(context.Background()); err != nil {
		t.Fatalf("SyncReservations() error = %v", err)
	}

	var crList v1alpha1.CommittedResourceList
	if err := k8sClient.List(context.Background(), &crList); err != nil {
		t.Fatalf("Failed to list: %v", err)
	}
	if len(crList.Items) != 0 {
		t.Errorf("Expected expired CRD to be GC'd, got %d CRDs", len(crList.Items))
	}
}
