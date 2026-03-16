// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"strings"
	"testing"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestHypervisorOvercommitMapping_Validate(t *testing.T) {
	tests := []struct {
		name        string
		mapping     HypervisorOvercommitMapping
		expectError bool
	}{
		{
			name: "valid overcommit ratios",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{
					hv1.ResourceCPU:    2.0,
					hv1.ResourceMemory: 1.5,
				},
			},
			expectError: false,
		},
		{
			name: "valid minimum overcommit ratio of 1.0",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{
					hv1.ResourceCPU: 1.0,
				},
			},
			expectError: false,
		},
		{
			name: "invalid overcommit ratio less than 1.0",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{
					hv1.ResourceCPU: 0.5,
				},
			},
			expectError: true,
		},
		{
			name: "invalid overcommit ratio of zero",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{
					hv1.ResourceMemory: 0.0,
				},
			},
			expectError: true,
		},
		{
			name: "invalid negative overcommit ratio",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{
					hv1.ResourceCPU: -1.0,
				},
			},
			expectError: true,
		},
		{
			name: "empty overcommit map is valid",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{},
			},
			expectError: false,
		},
		{
			name: "nil overcommit map is valid",
			mapping: HypervisorOvercommitMapping{
				Overcommit: nil,
			},
			expectError: false,
		},
		{
			name: "mixed valid and invalid overcommit ratios",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{
					hv1.ResourceCPU:    2.0,
					hv1.ResourceMemory: 0.5, // invalid
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mapping.Validate()
			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
		})
	}
}

func TestHypervisorOvercommitConfig_Validate(t *testing.T) {
	trait := "CUSTOM_GPU"
	tests := []struct {
		name        string
		config      HypervisorOvercommitConfig
		expectError bool
	}{
		{
			name: "valid config with single mapping",
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 2.0,
						},
						HasTrait: &trait,
					},
				},
			},
			expectError: false,
		},
		{
			name: "valid config with multiple mappings",
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 2.0,
						},
						HasTrait: &trait,
					},
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceMemory: 1.5,
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "invalid config with bad mapping",
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 0.5, // invalid
						},
					},
				},
			},
			expectError: true,
		},
		{
			name: "empty config is valid",
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{},
			},
			expectError: false,
		},
		{
			name: "nil mappings is valid",
			config: HypervisorOvercommitConfig{
				OvercommitMappings: nil,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
		})
	}
}

func newTestHypervisorScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hv1 to scheme: %v", err)
	}
	return scheme
}

