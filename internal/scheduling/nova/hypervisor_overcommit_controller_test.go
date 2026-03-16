// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"reflect"
	"strings"
	"testing"

	testlib "github.com/cobaltcore-dev/cortex/pkg/testing"
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

func newTestHypervisorScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hv1 to scheme: %v", err)
	}
	return scheme
}

func TestHypervisorOvercommitMapping_Validate(t *testing.T) {
	tests := []struct {
		name        string
		mapping     HypervisorOvercommitMapping
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid mapping with single resource",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{
					hv1.ResourceCPU: 2.0,
				},
			},
			expectError: false,
		},
		{
			name: "valid mapping with multiple resources",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{
					hv1.ResourceCPU:    2.0,
					hv1.ResourceMemory: 1.5,
				},
			},
			expectError: false,
		},
		{
			name: "valid mapping with exact 1.0 ratio",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{
					hv1.ResourceCPU: 1.0,
				},
			},
			expectError: false,
		},
		{
			name: "valid mapping with trait",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{
					hv1.ResourceCPU: 2.0,
				},
				Trait: testlib.Ptr("high-memory"),
			},
			expectError: false,
		},
		{
			name: "invalid mapping with ratio less than 1.0",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{
					hv1.ResourceCPU: 0.5,
				},
			},
			expectError: true,
			errorMsg:    "Invalid overcommit ratio in config, must be >= 1.0",
		},
		{
			name: "invalid mapping with zero ratio",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{
					hv1.ResourceCPU: 0.0,
				},
			},
			expectError: true,
			errorMsg:    "Invalid overcommit ratio in config, must be >= 1.0",
		},
		{
			name: "invalid mapping with negative ratio",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{
					hv1.ResourceCPU: -1.0,
				},
			},
			expectError: true,
			errorMsg:    "Invalid overcommit ratio in config, must be >= 1.0",
		},
		{
			name: "valid mapping with empty overcommit",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{},
			},
			expectError: false,
		},
		{
			name:        "valid mapping with nil overcommit",
			mapping:     HypervisorOvercommitMapping{},
			expectError: false,
		},
		{
			name: "mixed valid and invalid ratios",
			mapping: HypervisorOvercommitMapping{
				Overcommit: map[hv1.ResourceName]float64{
					hv1.ResourceCPU:    2.0,
					hv1.ResourceMemory: 0.5, // invalid
				},
			},
			expectError: true,
			errorMsg:    "Invalid overcommit ratio in config, must be >= 1.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mapping.Validate()

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if tt.expectError && err != nil && tt.errorMsg != "" {
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error to contain %q, got: %v", tt.errorMsg, err)
				}
			}
		})
	}
}

func TestHypervisorOvercommitConfig_Validate(t *testing.T) {
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
						Trait: testlib.Ptr("high-cpu"),
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
						Trait: testlib.Ptr("high-cpu"),
					},
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceMemory: 1.5,
						},
						Trait: testlib.Ptr("high-memory"),
					},
				},
			},
			expectError: false,
		},
		{
			name: "valid config with empty mappings",
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{},
			},
			expectError: false,
		},
		{
			name:        "valid config with nil mappings",
			config:      HypervisorOvercommitConfig{},
			expectError: false,
		},
		{
			name: "invalid config with one bad mapping",
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 2.0,
						},
						Trait: testlib.Ptr("high-cpu"),
					},
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 0.5, // invalid
						},
						Trait: testlib.Ptr("low-cpu"),
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

