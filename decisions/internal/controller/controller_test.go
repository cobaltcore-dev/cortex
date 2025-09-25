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
	expectedDescription := "Selected: host1 (score: 1.50), certainty: perfect, 2 hosts evaluated. Input favored host2 (score: 2.00, now filtered), final winner was #2 in input (1.00→1.50). Decision driven by 1/2 pipeline step: filter. Step impacts:\n• weigher +0.50\n• filter +0.00→#1."
	if updatedResource.Status.Description != expectedDescription {
		t.Errorf("Expected description '%s', got '%s'", expectedDescription, updatedResource.Status.Description)
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

func TestReconcileCertaintyLevels(t *testing.T) {
	tests := []struct {
		name              string
		input             map[string]float64
		activations       map[string]float64
		expectedWinner    string
		expectedCertainty string
	}{
		{
			name: "high-certainty",
			input: map[string]float64{
				"host1": 1.0,
				"host2": 1.0,
			},
			activations: map[string]float64{
				"host1": 1.0, // host1: 2.0, host2: 1.0, gap = 1.0 (high)
				"host2": 0.0,
			},
			expectedWinner:    "host1",
			expectedCertainty: "high",
		},
		{
			name: "medium-certainty",
			input: map[string]float64{
				"host1": 1.0,
				"host2": 1.0,
			},
			activations: map[string]float64{
				"host1": 0.3, // host1: 1.3, host2: 1.0, gap = 0.3 (medium)
				"host2": 0.0,
			},
			expectedWinner:    "host1",
			expectedCertainty: "medium",
		},
		{
			name: "low-certainty",
			input: map[string]float64{
				"host1": 1.0,
				"host2": 1.0,
			},
			activations: map[string]float64{
				"host1": 0.1, // host1: 1.1, host2: 1.0, gap = 0.1 (low)
				"host2": 0.0,
			},
			expectedWinner:    "host1",
			expectedCertainty: "low",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := &v1alpha1.SchedulingDecision{
				ObjectMeta: ctrl.ObjectMeta{
					Name: "test-certainty-" + tt.name,
				},
				Spec: v1alpha1.SchedulingDecisionSpec{
					Input: tt.input,
					Pipeline: v1alpha1.SchedulingDecisionPipelineSpec{
						Name: "certainty-test-pipeline",
						Outputs: []v1alpha1.SchedulingDecisionPipelineOutputSpec{
							{
								Step:        "weigher",
								Activations: tt.activations,
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
					Name: "test-certainty-" + tt.name,
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
			if err := fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-certainty-" + tt.name}, &updatedResource); err != nil {
				t.Fatalf("Failed to get updated resource: %v", err)
			}

			// Verify the description contains the expected winner and certainty
			description := updatedResource.Status.Description
			if !contains(description, "Selected: "+tt.expectedWinner) {
				t.Errorf("Expected description to contain 'Selected: %s', got '%s'", tt.expectedWinner, description)
			}
			if !contains(description, "certainty: "+tt.expectedCertainty) {
				t.Errorf("Expected description to contain 'certainty: %s', got '%s'", tt.expectedCertainty, description)
			}

			t.Logf("Certainty test %s completed: %s", tt.name, description)
		})
	}
}

func TestReconcileNoHostsRemaining(t *testing.T) {
	resource := &v1alpha1.SchedulingDecision{
		ObjectMeta: ctrl.ObjectMeta{
			Name: "test-no-hosts-remaining",
		},
		Spec: v1alpha1.SchedulingDecisionSpec{
			Input: map[string]float64{
				"host1": 1.0,
				"host2": 2.0,
			},
			Pipeline: v1alpha1.SchedulingDecisionPipelineSpec{
				Name: "filter-all-pipeline",
				Outputs: []v1alpha1.SchedulingDecisionPipelineOutputSpec{
					{
						Step:        "filter-all",
						Activations: map[string]float64{
							// No hosts in activations - all will be filtered out
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
			Name: "test-no-hosts-remaining",
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
	if err := fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-no-hosts-remaining"}, &updatedResource); err != nil {
		t.Fatalf("Failed to get updated resource: %v", err)
	}

	// Verify success state but no final scores
	if updatedResource.Status.State != v1alpha1.SchedulingDecisionStateResolved {
		t.Errorf("Expected state '%s', got '%s'", v1alpha1.SchedulingDecisionStateResolved, updatedResource.Status.State)
	}

	if len(updatedResource.Status.FinalScores) != 0 {
		t.Errorf("Expected 0 final scores, got %d", len(updatedResource.Status.FinalScores))
	}

	expectedDescription := "No hosts remaining after filtering, 2 hosts evaluated"
	if updatedResource.Status.Description != expectedDescription {
		t.Errorf("Expected description '%s', got '%s'", expectedDescription, updatedResource.Status.Description)
	}

	t.Logf("No hosts remaining test completed: %s", updatedResource.Status.Description)
}

func TestReconcileInputVsFinalComparison(t *testing.T) {
	tests := []struct {
		name                 string
		input                map[string]float64
		activations          []map[string]float64
		expectedDescContains []string
	}{
		{
			name: "input-choice-confirmed",
			input: map[string]float64{
				"host1": 3.0, // highest in input
				"host2": 2.0,
				"host3": 1.0,
			},
			activations: []map[string]float64{
				{"host1": 0.5, "host2": 0.3, "host3": 0.1}, // host1 stays winner
			},
			expectedDescContains: []string{
				"Selected: host1",
				"Input choice confirmed: host1 (3.00→3.50, remained #1)",
			},
		},
		{
			name: "input-winner-filtered",
			input: map[string]float64{
				"host1": 1.0,
				"host2": 3.0, // highest in input
				"host3": 2.0,
			},
			activations: []map[string]float64{
				{"host1": 0.5, "host3": 0.3}, // host2 filtered out, host3 becomes winner
			},
			expectedDescContains: []string{
				"Selected: host3",
				"Input favored host2 (score: 3.00, now filtered)",
				"final winner was #2 in input (2.00→2.30)",
			},
		},
		{
			name: "input-winner-demoted",
			input: map[string]float64{
				"host1": 1.0,
				"host2": 3.0, // highest in input
				"host3": 2.0,
			},
			activations: []map[string]float64{
				{"host1": 2.5, "host2": -0.5, "host3": 0.8}, // host1 becomes winner, host2 demoted to #3
			},
			expectedDescContains: []string{
				"Selected: host1",
				"Input favored host2 (score: 3.00, now #3 with 2.50)",
				"final winner was #3 in input (1.00→3.50)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := &v1alpha1.SchedulingDecision{
				ObjectMeta: ctrl.ObjectMeta{
					Name: "test-input-vs-final-" + tt.name,
				},
				Spec: v1alpha1.SchedulingDecisionSpec{
					Input: tt.input,
					Pipeline: v1alpha1.SchedulingDecisionPipelineSpec{
						Name: "input-vs-final-pipeline",
						Outputs: []v1alpha1.SchedulingDecisionPipelineOutputSpec{
							{
								Step:        "weigher",
								Activations: tt.activations[0],
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
					Name: "test-input-vs-final-" + tt.name,
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
			if err := fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-input-vs-final-" + tt.name}, &updatedResource); err != nil {
				t.Fatalf("Failed to get updated resource: %v", err)
			}

			// Verify the description contains expected elements
			description := updatedResource.Status.Description
			for _, expectedContent := range tt.expectedDescContains {
				if !contains(description, expectedContent) {
					t.Errorf("Expected description to contain '%s', got '%s'", expectedContent, description)
				}
			}

			t.Logf("Input vs Final test %s completed: %s", tt.name, description)
		})
	}
}

func TestReconcileCriticalStepElimination(t *testing.T) {
	tests := []struct {
		name                    string
		input                   map[string]float64
		pipeline                []v1alpha1.SchedulingDecisionPipelineOutputSpec
		expectedCriticalMessage string
	}{
		{
			name: "single-critical-step",
			input: map[string]float64{
				"host1": 2.0, // Would win without pipeline
				"host2": 1.0,
				"host3": 1.5,
			},
			pipeline: []v1alpha1.SchedulingDecisionPipelineOutputSpec{
				{
					Step: "non-critical-weigher",
					Activations: map[string]float64{
						"host1": 0.1, // Small changes don't affect winner
						"host2": 0.1,
						"host3": 0.1,
					},
				},
				{
					Step: "critical-filter",
					Activations: map[string]float64{
						"host2": 0.0, // host1 and host3 filtered out, host2 becomes winner
						"host3": 0.0,
					},
				},
			},
			expectedCriticalMessage: "Decision driven by 1/2 pipeline step: critical-filter.",
		},
		{
			name: "multiple-critical-steps",
			input: map[string]float64{
				"host1": 1.0,
				"host2": 3.0, // Strong initial winner
				"host3": 2.0,
			},
			pipeline: []v1alpha1.SchedulingDecisionPipelineOutputSpec{
				{
					Step: "critical-weigher1",
					Activations: map[string]float64{
						"host1": 1.0, // host1: 2.0, host2: 2.5, host3: 2.5 (ties host2 and host3)
						"host2": -0.5,
						"host3": 0.5,
					},
				},
				{
					Step: "critical-weigher2",
					Activations: map[string]float64{
						"host1": 1.0, // host1: 3.0, host2: 2.5, host3: 2.5 (host1 becomes winner)
						"host2": 0.0,
						"host3": 0.0,
					},
				},
			},
			expectedCriticalMessage: "Decision requires all 2 pipeline steps.",
		},
		{
			name: "all-non-critical",
			input: map[string]float64{
				"host1": 3.0, // Clear winner from input
				"host2": 1.0,
				"host3": 2.0,
			},
			pipeline: []v1alpha1.SchedulingDecisionPipelineOutputSpec{
				{
					Step: "non-critical-weigher1",
					Activations: map[string]float64{
						"host1": 0.1, // Small changes don't change winner
						"host2": 0.1,
						"host3": 0.1,
					},
				},
				{
					Step: "non-critical-weigher2",
					Activations: map[string]float64{
						"host1": 0.2,
						"host2": 0.0,
						"host3": 0.1,
					},
				},
			},
			expectedCriticalMessage: "Decision driven by input only (all 2 steps are non-critical).",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := &v1alpha1.SchedulingDecision{
				ObjectMeta: ctrl.ObjectMeta{
					Name: "test-critical-steps-" + tt.name,
				},
				Spec: v1alpha1.SchedulingDecisionSpec{
					Input: tt.input,
					Pipeline: v1alpha1.SchedulingDecisionPipelineSpec{
						Name:    "critical-step-test-pipeline",
						Outputs: tt.pipeline,
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
					Name: "test-critical-steps-" + tt.name,
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
			if err := fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-critical-steps-" + tt.name}, &updatedResource); err != nil {
				t.Fatalf("Failed to get updated resource: %v", err)
			}

			// Verify the description contains the expected critical step message
			description := updatedResource.Status.Description
			if !contains(description, tt.expectedCriticalMessage) {
				t.Errorf("Expected description to contain '%s', got '%s'", tt.expectedCriticalMessage, description)
			}

			t.Logf("Critical step test %s completed: %s", tt.name, description)
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
