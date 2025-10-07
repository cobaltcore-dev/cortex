// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"fmt"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/decisions/api/v1alpha1"
)

func TestReconcile(t *testing.T) {
	// Create test decision with pipeline outputs
	decision := NewTestDecision("decision-1").
		WithInput(map[string]float64{
			"host1": 1.0,
			"host2": 2.0,
		}).
		WithPipelineOutputs(
			NewTestPipelineOutput("weigher", map[string]float64{
				"host1": 0.5,
				"host2": 0.5,
			}),
			NewTestPipelineOutput("filter", map[string]float64{
				"host1": 0.0,
			}),
		).
		Build()

	resource := NewTestSchedulingDecision("test-decision").
		WithDecisions(decision).
		Build()

	fakeClient, _ := SetupTestEnvironment(t, resource)
	req := CreateTestRequest("test-decision")

	reconciler := CreateSchedulingReconciler(fakeClient)
	_, err := reconciler.Reconcile(t.Context(), req)
	if err != nil {
		t.Fatalf("Reconcile returned an error: %v", err)
	}

	// Fetch and verify the updated resource
	updatedResource := AssertResourceExists(t, fakeClient, "test-decision")
	AssertResourceState(t, updatedResource, v1alpha1.SchedulingDecisionStateResolved)
	AssertNoError(t, updatedResource)
	AssertDecisionCount(t, updatedResource, 1)
	AssertResultCount(t, updatedResource, 1)

	result := updatedResource.Status.Results[0]
	if result.ID != "decision-1" {
		t.Errorf("Expected result ID 'decision-1', got '%s'", result.ID)
	}

	expectedDescription := "Selected: host1 (score: 1.50), certainty: perfect, 2 hosts evaluated. Input favored host2 (score: 2.00, now filtered), final winner was #2 in input (1.00→1.50). Decision driven by 1/2 pipeline step: filter. Step impacts:\n• weigher +0.50\n• filter +0.00→#1."
	if result.Description != expectedDescription {
		t.Errorf("Expected description '%s', got '%s'", expectedDescription, result.Description)
	}

	// Verify final scores calculation
	// Expected: host1: 1.0 + 0.5 + 0.0 = 1.5, host2: removed by filter step
	expectedFinalScores := map[string]float64{
		"host1": 1.5,
	}
	AssertFinalScores(t, result, expectedFinalScores)

	// Verify deleted hosts tracking
	expectedDeletedHosts := map[string][]string{
		"host2": {"filter"}, // host2 was deleted by the filter step
	}
	AssertDeletedHosts(t, result, expectedDeletedHosts)

	t.Logf("Reconcile completed successfully: state=%s, finalScores=%v, deletedHosts=%v",
		updatedResource.Status.State, result.FinalScores, result.DeletedHosts)
}

func TestReconcileEmptyInput(t *testing.T) {
	// Create test decision with empty input
	decision := NewTestDecision("decision-1").
		WithInput(map[string]float64{}). // Empty input - no hosts
		WithPipelineOutputs(
			NewTestPipelineOutput("weigher", map[string]float64{
				"host1": 0.5,
				"host2": 0.5,
			}),
		).
		Build()

	resource := NewTestSchedulingDecision("test-decision-empty-input").
		WithDecisions(decision).
		Build()

	fakeClient, _ := SetupTestEnvironment(t, resource)
	req := CreateTestRequest("test-decision-empty-input")

	reconciler := CreateSchedulingReconciler(fakeClient)
	_, err := reconciler.Reconcile(t.Context(), req)
	if err != nil {
		t.Fatalf("Reconcile returned an error: %v", err)
	}

	// Fetch and verify the updated resource
	updatedResource := AssertResourceExists(t, fakeClient, "test-decision-empty-input")
	AssertResourceState(t, updatedResource, v1alpha1.SchedulingDecisionStateError)
	AssertResourceError(t, updatedResource, "Decision decision-1: No hosts provided in input")

	t.Logf("Reconcile completed with error: state=%s, error=%s", updatedResource.Status.State, updatedResource.Status.Error)
}

