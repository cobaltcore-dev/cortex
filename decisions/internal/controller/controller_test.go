// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/decisions/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcile(t *testing.T) {
	resource := &v1alpha1.SchedulingDecision{
		ObjectMeta: ctrl.ObjectMeta{
			Name: "test-decision",
		},
		Spec: v1alpha1.SchedulingDecisionSpec{
			Input: map[string]float64{
				"host1": 1.0,
				"host2": 2.0,
			},
			Pipeline: v1alpha1.SchedulingDecisionPipelineSpec{
				Name: "test-pipeline",
				Outputs: []v1alpha1.SchedulingDecisionPipelineOutputSpec{
					{
						Step: "weigher",
						Weights: map[string]float64{
							"host1": 0.5,
							"host2": 0.5,
						},
					},
					{
						Step: "filter",
						Weights: map[string]float64{
							"host1": 0.0,
						},
					},
				},
			},
		},
	}

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(resource).
		WithStatusSubresource(&v1alpha1.SchedulingDecision{}).
		Build()

	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Name: "test-decision",
		},
	}

	reconciler := &SchedulingDecisionReconciler{
		Conf:   Config{},
		Client: fakeClient,
	}
	_, err := reconciler.Reconcile(t.Context(), req)
	if err != nil {
		t.Fatalf("Reconcile returned an error: %v", err)
	}

	// Fetch the updated resource to check status
	var updatedResource v1alpha1.SchedulingDecision
	if err := fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-decision"}, &updatedResource); err != nil {
		t.Fatalf("Failed to get updated resource: %v", err)
	}

	// Verify success state
	if updatedResource.Status.State != v1alpha1.SchedulingDecisionStateResolved {
		t.Errorf("Expected state '%s', got '%s'", v1alpha1.SchedulingDecisionStateResolved, updatedResource.Status.State)
	}
	if updatedResource.Status.Error != "" {
		t.Errorf("Expected empty error, got '%s'", updatedResource.Status.Error)
	}
	if updatedResource.Status.Description != "...." {
		t.Errorf("Expected description '....', got '%s'", updatedResource.Status.Description)
	}

	t.Logf("Reconcile completed successfully: state=%s, description=%s", updatedResource.Status.State, updatedResource.Status.Description)
}

func TestReconcileEmptyInput(t *testing.T) {
	resource := &v1alpha1.SchedulingDecision{
		ObjectMeta: ctrl.ObjectMeta{
			Name: "test-decision-empty-input",
		},
		Spec: v1alpha1.SchedulingDecisionSpec{
			Input: map[string]float64{}, // Empty input - no hosts
			Pipeline: v1alpha1.SchedulingDecisionPipelineSpec{
				Name: "test-pipeline",
				Outputs: []v1alpha1.SchedulingDecisionPipelineOutputSpec{
					{
						Step: "weigher",
						Weights: map[string]float64{
							"host1": 0.5,
							"host2": 0.5,
						},
					},
				},
			},
		},
	}

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(resource).
		WithStatusSubresource(&v1alpha1.SchedulingDecision{}).
		Build()

	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Name: "test-decision-empty-input",
		},
	}

	reconciler := &SchedulingDecisionReconciler{
		Conf:   Config{},
		Client: fakeClient,
	}
	_, err := reconciler.Reconcile(t.Context(), req)
	if err != nil {
		t.Fatalf("Reconcile returned an error: %v", err)
	}

	// Fetch the updated resource to check status
	var updatedResource v1alpha1.SchedulingDecision
	if err := fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-decision-empty-input"}, &updatedResource); err != nil {
		t.Fatalf("Failed to get updated resource: %v", err)
	}

	// Verify error state
	if updatedResource.Status.State != v1alpha1.SchedulingDecisionStateError {
		t.Errorf("Expected state '%s', got '%s'", v1alpha1.SchedulingDecisionStateError, updatedResource.Status.State)
	}
	expectedError := "No hosts provided in input"
	if updatedResource.Status.Error != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, updatedResource.Status.Error)
	}
	if updatedResource.Status.Description != "" {
		t.Errorf("Expected empty description, got '%s'", updatedResource.Status.Description)
	}

	t.Logf("Reconcile completed with error: state=%s, error=%s", updatedResource.Status.State, updatedResource.Status.Error)
}

func TestReconcileHostMismatch(t *testing.T) {
	resource := &v1alpha1.SchedulingDecision{
		ObjectMeta: ctrl.ObjectMeta{
			Name: "test-decision-host-mismatch",
		},
		Spec: v1alpha1.SchedulingDecisionSpec{
			Input: map[string]float64{
				"host1": 1.0,
				"host2": 2.0,
			}, // host3 is missing but referenced in pipeline output
			Pipeline: v1alpha1.SchedulingDecisionPipelineSpec{
				Name: "test-pipeline",
				Outputs: []v1alpha1.SchedulingDecisionPipelineOutputSpec{
					{
						Step: "weigher",
						Weights: map[string]float64{
							"host1": 0.5,
							"host3": 0.3, // host3 doesn't exist in input
						},
					},
				},
			},
		},
	}

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(resource).
		WithStatusSubresource(&v1alpha1.SchedulingDecision{}).
		Build()

	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Name: "test-decision-host-mismatch",
		},
	}

	reconciler := &SchedulingDecisionReconciler{
		Conf:   Config{},
		Client: fakeClient,
	}
	_, err := reconciler.Reconcile(t.Context(), req)
	if err != nil {
		t.Fatalf("Reconcile returned an error: %v", err)
	}

	// Fetch the updated resource to check status
	var updatedResource v1alpha1.SchedulingDecision
	if err := fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-decision-host-mismatch"}, &updatedResource); err != nil {
		t.Fatalf("Failed to get updated resource: %v", err)
	}

	// Verify error state for host mismatch
	if updatedResource.Status.State != v1alpha1.SchedulingDecisionStateError {
		t.Errorf("Expected state '%s', got '%s'", v1alpha1.SchedulingDecisionStateError, updatedResource.Status.State)
	}
	expectedError := "Host 'host3' in pipeline output not found in input"
	if updatedResource.Status.Error != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, updatedResource.Status.Error)
	}
	if updatedResource.Status.Description != "" {
		t.Errorf("Expected empty description, got '%s'", updatedResource.Status.Description)
	}

	t.Logf("Reconcile completed with host mismatch error: state=%s, error=%s", updatedResource.Status.State, updatedResource.Status.Error)
}