func TestHypervisorOvercommitController_Reconcile(t *testing.T) {
	scheme := newTestHypervisorScheme(t)

	gpuTrait := "CUSTOM_GPU"
	standardTrait := "CUSTOM_STANDARD"
	missingTrait := "CUSTOM_MISSING"

	tests := []struct {
		name                string
		hypervisor          *hv1.Hypervisor
		config              HypervisorOvercommitConfig
		expectedOvercommit  map[hv1.ResourceName]float64
		expectNoUpdate      bool
		expectNotFoundError bool
	}{
		{
			name: "apply overcommit for matching HasTrait",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{"CUSTOM_GPU"},
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 4.0,
						},
						HasTrait: &gpuTrait,
					},
				},
			},
			expectedOvercommit: map[hv1.ResourceName]float64{
				hv1.ResourceCPU: 4.0,
			},
		},
		{
			name: "apply overcommit for matching HasntTrait",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{}, // missing trait
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 2.0,
						},
						HasntTrait: &missingTrait,
					},
				},
			},
			expectedOvercommit: map[hv1.ResourceName]float64{
				hv1.ResourceCPU: 2.0,
			},
		},
		{
			name: "skip mapping when HasTrait not present",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{"CUSTOM_OTHER"},
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 4.0,
						},
						HasTrait: &gpuTrait,
					},
				},
			},
			expectedOvercommit: map[hv1.ResourceName]float64{},
		},
		{
			name: "skip mapping when HasntTrait is present",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{"CUSTOM_GPU"}, // trait is present
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 2.0,
						},
						HasntTrait: &gpuTrait, // should skip because GPU trait IS present
					},
				},
			},
			expectedOvercommit: map[hv1.ResourceName]float64{},
		},
		{
			name: "later mappings override earlier ones",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{"CUSTOM_GPU", "CUSTOM_STANDARD"},
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 2.0,
						},
						HasTrait: &standardTrait,
					},
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 4.0, // should override the first
						},
						HasTrait: &gpuTrait,
					},
				},
			},
			expectedOvercommit: map[hv1.ResourceName]float64{
				hv1.ResourceCPU: 4.0,
			},
		},
		{
			name: "no update when overcommit already matches",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{
						hv1.ResourceCPU: 4.0,
					},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{"CUSTOM_GPU"},
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 4.0,
						},
						HasTrait: &gpuTrait,
					},
				},
			},
			expectedOvercommit: map[hv1.ResourceName]float64{
				hv1.ResourceCPU: 4.0,
			},
			expectNoUpdate: true,
		},
		{
			name: "skip mapping without trait specified",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{"CUSTOM_GPU"},
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 2.0,
						},
						// No HasTrait or HasntTrait specified
					},
				},
			},
			expectedOvercommit: map[hv1.ResourceName]float64{},
		},
		{
			name: "combine HasTrait and HasntTrait mappings",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{"CUSTOM_GPU"}, // has GPU, doesn't have STANDARD
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 4.0,
						},
						HasTrait: &gpuTrait,
					},
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceMemory: 1.5,
						},
						HasntTrait: &standardTrait, // STANDARD not present
					},
				},
			},
			expectedOvercommit: map[hv1.ResourceName]float64{
				hv1.ResourceCPU:    4.0,
				hv1.ResourceMemory: 1.5,
			},
		},
		{
			name: "hypervisor not found",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "nonexistent",
				},
			},
			config:              HypervisorOvercommitConfig{},
			expectNotFoundError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fakeClient client.Client
			if tt.expectNotFoundError {
				// Don't add the hypervisor to the fake client
				fakeClient = fake.NewClientBuilder().
					WithScheme(scheme).
					Build()
			} else {
				fakeClient = fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(tt.hypervisor).
					Build()
			}

			controller := &HypervisorOvercommitController{
				Client: fakeClient,
				config: tt.config,
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name: tt.hypervisor.Name,
				},
			}

			ctx := context.Background()
			result, err := controller.Reconcile(ctx, req)

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if result.RequeueAfter > 0 {
				t.Error("expected no requeue")
			}

			if tt.expectNotFoundError {
				// For not found case, we expect no error and no requeue
				return
			}

			// Get the updated hypervisor
			updated := &hv1.Hypervisor{}
			if err := fakeClient.Get(ctx, req.NamespacedName, updated); err != nil {
				t.Fatalf("failed to get updated hypervisor: %v", err)
			}

			// Check overcommit ratios
			if len(updated.Spec.Overcommit) != len(tt.expectedOvercommit) {
				t.Errorf("expected %d overcommit entries, got %d",
					len(tt.expectedOvercommit), len(updated.Spec.Overcommit))
			}

			for resource, expected := range tt.expectedOvercommit {
				actual, ok := updated.Spec.Overcommit[resource]
				if !ok {
					t.Errorf("expected overcommit for resource %s, but not found", resource)
					continue
				}
				if actual != expected {
					t.Errorf("expected overcommit %f for resource %s, got %f",
						expected, resource, actual)
				}
			}
		})
	}
}

func TestHypervisorOvercommitController_ReconcileNotFound(t *testing.T) {
	scheme := newTestHypervisorScheme(t)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	controller := &HypervisorOvercommitController{
		Client: fakeClient,
		config: HypervisorOvercommitConfig{},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: "nonexistent-hypervisor",
		},
	}

	ctx := context.Background()
	result, err := controller.Reconcile(ctx, req)

	if err != nil {
		t.Errorf("expected no error for not found resource, got: %v", err)
	}

	if result.RequeueAfter > 0 {
		t.Error("expected no requeue for not found resource")
	}
}

// mockWorkQueue implements workqueue.TypedRateLimitingInterface for testing
type mockWorkQueue struct {
	workqueue.TypedRateLimitingInterface[reconcile.Request]
	items []reconcile.Request
}