func TestHypervisorOvercommitController_Reconcile(t *testing.T) {
	scheme := newTestHypervisorScheme(t)

	tests := []struct {
		name                     string
		hypervisor               *hv1.Hypervisor
		config                   HypervisorOvercommitConfig
		expectError              bool
		expectUpdate             bool
		expectedOvercommitValues map[hv1.ResourceName]float64
	}{
		{
			name: "hypervisor with matching trait gets overcommit updated",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{"high-cpu", "standard"},
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 2.0,
						},
						Trait: testlib.Ptr("high-cpu"),
					},
				},
			},
			expectError:  false,
			expectUpdate: true,
			expectedOvercommitValues: map[hv1.ResourceName]float64{
				hv1.ResourceCPU: 2.0,
			},
		},
		{
			name: "hypervisor without matching trait keeps original overcommit",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{
						hv1.ResourceCPU: 1.5,
					},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{"standard", "other"},
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 2.0,
						},
						Trait: testlib.Ptr("high-cpu"),
					},
				},
			},
			expectError:              false,
			expectUpdate:             true, // Update to empty overcommit since no traits match
			expectedOvercommitValues: nil,
		},
		{
			name: "hypervisor already has desired overcommit - no update needed",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{
						hv1.ResourceCPU: 2.0,
					},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{"high-cpu"},
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 2.0,
						},
						Trait: testlib.Ptr("high-cpu"),
					},
				},
			},
			expectError:  false,
			expectUpdate: false,
			expectedOvercommitValues: map[hv1.ResourceName]float64{
				hv1.ResourceCPU: 2.0,
			},
		},
		{
			name: "only second trait from config matches hypervisor traits",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{"trait-b", "other"}, // Only trait-b matches config
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 2.0,
						},
						Trait: testlib.Ptr("trait-a"),
					},
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 3.0,
						},
						Trait: testlib.Ptr("trait-b"),
					},
				},
			},
			expectError:  false,
			expectUpdate: true,
			expectedOvercommitValues: map[hv1.ResourceName]float64{
				hv1.ResourceCPU: 3.0, // Only trait-b matches
			},
		},
		{
			name: "multiple trait mappings with different resources",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{"cpu-trait", "memory-trait"},
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 2.0,
						},
						Trait: testlib.Ptr("cpu-trait"),
					},
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceMemory: 1.5,
						},
						Trait: testlib.Ptr("memory-trait"),
					},
				},
			},
			expectError:  false,
			expectUpdate: true,
			expectedOvercommitValues: map[hv1.ResourceName]float64{
				hv1.ResourceCPU:    2.0,
				hv1.ResourceMemory: 1.5,
			},
		},
		{
			name: "mapping without trait is ignored",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{"any-trait"},
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 2.0,
						},
						// Trait is nil - this mapping should be ignored
					},
				},
			},
			expectError:              false,
			expectUpdate:             false, // No update since mapping has no trait
			expectedOvercommitValues: nil,
		},
		{
			name: "hypervisor with no traits gets empty overcommit",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{
						hv1.ResourceCPU: 2.0,
					},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{}, // No traits
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{
					{
						Overcommit: map[hv1.ResourceName]float64{
							hv1.ResourceCPU: 2.0,
						},
						Trait: testlib.Ptr("high-cpu"),
					},
				},
			},
			expectError:              false,
			expectUpdate:             true,
			expectedOvercommitValues: nil,
		},
		{
			name: "empty config results in empty overcommit",
			hypervisor: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
				Spec: hv1.HypervisorSpec{
					Overcommit: map[hv1.ResourceName]float64{
						hv1.ResourceCPU: 2.0,
					},
				},
				Status: hv1.HypervisorStatus{
					Traits: []string{"any-trait"},
				},
			},
			config: HypervisorOvercommitConfig{
				OvercommitMappings: []HypervisorOvercommitMapping{},
			},
			expectError:              false,
			expectUpdate:             true,
			expectedOvercommitValues: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{}
			if tt.hypervisor != nil {
				objects = append(objects, tt.hypervisor)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			controller := &HypervisorOvercommitController{
				Client: fakeClient,
				config: tt.config,
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name: tt.hypervisor.Name,
				},
			}

			result, err := controller.Reconcile(context.Background(), req)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			// Result should not requeue
			if result.RequeueAfter > 0 {
				t.Error("Expected no requeue")
			}

			// Check the updated hypervisor
			var updatedHypervisor hv1.Hypervisor
			if err := fakeClient.Get(context.Background(), req.NamespacedName, &updatedHypervisor); err != nil {
				t.Fatalf("Failed to get updated hypervisor: %v", err)
			}

			if tt.expectedOvercommitValues != nil {
				if !reflect.DeepEqual(updatedHypervisor.Spec.Overcommit, tt.expectedOvercommitValues) {
					t.Errorf("Expected overcommit values %v, got %v",
						tt.expectedOvercommitValues, updatedHypervisor.Spec.Overcommit)
				}
			}
		})
	}
}

