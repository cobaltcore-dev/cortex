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
						Activations: map[string]float64{
							"host1": 0.5,
							"host2": 0.5,
						},
					},
					{
						Step: "filter",
						Activations: map[string]float64{
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
	if updatedResource.Status.Description != "Calculated final scores for hosts" {
		t.Errorf("Expected description 'Calculated final scores for hosts', got '%s'", updatedResource.Status.Description)
	}

	// Verify final scores calculation
	// Expected: host1: 1.0 + 0.5 + 0.0 = 1.5, host2: removed by filter step
	expectedFinalScores := map[string]float64{
		"host1": 1.5,
	}
	if len(updatedResource.Status.FinalScores) != len(expectedFinalScores) {
		t.Errorf("Expected %d final scores, got %d", len(expectedFinalScores), len(updatedResource.Status.FinalScores))
	}
	for host, expectedScore := range expectedFinalScores {
		if actualScore, exists := updatedResource.Status.FinalScores[host]; !exists {
			t.Errorf("Expected final score for host '%s', but it was not found", host)
		} else if actualScore != expectedScore {
			t.Errorf("Expected final score for host '%s' to be %f, got %f", host, expectedScore, actualScore)
		}
	}

	// Verify deleted hosts tracking
	expectedDeletedHosts := map[string][]string{
		"host2": {"filter"}, // host2 was deleted by the filter step
	}
	if len(updatedResource.Status.DeletedHosts) != len(expectedDeletedHosts) {
		t.Errorf("Expected %d deleted hosts, got %d", len(expectedDeletedHosts), len(updatedResource.Status.DeletedHosts))
	}
	for host, expectedSteps := range expectedDeletedHosts {
		if actualSteps, exists := updatedResource.Status.DeletedHosts[host]; !exists {
			t.Errorf("Expected deleted host '%s', but it was not found", host)
		} else if len(actualSteps) != len(expectedSteps) {
			t.Errorf("Expected host '%s' to be deleted by %d steps, got %d", host, len(expectedSteps), len(actualSteps))
		} else {
			for i, expectedStep := range expectedSteps {
				if actualSteps[i] != expectedStep {
					t.Errorf("Expected host '%s' step %d to be '%s', got '%s'", host, i, expectedStep, actualSteps[i])
				}
			}
		}
	}

	t.Logf("Reconcile completed successfully: state=%s, finalScores=%v, deletedHosts=%v",
		updatedResource.Status.State, updatedResource.Status.FinalScores, updatedResource.Status.DeletedHosts)
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
						Activations: map[string]float64{
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
						Activations: map[string]float64{
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

func TestReconcileComplexScoring(t *testing.T) {
	resource := &v1alpha1.SchedulingDecision{
		ObjectMeta: ctrl.ObjectMeta{
			Name: "test-decision-complex",
		},
		Spec: v1alpha1.SchedulingDecisionSpec{
			Input: map[string]float64{
				"host1": 1.0,
				"host2": 2.0,
				"host3": 3.0,
				"host4": 4.0,
			},
			Pipeline: v1alpha1.SchedulingDecisionPipelineSpec{
				Name: "complex-pipeline",
				Outputs: []v1alpha1.SchedulingDecisionPipelineOutputSpec{
					{
						Step: "weigher1",
						Activations: map[string]float64{
							"host1": 0.5,
							"host2": 1.0,
							"host3": -0.5,
							"host4": 2.0,
						},
					},
					{
						Step: "filter1",
						Activations: map[string]float64{
							"host1": 0.2,
							"host3": 0.1, // host2 and host4 removed by this step
						},
					},
					{
						Step: "weigher2",
						Activations: map[string]float64{
							"host1": -0.3, // host3 removed by this step
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
			Name: "test-decision-complex",
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
	if err := fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-decision-complex"}, &updatedResource); err != nil {
		t.Fatalf("Failed to get updated resource: %v", err)
	}

	// Verify success state
	if updatedResource.Status.State != v1alpha1.SchedulingDecisionStateResolved {
		t.Errorf("Expected state '%s', got '%s'", v1alpha1.SchedulingDecisionStateResolved, updatedResource.Status.State)
	}

	// Verify final scores calculation
	// Expected: host1: 1.0 + 0.5 + 0.2 + (-0.3) = 1.4
	// host2: removed by filter1, host3: removed by weigher2, host4: removed by filter1
	expectedFinalScores := map[string]float64{
		"host1": 1.4,
	}
	if len(updatedResource.Status.FinalScores) != len(expectedFinalScores) {
		t.Errorf("Expected %d final scores, got %d", len(expectedFinalScores), len(updatedResource.Status.FinalScores))
	}
	for host, expectedScore := range expectedFinalScores {
		if actualScore, exists := updatedResource.Status.FinalScores[host]; !exists {
			t.Errorf("Expected final score for host '%s', but it was not found", host)
		} else if actualScore != expectedScore {
			t.Errorf("Expected final score for host '%s' to be %f, got %f", host, expectedScore, actualScore)
		}
	}

	// Verify deleted hosts tracking
	expectedDeletedHosts := map[string][]string{
		"host2": {"filter1"},  // host2 deleted by filter1
		"host4": {"filter1"},  // host4 deleted by filter1
		"host3": {"weigher2"}, // host3 deleted by weigher2
	}
	if len(updatedResource.Status.DeletedHosts) != len(expectedDeletedHosts) {
		t.Errorf("Expected %d deleted hosts, got %d", len(expectedDeletedHosts), len(updatedResource.Status.DeletedHosts))
	}
	for host, expectedSteps := range expectedDeletedHosts {
		if actualSteps, exists := updatedResource.Status.DeletedHosts[host]; !exists {
			t.Errorf("Expected deleted host '%s', but it was not found", host)
		} else if len(actualSteps) != len(expectedSteps) {
			t.Errorf("Expected host '%s' to be deleted by %d steps, got %d", host, len(expectedSteps), len(actualSteps))
		} else {
			for i, expectedStep := range expectedSteps {
				if actualSteps[i] != expectedStep {
					t.Errorf("Expected host '%s' step %d to be '%s', got '%s'", host, i, expectedStep, actualSteps[i])
				}
			}
		}
	}

	t.Logf("Complex scoring completed: finalScores=%v, deletedHosts=%v",
		updatedResource.Status.FinalScores, updatedResource.Status.DeletedHosts)
}

func TestReconcileMultipleDeletionSteps(t *testing.T) {
	resource := &v1alpha1.SchedulingDecision{
		ObjectMeta: ctrl.ObjectMeta{
			Name: "test-decision-multiple-deletions",
		},
		Spec: v1alpha1.SchedulingDecisionSpec{
			Input: map[string]float64{
				"host1": 1.0,
				"host2": 2.0,
				"host3": 3.0,
			},
			Pipeline: v1alpha1.SchedulingDecisionPipelineSpec{
				Name: "multiple-deletion-pipeline",
				Outputs: []v1alpha1.SchedulingDecisionPipelineOutputSpec{
					{
						Step: "weigher1",
						Activations: map[string]float64{
							"host1": 0.5,
							"host2": 1.0,
							"host3": -0.5,
						},
					},
					{
						Step: "filter1",
						Activations: map[string]float64{
							"host1": 0.2,
							// host2 and host3 removed by this step
						},
					},
					{
						Step:        "filter2",
						Activations: map[string]float64{
							// host1 removed by this step
							// host2 and host3 would be removed again, but they're already gone
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
			Name: "test-decision-multiple-deletions",
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
	if err := fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-decision-multiple-deletions"}, &updatedResource); err != nil {
		t.Fatalf("Failed to get updated resource: %v", err)
	}

	// Verify success state
	if updatedResource.Status.State != v1alpha1.SchedulingDecisionStateResolved {
		t.Errorf("Expected state '%s', got '%s'", v1alpha1.SchedulingDecisionStateResolved, updatedResource.Status.State)
	}

	// Verify final scores calculation
	// Expected: All hosts should be removed, no final scores
	expectedFinalScores := map[string]float64{}
	if len(updatedResource.Status.FinalScores) != len(expectedFinalScores) {
		t.Errorf("Expected %d final scores, got %d", len(expectedFinalScores), len(updatedResource.Status.FinalScores))
	}

	// Verify deleted hosts tracking
	// host2 and host3 deleted by filter1, host1 deleted by filter2
	expectedDeletedHosts := map[string][]string{
		"host2": {"filter1"}, // host2 deleted by filter1
		"host3": {"filter1"}, // host3 deleted by filter1
		"host1": {"filter2"}, // host1 deleted by filter2
	}
	if len(updatedResource.Status.DeletedHosts) != len(expectedDeletedHosts) {
		t.Errorf("Expected %d deleted hosts, got %d", len(expectedDeletedHosts), len(updatedResource.Status.DeletedHosts))
	}
	for host, expectedSteps := range expectedDeletedHosts {
		if actualSteps, exists := updatedResource.Status.DeletedHosts[host]; !exists {
			t.Errorf("Expected deleted host '%s', but it was not found", host)
		} else if len(actualSteps) != len(expectedSteps) {
			t.Errorf("Expected host '%s' to be deleted by %d steps, got %d", host, len(expectedSteps), len(actualSteps))
		} else {
			for i, expectedStep := range expectedSteps {
				if actualSteps[i] != expectedStep {
					t.Errorf("Expected host '%s' step %d to be '%s', got '%s'", host, i, expectedStep, actualSteps[i])
				}
			}
		}
	}

	t.Logf("Multiple deletion test completed: finalScores=%v, deletedHosts=%v",
		updatedResource.Status.FinalScores, updatedResource.Status.DeletedHosts)
}