func (m *mockWorkQueue) Add(item reconcile.Request) {
	m.items = append(m.items, item)
}

func TestHypervisorOvercommitController_HandleRemoteHypervisor(t *testing.T) {
	controller := &HypervisorOvercommitController{}
	handler := controller.handleRemoteHypervisor()

	hypervisor := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-hypervisor",
		},
	}

	ctx := context.Background()

	t.Run("CreateFunc", func(t *testing.T) {
		queue := &mockWorkQueue{}
		handler.Create(ctx, event.CreateEvent{Object: hypervisor}, queue)

		if len(queue.items) != 1 {
			t.Errorf("expected 1 item in queue, got %d", len(queue.items))
		}
		if queue.items[0].Name != "test-hypervisor" {
			t.Errorf("expected hypervisor name 'test-hypervisor', got %s", queue.items[0].Name)
		}
	})

	t.Run("UpdateFunc", func(t *testing.T) {
		queue := &mockWorkQueue{}
		handler.Update(ctx, event.UpdateEvent{
			ObjectOld: hypervisor,
			ObjectNew: hypervisor,
		}, queue)

		if len(queue.items) != 1 {
			t.Errorf("expected 1 item in queue, got %d", len(queue.items))
		}
		if queue.items[0].Name != "test-hypervisor" {
			t.Errorf("expected hypervisor name 'test-hypervisor', got %s", queue.items[0].Name)
		}
	})

	t.Run("DeleteFunc", func(t *testing.T) {
		queue := &mockWorkQueue{}
		handler.Delete(ctx, event.DeleteEvent{Object: hypervisor}, queue)

		if len(queue.items) != 1 {
			t.Errorf("expected 1 item in queue, got %d", len(queue.items))
		}
		if queue.items[0].Name != "test-hypervisor" {
			t.Errorf("expected hypervisor name 'test-hypervisor', got %s", queue.items[0].Name)
		}
	})
}

func TestHypervisorOvercommitController_PredicateRemoteHypervisor(t *testing.T) {
	controller := &HypervisorOvercommitController{}
	predicate := controller.predicateRemoteHypervisor()

	t.Run("accepts Hypervisor objects", func(t *testing.T) {
		hypervisor := &hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-hypervisor",
			},
		}

		if !predicate.Generic(event.GenericEvent{Object: hypervisor}) {
			t.Error("expected predicate to accept Hypervisor object")
		}
	})

	t.Run("rejects non-Hypervisor objects", func(t *testing.T) {
		// Create a non-Hypervisor object by using a different type
		// We'll test with a nil object which should return false
		type nonHypervisor struct {
			client.Object
		}

		if predicate.Generic(event.GenericEvent{Object: &nonHypervisor{}}) {
			t.Error("expected predicate to reject non-Hypervisor object")
		}
	})
}

func TestHypervisorOvercommitController_SetupWithManager_InvalidClient(t *testing.T) {
	scheme := newTestHypervisorScheme(t)

	// Create a regular fake client (not a multicluster client)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	controller := &HypervisorOvercommitController{
		Client: fakeClient,
	}

	// Create a minimal mock manager for testing
	mgr := &mockManager{scheme: scheme}

	// SetupWithManager should fail - either because config loading fails
	// (in test environment without config files) or because the client
	// is not a multicluster client.
	err := controller.SetupWithManager(mgr)
	if err == nil {
		t.Error("expected error when calling SetupWithManager, got nil")
	}
	// The error could be either about missing config or about multicluster client
	// depending on the test environment. We just verify an error is returned.
}

// mockManager implements ctrl.Manager for testing SetupWithManager
type mockManager struct {
	ctrl.Manager
	scheme *runtime.Scheme
}