func TestReconcileHostMismatch(t *testing.T) {
	// Create test decision with host mismatch (host3 in pipeline but not in input)
	decision := NewTestDecision("decision-1").
		WithInput(map[string]float64{
			"host1": 1.0,
			"host2": 2.0,
		}).
		WithPipelineOutputs(
			NewTestPipelineOutput("weigher", map[string]float64{
				"host1": 0.5,
				"host3": 0.3, // host3 doesn't exist in input
			}),
		).
		Build()

	resource := NewTestSchedulingDecision("test-decision-host-mismatch").
		WithDecisions(decision).
		Build()

	fakeClient, _ := SetupTestEnvironment(t, resource)
	req := CreateTestRequest("test-decision-host-mismatch")

	reconciler := CreateSchedulingReconciler(fakeClient)
	_, err := reconciler.Reconcile(t.Context(), req)
	if err != nil {
		t.Fatalf("Reconcile returned an error: %v", err)
	}

	// Fetch and verify the updated resource
	updatedResource := AssertResourceExists(t, fakeClient, "test-decision-host-mismatch")
	AssertResourceState(t, updatedResource, v1alpha1.SchedulingDecisionStateError)
	AssertResourceError(t, updatedResource, "Decision decision-1: Host 'host3' in pipeline output not found in input")

	t.Logf("Reconcile completed with host mismatch error: state=%s, error=%s", updatedResource.Status.State, updatedResource.Status.Error)
}

func TestReconcileComplexScoring(t *testing.T) {
	// Create test decision with complex multi-step pipeline
	decision := NewTestDecision("decision-1").
		WithInput(map[string]float64{
			"host1": 1.0,
			"host2": 2.0,
			"host3": 3.0,
			"host4": 4.0,
		}).
		WithPipelineOutputs(
			NewTestPipelineOutput("weigher1", map[string]float64{
				"host1": 0.5,
				"host2": 1.0,
				"host3": -0.5,
				"host4": 2.0,
			}),
			NewTestPipelineOutput("filter1", map[string]float64{
				"host1": 0.2,
				"host3": 0.1, // host2 and host4 removed by this step
			}),
			NewTestPipelineOutput("weigher2", map[string]float64{
				"host1": -0.3, // host3 removed by this step
			}),
		).
		Build()

	resource := NewTestSchedulingDecision("test-decision-complex").
		WithDecisions(decision).
		Build()

	fakeClient, _ := SetupTestEnvironment(t, resource)
	req := CreateTestRequest("test-decision-complex")

	reconciler := CreateSchedulingReconciler(fakeClient)
	_, err := reconciler.Reconcile(t.Context(), req)
	if err != nil {
		t.Fatalf("Reconcile returned an error: %v", err)
	}

	// Fetch and verify the updated resource
	updatedResource := AssertResourceExists(t, fakeClient, "test-decision-complex")
	AssertResourceState(t, updatedResource, v1alpha1.SchedulingDecisionStateResolved)
	AssertResultCount(t, updatedResource, 1)

	result := updatedResource.Status.Results[0]
	if result.ID != "decision-1" {
		t.Errorf("Expected result ID 'decision-1', got '%s'", result.ID)
	}

	// Verify final scores calculation
	// Expected: host1: 1.0 + 0.5 + 0.2 + (-0.3) = 1.4
	// host2: removed by filter1, host3: removed by weigher2, host4: removed by filter1
	expectedFinalScores := map[string]float64{
		"host1": 1.4,
	}
	AssertFinalScores(t, result, expectedFinalScores)

	// Verify deleted hosts tracking
	expectedDeletedHosts := map[string][]string{
		"host2": {"filter1"},  // host2 deleted by filter1
		"host4": {"filter1"},  // host4 deleted by filter1
		"host3": {"weigher2"}, // host3 deleted by weigher2
	}
	AssertDeletedHosts(t, result, expectedDeletedHosts)

	t.Logf("Complex scoring completed: finalScores=%v, deletedHosts=%v",
		result.FinalScores, result.DeletedHosts)
}