func TestHypervisorOvercommitController_Reconcile_HypervisorNotFound(t *testing.T) {
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

	result, err := controller.Reconcile(context.Background(), req)

	// Should not return an error for not found
	if err != nil {
		t.Errorf("Expected no error for not found hypervisor, got: %v", err)
	}

	// Should not requeue
	if result.RequeueAfter > 0 {
		t.Error("Expected no requeue for not found hypervisor")
	}
}

func TestHypervisorOvercommitController_handleRemoteHypervisor(t *testing.T) {
	controller := &HypervisorOvercommitController{}
	handler := controller.handleRemoteHypervisor()

	// Test CreateFunc
	t.Run("CreateFunc adds request to queue", func(t *testing.T) {
		hypervisor := &hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-hypervisor-create",
			},
		}

		queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
		defer queue.ShutDown()

		createEvt := event.CreateEvent{
			Object: hypervisor,
		}

		handler.Create(context.Background(), createEvt, queue)

		if queue.Len() != 1 {
			t.Errorf("Expected 1 item in queue, got %d", queue.Len())
		}

		item, _ := queue.Get()
		if item.Name != "test-hypervisor-create" {
			t.Errorf("Expected request name 'test-hypervisor-create', got '%s'", item.Name)
		}
		queue.Done(item)
	})

	// Test UpdateFunc
	t.Run("UpdateFunc adds request to queue", func(t *testing.T) {
		oldHypervisor := &hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-hypervisor-update",
			},
		}
		newHypervisor := &hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-hypervisor-update",
			},
		}

		queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
		defer queue.ShutDown()

		updateEvt := event.UpdateEvent{
			ObjectOld: oldHypervisor,
			ObjectNew: newHypervisor,
		}

		handler.Update(context.Background(), updateEvt, queue)

		if queue.Len() != 1 {
			t.Errorf("Expected 1 item in queue, got %d", queue.Len())
		}

		item, _ := queue.Get()
		if item.Name != "test-hypervisor-update" {
			t.Errorf("Expected request name 'test-hypervisor-update', got '%s'", item.Name)
		}
		queue.Done(item)
	})

	// Test DeleteFunc
	t.Run("DeleteFunc adds request to queue", func(t *testing.T) {
		hypervisor := &hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-hypervisor-delete",
			},
		}

		queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
		defer queue.ShutDown()

		deleteEvt := event.DeleteEvent{
			Object: hypervisor,
		}

		handler.Delete(context.Background(), deleteEvt, queue)

		if queue.Len() != 1 {
			t.Errorf("Expected 1 item in queue, got %d", queue.Len())
		}

		item, _ := queue.Get()
		if item.Name != "test-hypervisor-delete" {
			t.Errorf("Expected request name 'test-hypervisor-delete', got '%s'", item.Name)
		}
		queue.Done(item)
	})
}

func TestHypervisorOvercommitController_predicateRemoteHypervisor(t *testing.T) {
	controller := &HypervisorOvercommitController{}
	predicate := controller.predicateRemoteHypervisor()

	tests := []struct {
		name           string
		object         client.Object
		expectedResult bool
	}{
		{
			name: "Hypervisor object returns true",
			object: &hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hypervisor",
				},
			},
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test Create predicate
			createEvt := event.CreateEvent{Object: tt.object}
			if result := predicate.Create(createEvt); result != tt.expectedResult {
				t.Errorf("Create predicate: expected %v, got %v", tt.expectedResult, result)
			}

			// Test Update predicate
			updateEvt := event.UpdateEvent{ObjectOld: tt.object, ObjectNew: tt.object}
			if result := predicate.Update(updateEvt); result != tt.expectedResult {
				t.Errorf("Update predicate: expected %v, got %v", tt.expectedResult, result)
			}

			// Test Delete predicate
			deleteEvt := event.DeleteEvent{Object: tt.object}
			if result := predicate.Delete(deleteEvt); result != tt.expectedResult {
				t.Errorf("Delete predicate: expected %v, got %v", tt.expectedResult, result)
			}

			// Test Generic predicate
			genericEvt := event.GenericEvent{Object: tt.object}
			if result := predicate.Generic(genericEvt); result != tt.expectedResult {
				t.Errorf("Generic predicate: expected %v, got %v", tt.expectedResult, result)
			}
		})
	}
}