func (m *mockManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

// patchFailingClient wraps a client.Client and returns an error on Patch calls
type patchFailingClient struct {
	client.Client
	patchErr error
}

func (c *patchFailingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	return c.patchErr
}

func TestHypervisorOvercommitController_Reconcile_PatchError(t *testing.T) {
	scheme := newTestHypervisorScheme(t)

	gpuTrait := "CUSTOM_GPU"
	hypervisor := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-hypervisor",
		},
		Spec: hv1.HypervisorSpec{
			Overcommit: map[hv1.ResourceName]float64{},
		},
		Status: hv1.HypervisorStatus{
			Traits: []string{"CUSTOM_GPU"},
		},
	}

	// Create a fake client with the hypervisor, then wrap it to fail on Patch
	baseClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hypervisor).
		Build()

	patchErr := errors.New("patch failed")
	failingClient := &patchFailingClient{
		Client:   baseClient,
		patchErr: patchErr,
	}

	controller := &HypervisorOvercommitController{
		Client: failingClient,
		config: HypervisorOvercommitConfig{
			OvercommitMappings: []HypervisorOvercommitMapping{
				{
					Overcommit: map[hv1.ResourceName]float64{
						hv1.ResourceCPU: 4.0,
					},
					HasTrait: &gpuTrait,
				},
			},
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: hypervisor.Name,
		},
	}

	ctx := context.Background()
	_, err := controller.Reconcile(ctx, req)

	// Reconcile should return an error when Patch fails
	if err == nil {
		t.Error("expected error when Patch fails, got nil")
	}
	if !strings.Contains(err.Error(), "patch failed") {
		t.Errorf("expected error message to contain 'patch failed', got: %v", err)
	}
}

func TestHypervisorOvercommitController_Reconcile_EmptyConfig(t *testing.T) {
	scheme := newTestHypervisorScheme(t)

	hypervisor := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-hypervisor",
		},
		Spec: hv1.HypervisorSpec{
			Overcommit: map[hv1.ResourceName]float64{},
		},
		Status: hv1.HypervisorStatus{
			Traits: []string{"CUSTOM_GPU"},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hypervisor).
		Build()

	controller := &HypervisorOvercommitController{
		Client: fakeClient,
		config: HypervisorOvercommitConfig{
			OvercommitMappings: []HypervisorOvercommitMapping{},
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: hypervisor.Name,
		},
	}

	ctx := context.Background()
	result, err := controller.Reconcile(ctx, req)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if result.RequeueAfter > 0 {
		t.Error("expected no requeue")
	}

	// Verify no changes were made
	updated := &hv1.Hypervisor{}
	if err := fakeClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get updated hypervisor: %v", err)
	}

	if len(updated.Spec.Overcommit) != 0 {
		t.Errorf("expected empty overcommit, got %v", updated.Spec.Overcommit)
	}
}

func TestHypervisorOvercommitController_Reconcile_MultipleResources(t *testing.T) {
	scheme := newTestHypervisorScheme(t)

	gpuTrait := "CUSTOM_GPU"
	hypervisor := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-hypervisor",
		},
		Spec: hv1.HypervisorSpec{
			Overcommit: map[hv1.ResourceName]float64{},
		},
		Status: hv1.HypervisorStatus{
			Traits: []string{"CUSTOM_GPU"},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hypervisor).
		Build()

	controller := &HypervisorOvercommitController{
		Client: fakeClient,
		config: HypervisorOvercommitConfig{
			OvercommitMappings: []HypervisorOvercommitMapping{
				{
					Overcommit: map[hv1.ResourceName]float64{
						hv1.ResourceCPU:    4.0,
						hv1.ResourceMemory: 1.5,
					},
					HasTrait: &gpuTrait,
				},
			},
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: hypervisor.Name,
		},
	}

	ctx := context.Background()
	_, err := controller.Reconcile(ctx, req)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	updated := &hv1.Hypervisor{}
	if err := fakeClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get updated hypervisor: %v", err)
	}

	if len(updated.Spec.Overcommit) != 2 {
		t.Errorf("expected 2 overcommit entries, got %d", len(updated.Spec.Overcommit))
	}

	if updated.Spec.Overcommit[hv1.ResourceCPU] != 4.0 {
		t.Errorf("expected CPU overcommit 4.0, got %f", updated.Spec.Overcommit[hv1.ResourceCPU])
	}

	if updated.Spec.Overcommit[hv1.ResourceMemory] != 1.5 {
		t.Errorf("expected Memory overcommit 1.5, got %f", updated.Spec.Overcommit[hv1.ResourceMemory])
	}
}