func TestReconcileMultipleDeletionSteps(t *testing.T) {
	// Create test decision with multiple filter steps that remove all hosts
	decision := NewTestDecision("decision-1").
		WithInput(map[string]float64{
			"host1": 1.0,
			"host2": 2.0,
			"host3": 3.0,
		}).
		WithPipelineOutputs(
			NewTestPipelineOutput("weigher1", map[string]float64{
				"host1": 0.5,
				"host2": 1.0,
				"host3": -0.5,
			}),
			NewTestPipelineOutput("filter1", map[string]float64{
				"host1": 0.2,
				// host2 and host3 removed by this step
			}),
			NewTestPipelineOutput("filter2", map[string]float64{
				// host1 removed by this step
				// host2 and host3 would be removed again, but they're already gone
			}),
		).
		Build()

	resource := NewTestSchedulingDecision("test-decision-multiple-deletions").
		WithDecisions(decision).
		Build()

	fakeClient, _ := SetupTestEnvironment(t, resource)
	req := CreateTestRequest("test-decision-multiple-deletions")

	reconciler := CreateSchedulingReconciler(fakeClient)
	_, err := reconciler.Reconcile(t.Context(), req)
	if err != nil {
		t.Fatalf("Reconcile returned an error: %v", err)
	}

	// Fetch and verify the updated resource
	updatedResource := AssertResourceExists(t, fakeClient, "test-decision-multiple-deletions")
	AssertResourceState(t, updatedResource, v1alpha1.SchedulingDecisionStateResolved)
	AssertResultCount(t, updatedResource, 1)

	result := updatedResource.Status.Results[0]
	if result.ID != "decision-1" {
		t.Errorf("Expected result ID 'decision-1', got '%s'", result.ID)
	}

	// Verify final scores calculation - all hosts should be removed, no final scores
	expectedFinalScores := map[string]float64{}
	AssertFinalScores(t, result, expectedFinalScores)

	// Verify deleted hosts tracking
	// host2 and host3 deleted by filter1, host1 deleted by filter2
	expectedDeletedHosts := map[string][]string{
		"host2": {"filter1"}, // host2 deleted by filter1
		"host3": {"filter1"}, // host3 deleted by filter1
		"host1": {"filter2"}, // host1 deleted by filter2
	}
	AssertDeletedHosts(t, result, expectedDeletedHosts)

	t.Logf("Multiple deletion test completed: finalScores=%v, deletedHosts=%v",
		result.FinalScores, result.DeletedHosts)
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
			// Create test decision with specific activations to test certainty levels
			decision := NewTestDecision("decision-1").
				WithInput(tt.input).
				WithPipelineOutputs(
					NewTestPipelineOutput("weigher", tt.activations),
				).
				Build()

			resource := NewTestSchedulingDecision("test-certainty-" + tt.name).
				WithDecisions(decision).
				Build()

			fakeClient, _ := SetupTestEnvironment(t, resource)
			req := CreateTestRequest("test-certainty-" + tt.name)

			reconciler := CreateSchedulingReconciler(fakeClient)
			_, err := reconciler.Reconcile(t.Context(), req)
			if err != nil {
				t.Fatalf("Reconcile returned an error: %v", err)
			}

			// Fetch and verify the updated resource
			updatedResource := AssertResourceExists(t, fakeClient, "test-certainty-"+tt.name)
			AssertResultCount(t, updatedResource, 1)

			result := updatedResource.Status.Results[0]
			if result.ID != "decision-1" {
				t.Errorf("Expected result ID 'decision-1', got '%s'", result.ID)
			}

			// Verify the description contains the expected winner and certainty
			AssertDescriptionContains(t, result.Description,
				"Selected: "+tt.expectedWinner,
				"certainty: "+tt.expectedCertainty,
			)

			t.Logf("Certainty test %s completed: %s", tt.name, result.Description)
		})
	}
}

func TestReconcileNoHostsRemaining(t *testing.T) {
	// Create test decision where all hosts are filtered out
	decision := NewTestDecision("decision-1").
		WithInput(map[string]float64{
			"host1": 1.0,
			"host2": 2.0,
		}).
		WithPipelineOutputs(
			NewTestPipelineOutput("filter-all", map[string]float64{
				// No hosts in activations - all will be filtered out
			}),
		).
		Build()

	resource := NewTestSchedulingDecision("test-no-hosts-remaining").
		WithDecisions(decision).
		Build()

	fakeClient, _ := SetupTestEnvironment(t, resource)
	req := CreateTestRequest("test-no-hosts-remaining")

	reconciler := CreateSchedulingReconciler(fakeClient)
	_, err := reconciler.Reconcile(t.Context(), req)
	if err != nil {
		t.Fatalf("Reconcile returned an error: %v", err)
	}

	// Fetch and verify the updated resource
	updatedResource := AssertResourceExists(t, fakeClient, "test-no-hosts-remaining")
	AssertResourceState(t, updatedResource, v1alpha1.SchedulingDecisionStateResolved)
	AssertResultCount(t, updatedResource, 1)

	result := updatedResource.Status.Results[0]
	if result.ID != "decision-1" {
		t.Errorf("Expected result ID 'decision-1', got '%s'", result.ID)
	}

	// Verify no final scores since all hosts were filtered out
	expectedFinalScores := map[string]float64{}
	AssertFinalScores(t, result, expectedFinalScores)

	expectedDescription := "No hosts remaining after filtering, 2 hosts evaluated"
	if result.Description != expectedDescription {
		t.Errorf("Expected description '%s', got '%s'", expectedDescription, result.Description)
	}

	t.Logf("No hosts remaining test completed: %s", result.Description)
}

func TestReconcileInputVsFinalComparison(t *testing.T) {
	tests := []struct {
		name                 string
		input                map[string]float64
		activations          map[string]float64
		expectedDescContains []string
	}{
		{
			name: "input-choice-confirmed",
			input: map[string]float64{
				"host1": 3.0, // highest in input
				"host2": 2.0,
				"host3": 1.0,
			},
			activations: map[string]float64{
				"host1": 0.5, "host2": 0.3, "host3": 0.1, // host1 stays winner
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
			activations: map[string]float64{
				"host1": 0.5, "host3": 0.3, // host2 filtered out, host3 becomes winner
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
			activations: map[string]float64{
				"host1": 2.5, "host2": -0.5, "host3": 0.8, // host1 becomes winner, host2 demoted to #3
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
			// Create test decision to compare input vs final rankings
			decision := NewTestDecision("decision-1").
				WithInput(tt.input).
				WithPipelineOutputs(
					NewTestPipelineOutput("weigher", tt.activations),
				).
				Build()

			resource := NewTestSchedulingDecision("test-input-vs-final-" + tt.name).
				WithDecisions(decision).
				Build()

			fakeClient, _ := SetupTestEnvironment(t, resource)
			req := CreateTestRequest("test-input-vs-final-" + tt.name)

			reconciler := CreateSchedulingReconciler(fakeClient)
			_, err := reconciler.Reconcile(t.Context(), req)
			if err != nil {
				t.Fatalf("Reconcile returned an error: %v", err)
			}

			// Fetch and verify the updated resource
			updatedResource := AssertResourceExists(t, fakeClient, "test-input-vs-final-"+tt.name)
			AssertResultCount(t, updatedResource, 1)

			result := updatedResource.Status.Results[0]
			if result.ID != "decision-1" {
				t.Errorf("Expected result ID 'decision-1', got '%s'", result.ID)
			}

			// Verify the description contains expected elements
			AssertDescriptionContains(t, result.Description, tt.expectedDescContains...)

			t.Logf("Input vs Final test %s completed: %s", tt.name, result.Description)
		})
	}
}

func TestReconcileCriticalStepElimination(t *testing.T) {
	tests := []struct {
		name                    string
		input                   map[string]float64
		pipelineOutputs         []v1alpha1.SchedulingDecisionPipelineOutputSpec
		expectedCriticalMessage string
	}{
		{
			name: "single-critical-step",
			input: map[string]float64{
				"host1": 2.0, // Would win without pipeline
				"host2": 1.0,
				"host3": 1.5,
			},
			pipelineOutputs: []v1alpha1.SchedulingDecisionPipelineOutputSpec{
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
			pipelineOutputs: []v1alpha1.SchedulingDecisionPipelineOutputSpec{
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
			pipelineOutputs: []v1alpha1.SchedulingDecisionPipelineOutputSpec{
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
			// Create test decision with multiple pipeline steps to test critical step analysis
			decision := NewTestDecision("decision-1").
				WithInput(tt.input).
				WithPipelineOutputs(tt.pipelineOutputs...).
				Build()

			resource := NewTestSchedulingDecision("test-critical-steps-" + tt.name).
				WithDecisions(decision).
				Build()

			fakeClient, _ := SetupTestEnvironment(t, resource)
			req := CreateTestRequest("test-critical-steps-" + tt.name)

			reconciler := CreateSchedulingReconciler(fakeClient)
			_, err := reconciler.Reconcile(t.Context(), req)
			if err != nil {
				t.Fatalf("Reconcile returned an error: %v", err)
			}

			// Fetch and verify the updated resource
			updatedResource := AssertResourceExists(t, fakeClient, "test-critical-steps-"+tt.name)
			AssertResultCount(t, updatedResource, 1)

			result := updatedResource.Status.Results[0]
			if result.ID != "decision-1" {
				t.Errorf("Expected result ID 'decision-1', got '%s'", result.ID)
			}

			// Verify the description contains the expected critical step message
			AssertDescriptionContains(t, result.Description, tt.expectedCriticalMessage)

			t.Logf("Critical step test %s completed: %s", tt.name, result.Description)
		})
	}
}

func TestReconcileGlobalDescription(t *testing.T) {
	tests := []struct {
		name                      string
		decisions                 []v1alpha1.SchedulingDecisionRequest
		expectedGlobalDescription string
	}{
		{
			name: "single-decision-no-global",
			decisions: []v1alpha1.SchedulingDecisionRequest{
				NewTestDecision("decision-1").
					WithInput(map[string]float64{"host1": 1.0, "host2": 2.0}).
					WithPipelineOutputs(NewTestPipelineOutput("weigher", map[string]float64{"host1": 1.0, "host2": 0.0})).
					Build(),
			},
			expectedGlobalDescription: "", // No global description for single decision
		},
		{
			name: "simple-chain-no-loop",
			decisions: []v1alpha1.SchedulingDecisionRequest{
				NewTestDecision("decision-1").
					WithRequestedAt(time.Now().Add(-5 * time.Hour)).
					WithInput(map[string]float64{"host1": 1.0, "host2": 2.0}).
					WithPipelineOutputs(NewTestPipelineOutput("weigher", map[string]float64{"host1": 2.0, "host2": 0.0})).
					Build(),
				NewTestDecision("decision-2").
					WithRequestedAt(time.Now().Add(-3 * time.Hour)).
					WithInput(map[string]float64{"host2": 1.0, "host3": 2.0}).
					WithPipelineOutputs(NewTestPipelineOutput("weigher", map[string]float64{"host2": 1.5, "host3": 0.0})).
					Build(),
				NewTestDecision("decision-3").
					WithRequestedAt(time.Now().Add(-1 * time.Hour)).
					WithInput(map[string]float64{"host2": 1.0, "host3": 2.0}).
					WithPipelineOutputs(NewTestPipelineOutput("weigher", map[string]float64{"host2": 1.5, "host3": 0.0})).
					Build(),
				NewTestDecision("decision-4").
					WithRequestedAt(time.Now()).
					WithInput(map[string]float64{"host3": 1.0, "host4": 2.0}).
					WithPipelineOutputs(NewTestPipelineOutput("weigher", map[string]float64{"host3": 0.0, "host4": 1.0})).
					Build(),
			},
			expectedGlobalDescription: "chain: host1 (2h) -> host2 (3h; 2 decisions) -> host4 (0m)",
		},
		{
			name: "chain-with-loop",
			decisions: []v1alpha1.SchedulingDecisionRequest{
				NewTestDecision("decision-1").
					WithRequestedAt(time.Now().Add(-5 * time.Hour)).
					WithInput(map[string]float64{"host1": 1.0, "host2": 2.0}).
					WithPipelineOutputs(NewTestPipelineOutput("weigher", map[string]float64{"host1": 2.0, "host2": 0.0})).
					Build(),
				NewTestDecision("decision-2").
					WithRequestedAt(time.Now().Add(-2 * time.Hour)).
					WithInput(map[string]float64{"host1": 1.0, "host2": 2.0}).
					WithPipelineOutputs(NewTestPipelineOutput("weigher", map[string]float64{"host1": 0.0, "host2": 1.0})).
					Build(),
				NewTestDecision("decision-3").
					WithRequestedAt(time.Now().Add(-1 * time.Hour)).
					WithInput(map[string]float64{"host1": 1.0, "host2": 2.0}).
					WithPipelineOutputs(NewTestPipelineOutput("weigher", map[string]float64{"host1": 2.0, "host2": 0.0})).
					Build(),
				NewTestDecision("decision-4").
					WithRequestedAt(time.Now()).
					WithInput(map[string]float64{"host3": 1.0}).
					WithPipelineOutputs(NewTestPipelineOutput("weigher", map[string]float64{"host3": 0.0})).
					Build(),
			},
			expectedGlobalDescription: "chain (loop detected): host1 (3h) -> host2 (1h) -> host1 (1h) -> host3 (0m)",
		},
		{
			name: "same-host-all-decisions-no-loop",
			decisions: []v1alpha1.SchedulingDecisionRequest{
				NewTestDecision("decision-1").
					WithRequestedAt(time.Now().Add(-2 * time.Hour)).
					WithInput(map[string]float64{"host1": 2.0, "host2": 1.0}).
					WithPipelineOutputs(NewTestPipelineOutput("weigher", map[string]float64{"host1": 1.0, "host2": 0.0})).
					Build(),
				NewTestDecision("decision-2").
					WithRequestedAt(time.Now()).
					WithInput(map[string]float64{"host1": 2.0, "host3": 1.0}).
					WithPipelineOutputs(NewTestPipelineOutput("weigher", map[string]float64{"host1": 1.0, "host3": 0.0})).
					Build(),
			},
			expectedGlobalDescription: "chain: host1 (2h; 2 decisions)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := NewTestSchedulingDecision("test-global-" + tt.name).
				WithDecisions(tt.decisions...).
				Build()

			fakeClient, _ := SetupTestEnvironment(t, resource)
			req := CreateTestRequest("test-global-" + tt.name)

			reconciler := CreateSchedulingReconciler(fakeClient)
			_, err := reconciler.Reconcile(t.Context(), req)
			if err != nil {
				t.Fatalf("Reconcile returned an error: %v", err)
			}

			// Fetch and verify the updated resource
			updatedResource := AssertResourceExists(t, fakeClient, "test-global-"+tt.name)
			AssertResourceState(t, updatedResource, v1alpha1.SchedulingDecisionStateResolved)
			AssertDecisionCount(t, updatedResource, len(tt.decisions))

			// Verify global description
			if updatedResource.Status.GlobalDescription != tt.expectedGlobalDescription {
				t.Errorf("Expected global description '%s', got '%s'",
					tt.expectedGlobalDescription, updatedResource.Status.GlobalDescription)
			}

			t.Logf("Global description test %s completed: '%s'", tt.name, updatedResource.Status.GlobalDescription)
		})
	}
}

// TestReconcileEmptyDecisionsList tests the case where no decisions are provided
func TestReconcileEmptyDecisionsList(t *testing.T) {
	resource := NewTestSchedulingDecision("test-empty-decisions").
		WithDecisions(). // No decisions provided
		Build()

	fakeClient, _ := SetupTestEnvironment(t, resource)
	req := CreateTestRequest("test-empty-decisions")

	reconciler := CreateSchedulingReconciler(fakeClient)
	_, err := reconciler.Reconcile(t.Context(), req)
	if err != nil {
		t.Fatalf("Reconcile returned an error: %v", err)
	}

	// Fetch and verify the updated resource
	updatedResource := AssertResourceExists(t, fakeClient, "test-empty-decisions")
	AssertResourceState(t, updatedResource, v1alpha1.SchedulingDecisionStateError)
	AssertResourceError(t, updatedResource, "No decisions provided in spec")

	t.Logf("Empty decisions test completed: state=%s, error=%s", updatedResource.Status.State, updatedResource.Status.Error)
}

// TestReconcileResourceNotFound tests the case where the resource is deleted during reconciliation
func TestReconcileResourceNotFound(t *testing.T) {
	fakeClient, _ := SetupTestEnvironment(t) // No resource created
	req := CreateTestRequest("non-existent-resource")

	reconciler := CreateSchedulingReconciler(fakeClient)
	_, err := reconciler.Reconcile(t.Context(), req)

	// Should return an error when resource is not found
	if err == nil {
		t.Fatalf("Expected error when resource not found, got nil")
	}

	t.Logf("Resource not found test completed: error=%v", err)
}

// TestUtilityFunctions tests the standalone utility functions
func TestUtilityFunctions(t *testing.T) {
	t.Run("findWinner", func(t *testing.T) {
		tests := []struct {
			name           string
			scores         map[string]float64
			expectedWinner string
			expectedScore  float64
		}{
			{
				name:           "empty-map",
				scores:         map[string]float64{},
				expectedWinner: "",
				expectedScore:  MinScoreValue,
			},
			{
				name:           "single-host",
				scores:         map[string]float64{"host1": 5.0},
				expectedWinner: "host1",
				expectedScore:  5.0,
			},
			{
				name:           "clear-winner",
				scores:         map[string]float64{"host1": 3.0, "host2": 1.0, "host3": 2.0},
				expectedWinner: "host1",
				expectedScore:  3.0,
			},
			{
				name:           "tied-scores",
				scores:         map[string]float64{"host1": 2.0, "host2": 2.0},
				expectedWinner: "", // Don't check specific winner for tied scores (map iteration order is not deterministic)
				expectedScore:  2.0,
			},
			{
				name:           "negative-scores",
				scores:         map[string]float64{"host1": -1.0, "host2": -2.0},
				expectedWinner: "host1",
				expectedScore:  -1.0,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				winner, score := findWinner(tt.scores)
				if tt.expectedWinner != "" && winner != tt.expectedWinner {
					t.Errorf("Expected winner '%s', got '%s'", tt.expectedWinner, winner)
				}
				if score != tt.expectedScore {
					t.Errorf("Expected score %f, got %f", tt.expectedScore, score)
				}
				// For tied scores, just verify we got one of the tied hosts
				if tt.name == "tied-scores" {
					if winner != "host1" && winner != "host2" {
						t.Errorf("Expected winner to be either 'host1' or 'host2', got '%s'", winner)
					}
				}
			})
		}
	})

	t.Run("mapToSortedHostScores", func(t *testing.T) {
		scores := map[string]float64{
			"host1": 1.0,
			"host2": 3.0,
			"host3": 2.0,
		}
		sorted := mapToSortedHostScores(scores)

		if len(sorted) != 3 {
			t.Errorf("Expected 3 sorted hosts, got %d", len(sorted))
		}

		// Should be sorted by score descending
		if sorted[0].host != "host2" || sorted[0].score != 3.0 {
			t.Errorf("Expected first host to be host2 with score 3.0, got %s with %f", sorted[0].host, sorted[0].score)
		}
		if sorted[1].host != "host3" || sorted[1].score != 2.0 {
			t.Errorf("Expected second host to be host3 with score 2.0, got %s with %f", sorted[1].host, sorted[1].score)
		}
		if sorted[2].host != "host1" || sorted[2].score != 1.0 {
			t.Errorf("Expected third host to be host1 with score 1.0, got %s with %f", sorted[2].host, sorted[2].score)
		}
	})

	t.Run("findHostPosition", func(t *testing.T) {
		hosts := []hostScore{
			{host: "host2", score: 3.0},
			{host: "host3", score: 2.0},
			{host: "host1", score: 1.0},
		}

		tests := []struct {
			targetHost       string
			expectedPosition int
		}{
			{"host2", 1},  // First position
			{"host3", 2},  // Second position
			{"host1", 3},  // Third position
			{"host4", -1}, // Not found
		}

		for _, tt := range tests {
			position := findHostPosition(hosts, tt.targetHost)
			if position != tt.expectedPosition {
				t.Errorf("Expected position %d for host %s, got %d", tt.expectedPosition, tt.targetHost, position)
			}
		}
	})

	t.Run("getCertaintyLevel", func(t *testing.T) {
		tests := []struct {
			gap               float64
			expectedCertainty string
		}{
			{1.0, "high"},   // >= 0.5
			{0.5, "high"},   // exactly 0.5
			{0.3, "medium"}, // >= 0.2, < 0.5
			{0.2, "medium"}, // exactly 0.2
			{0.1, "low"},    // >= 0.0, < 0.2
			{0.0, "low"},    // exactly 0.0
			{-0.1, "low"},   // < 0.0
		}

		for _, tt := range tests {
			certainty := getCertaintyLevel(tt.gap)
			if certainty != tt.expectedCertainty {
				t.Errorf("Expected certainty '%s' for gap %f, got '%s'", tt.expectedCertainty, tt.gap, certainty)
			}
		}
	})
}

// TestStepImpactAnalysis tests the step impact calculation logic
func TestStepImpactAnalysis(t *testing.T) {
	reconciler := &SchedulingDecisionReconciler{}

	t.Run("promotion-scenarios", func(t *testing.T) {
		input := map[string]float64{
			"host1": 1.0, // Will become winner
			"host2": 3.0, // Initial winner
			"host3": 2.0,
		}

		outputs := []v1alpha1.SchedulingDecisionPipelineOutputSpec{
			{
				Step: "promotion-step",
				Activations: map[string]float64{
					"host1": 2.5,  // host1: 3.5 (becomes winner)
					"host2": -0.5, // host2: 2.5 (demoted)
					"host3": 0.0,  // host3: 2.0
				},
			},
		}

		finalScores := map[string]float64{
			"host1": 3.5,
			"host2": 2.5,
			"host3": 2.0,
		}

		impacts := reconciler.calculateStepImpacts(input, outputs, finalScores)

		if len(impacts) != 1 {
			t.Fatalf("Expected 1 step impact, got %d", len(impacts))
		}

		impact := impacts[0]
		if impact.Step != "promotion-step" {
			t.Errorf("Expected step 'promotion-step', got '%s'", impact.Step)
		}
		if !impact.PromotedToFirst {
			t.Errorf("Expected PromotedToFirst to be true")
		}
		if impact.ScoreDelta != 2.5 {
			t.Errorf("Expected ScoreDelta 2.5, got %f", impact.ScoreDelta)
		}
		if impact.CompetitorsRemoved != 0 {
			t.Errorf("Expected CompetitorsRemoved 0, got %d", impact.CompetitorsRemoved)
		}
	})

	t.Run("competitor-removal", func(t *testing.T) {
		input := map[string]float64{
			"host1": 1.0, // Will become winner after competitors removed
			"host2": 3.0, // Initial winner, will be removed
			"host3": 2.0, // Will be removed
		}

		outputs := []v1alpha1.SchedulingDecisionPipelineOutputSpec{
			{
				Step: "filter-step",
				Activations: map[string]float64{
					"host1": 0.0, // Only host1 survives
				},
			},
		}

		finalScores := map[string]float64{
			"host1": 1.0,
		}

		impacts := reconciler.calculateStepImpacts(input, outputs, finalScores)

		if len(impacts) != 1 {
			t.Fatalf("Expected 1 step impact, got %d", len(impacts))
		}

		impact := impacts[0]
		if impact.CompetitorsRemoved != 2 {
			t.Errorf("Expected CompetitorsRemoved 2, got %d", impact.CompetitorsRemoved)
		}
		if !impact.PromotedToFirst {
			t.Errorf("Expected PromotedToFirst to be true (host1 was not #1 before, became #1 after competitors removed)")
		}
		if impact.ScoreDelta != 0.0 {
			t.Errorf("Expected ScoreDelta 0.0, got %f", impact.ScoreDelta)
		}
	})

	t.Run("empty-inputs", func(t *testing.T) {
		// Test with empty final scores
		impacts := reconciler.calculateStepImpacts(map[string]float64{}, []v1alpha1.SchedulingDecisionPipelineOutputSpec{}, map[string]float64{})
		if len(impacts) != 0 {
			t.Errorf("Expected 0 impacts for empty inputs, got %d", len(impacts))
		}

		// Test with no outputs
		impacts = reconciler.calculateStepImpacts(map[string]float64{"host1": 1.0}, []v1alpha1.SchedulingDecisionPipelineOutputSpec{}, map[string]float64{"host1": 1.0})
		if len(impacts) != 0 {
			t.Errorf("Expected 0 impacts for no outputs, got %d", len(impacts))
		}
	})
}

// TestLargeDatasetPerformance tests the controller with larger datasets
func TestLargeDatasetPerformance(t *testing.T) {
	// Create a decision with many hosts
	input := make(map[string]float64)
	activations := make(map[string]float64)

	for i := 0; i < 100; i++ {
		hostName := fmt.Sprintf("host%d", i)
		input[hostName] = float64(i)
		activations[hostName] = float64(i % 10) // Vary activations
	}

	decision := NewTestDecision("large-decision").
		WithInput(input).
		WithPipelineOutputs(
			NewTestPipelineOutput("weigher1", activations),
			NewTestPipelineOutput("weigher2", activations),
			NewTestPipelineOutput("weigher3", activations),
		).
		Build()

	resource := NewTestSchedulingDecision("test-large-dataset").
		WithDecisions(decision).
		Build()

	fakeClient, _ := SetupTestEnvironment(t, resource)
	req := CreateTestRequest("test-large-dataset")

	reconciler := CreateSchedulingReconciler(fakeClient)

	start := time.Now()
	_, err := reconciler.Reconcile(t.Context(), req)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Reconcile returned an error: %v", err)
	}

	// Verify the result
	updatedResource := AssertResourceExists(t, fakeClient, "test-large-dataset")
	AssertResourceState(t, updatedResource, v1alpha1.SchedulingDecisionStateResolved)
	AssertResultCount(t, updatedResource, 1)

	result := updatedResource.Status.Results[0]
	if len(result.FinalScores) != 100 {
		t.Errorf("Expected 100 final scores, got %d", len(result.FinalScores))
	}

	t.Logf("Large dataset test completed in %v with %d hosts", duration, len(result.FinalScores))

	// Performance check - should complete within reasonable time
	if duration > 5*time.Second {
		t.Errorf("Large dataset processing took too long: %v", duration)
	}
}
