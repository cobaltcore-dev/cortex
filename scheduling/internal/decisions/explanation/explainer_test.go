// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package explanation

import (
	"context"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestExplainer_Explain(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	tests := []struct {
		name              string
		decision          *v1alpha1.Decision
		existingDecisions []v1alpha1.Decision
		expectedContains  []string
		expectError       bool
	}{
		{
			name: "initial nova server placement",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource-1",
				},
				Status: v1alpha1.DecisionStatus{
					History: nil,
				},
			},
			expectedContains: []string{"Initial placement of the nova server"},
		},
		{
			name: "initial cinder volume placement",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeCinderVolume,
					ResourceID: "test-resource-2",
				},
				Status: v1alpha1.DecisionStatus{
					History: nil,
				},
			},
			expectedContains: []string{"Initial placement of the cinder volume"},
		},
		{
			name: "initial manila share placement",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeManilaShare,
					ResourceID: "test-resource-3",
				},
				Status: v1alpha1.DecisionStatus{
					History: nil,
				},
			},
			expectedContains: []string{"Initial placement of the manila share"},
		},
		{
			name: "initial ironcore machine placement",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeIroncoreMachine,
					ResourceID: "test-resource-4",
				},
				Status: v1alpha1.DecisionStatus{
					History: nil,
				},
			},
			expectedContains: []string{"Initial placement of the ironcore machine"},
		},
		{
			name: "unknown resource type falls back to generic",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       "unknown-type",
					ResourceID: "test-resource-5",
				},
				Status: v1alpha1.DecisionStatus{
					History: nil,
				},
			},
			expectedContains: []string{"Initial placement of the resource"},
		},
		{
			name: "empty history array",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource-6",
				},
				Status: v1alpha1.DecisionStatus{
					History: &[]corev1.ObjectReference{},
				},
			},
			expectedContains: []string{"Initial placement of the nova server"},
		},
		{
			name: "subsequent decision with history",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-decision-2",
					Namespace:         "default",
					CreationTimestamp: metav1.Time{Time: metav1.Now().Add(1000)},
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource-7",
				},
				Status: v1alpha1.DecisionStatus{
					History: &[]corev1.ObjectReference{
						{
							Kind:      "Decision",
							Namespace: "default",
							Name:      "test-decision-1",
							UID:       "test-uid-1",
						},
					},
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-2"),
					},
				},
			},
			existingDecisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-decision-1",
						Namespace: "default",
						UID:       "test-uid-1",
					},
					Spec: v1alpha1.DecisionSpec{
						Type:       v1alpha1.DecisionTypeNovaServer,
						ResourceID: "test-resource-7",
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-1"),
						},
					},
				},
			},
			expectedContains: []string{
				"Decision #2 for this nova server",
				"Previous target host was 'host-1'",
				"now it's 'host-2'",
			},
		},
		{
			name: "subsequent decision with nil target hosts",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-4",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource-8",
				},
				Status: v1alpha1.DecisionStatus{
					History: &[]corev1.ObjectReference{
						{
							Kind:      "Decision",
							Namespace: "default",
							Name:      "test-decision-3",
							UID:       "test-uid-3",
						},
					},
					Result: nil,
				},
			},
			existingDecisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-decision-3",
						Namespace: "default",
						UID:       "test-uid-3",
					},
					Spec: v1alpha1.DecisionSpec{
						Type:       v1alpha1.DecisionTypeNovaServer,
						ResourceID: "test-resource-8",
					},
					Status: v1alpha1.DecisionStatus{
						Result: nil,
					},
				},
			},
			expectedContains: []string{
				"Decision #2 for this nova server",
				"Previous target host was '(n/a)'",
				"now it's '(n/a)'",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []runtime.Object{tt.decision}
			for i := range tt.existingDecisions {
				objects = append(objects, &tt.existingDecisions[i])
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				Build()

			explainer := &Explainer{Client: client}

			explanation, err := explainer.Explain(context.Background(), tt.decision)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
				return
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}

			for _, expected := range tt.expectedContains {
				if !contains(explanation, expected) {
					t.Errorf("Expected explanation to contain '%s', but got: %s", expected, explanation)
				}
			}
		})
	}
}

func TestExplainer_Explain_HistoryDecisionNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	decision := &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-decision",
			Namespace: "default",
		},
		Spec: v1alpha1.DecisionSpec{
			Type:       v1alpha1.DecisionTypeNovaServer,
			ResourceID: "test-resource",
		},
		Status: v1alpha1.DecisionStatus{
			History: &[]corev1.ObjectReference{
				{
					Kind:      "Decision",
					Namespace: "default",
					Name:      "non-existent-decision",
					UID:       "non-existent-uid",
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(decision).
		Build()

	explainer := &Explainer{Client: client}

	_, err := explainer.Explain(context.Background(), decision)
	if err == nil {
		t.Error("Expected error when previous decision not found, but got none")
	}
}

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || substr == "" || findInString(s, substr))
}

func findInString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Test data creation helpers
func createBasicDecision(name, resourceID string, decisionType v1alpha1.DecisionType) *v1alpha1.Decision {
	return &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: v1alpha1.DecisionSpec{
			Type:       decisionType,
			ResourceID: resourceID,
		},
		Status: v1alpha1.DecisionStatus{
			History: nil,
		},
	}
}

func createDecisionWithResult(name, resourceID string, targetHost string, weights map[string]float64, orderedHosts []string) *v1alpha1.Decision {
	decision := createBasicDecision(name, resourceID, v1alpha1.DecisionTypeNovaServer)
	decision.Status.Result = &v1alpha1.DecisionResult{
		TargetHost:           stringPtr(targetHost),
		AggregatedOutWeights: weights,
		OrderedHosts:         orderedHosts,
	}
	return decision
}

func createDecisionWithInputComparison(name, resourceID, targetHost string, rawWeights, finalWeights map[string]float64) *v1alpha1.Decision {
	decision := createDecisionWithResult(name, resourceID, targetHost, finalWeights, nil)
	decision.Status.Result.RawInWeights = rawWeights
	return decision
}

func createDecisionWithNormalizedWeights(name, resourceID, targetHost string, rawWeights, normalizedWeights, finalWeights map[string]float64) *v1alpha1.Decision {
	decision := createDecisionWithInputComparison(name, resourceID, targetHost, rawWeights, finalWeights)
	decision.Status.Result.NormalizedInWeights = normalizedWeights
	return decision
}

func createStepResult(stepName string, activations map[string]float64) v1alpha1.StepResult {
	return v1alpha1.StepResult{
		StepRef:     corev1.ObjectReference{Name: stepName},
		Activations: activations,
	}
}

func createDecisionWithSteps(name, resourceID, targetHost string, stepResults []v1alpha1.StepResult) *v1alpha1.Decision {
	decision := createBasicDecision(name, resourceID, v1alpha1.DecisionTypeNovaServer)
	decision.Status.Result = &v1alpha1.DecisionResult{
		TargetHost:  stringPtr(targetHost),
		StepResults: stepResults,
	}
	return decision
}

func createDecisionWithHistory(name, resourceID string, historyRefs []corev1.ObjectReference, result *v1alpha1.DecisionResult) *v1alpha1.Decision {
	decision := createBasicDecision(name, resourceID, v1alpha1.DecisionTypeNovaServer)
	decision.Status.History = &historyRefs
	decision.Status.Result = result
	return decision
}

func TestExplainer_WinnerAnalysis(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	tests := []struct {
		name             string
		decision         *v1alpha1.Decision
		expectedContains []string
	}{
		{
			name: "winner analysis with score gap",
			decision: createDecisionWithResult("test-decision", "test-resource", "host-1",
				map[string]float64{"host-1": 2.45, "host-2": 2.10, "host-3": 1.85},
				[]string{"host-1", "host-2", "host-3"}),
			expectedContains: []string{
				"Selected: host-1 (score: 2.45)",
				"gap to 2nd: 0.35",
				"3 hosts evaluated",
			},
		},
		{
			name: "winner analysis with single host",
			decision: createDecisionWithResult("test-decision", "test-resource", "host-1",
				map[string]float64{"host-1": 2.45},
				[]string{"host-1"}),
			expectedContains: []string{
				"Selected: host-1 (score: 2.45)",
				"1 hosts evaluated",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(tt.decision).
				Build()

			explainer := &Explainer{Client: client}

			explanation, err := explainer.Explain(context.Background(), tt.decision)
			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}

			for _, expected := range tt.expectedContains {
				if !contains(explanation, expected) {
					t.Errorf("Expected explanation to contain '%s', but got: %s", expected, explanation)
				}
			}
		})
	}
}

func TestExplainer_InputComparison(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	tests := []struct {
		name             string
		decision         *v1alpha1.Decision
		expectedContains []string
	}{
		{
			name: "input choice confirmed",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
						RawInWeights: map[string]float64{
							"host-1": 1.20,
							"host-2": 1.10,
							"host-3": 0.95,
						},
						AggregatedOutWeights: map[string]float64{
							"host-1": 2.45,
							"host-2": 2.10,
							"host-3": 1.85,
						},
					},
				},
			},
			expectedContains: []string{
				"Input choice confirmed: host-1 (1.20→2.45)",
			},
		},
		{
			name: "input choice overridden",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-2"),
						RawInWeights: map[string]float64{
							"host-1": 1.50,
							"host-2": 1.20,
							"host-3": 0.95,
						},
						AggregatedOutWeights: map[string]float64{
							"host-1": 1.85,
							"host-2": 2.45,
							"host-3": 2.10,
						},
					},
				},
			},
			expectedContains: []string{
				"Input favored host-1 (1.50), final winner: host-2 (1.20→2.45)",
			},
		},
		{
			name: "raw weights preferred over normalized",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
						RawInWeights: map[string]float64{
							"host-1": 100.0,
							"host-2": 90.0,
						},
						NormalizedInWeights: map[string]float64{
							"host-1": 1.0,
							"host-2": 0.9,
						},
						AggregatedOutWeights: map[string]float64{
							"host-1": 2.45,
							"host-2": 2.10,
						},
					},
				},
			},
			expectedContains: []string{
				"Input choice confirmed: host-1 (100.00→2.45)", // Should now use raw weights (100.00)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(tt.decision).
				Build()

			explainer := &Explainer{Client: client}

			explanation, err := explainer.Explain(context.Background(), tt.decision)
			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}

			for _, expected := range tt.expectedContains {
				if !contains(explanation, expected) {
					t.Errorf("Expected explanation to contain '%s', but got: %s", expected, explanation)
				}
			}
		})
	}
}

func TestExplainer_CriticalStepsAnalysis(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	tests := []struct {
		name             string
		decision         *v1alpha1.Decision
		expectedContains []string
	}{
		{
			name: "single critical step",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
						NormalizedInWeights: map[string]float64{
							"host-1": 1.0,
							"host-2": 2.0, // host-2 would win without pipeline
						},
						StepResults: []v1alpha1.StepResult{
							{
								StepRef: corev1.ObjectReference{Name: "resource-weigher"},
								Activations: map[string]float64{
									"host-1": 1.5, // host-1: 2.5, host-2: 2.2 (host-1 becomes winner)
									"host-2": 0.2,
								},
							},
							{
								StepRef: corev1.ObjectReference{Name: "availability-filter"},
								Activations: map[string]float64{
									"host-1": 0.0, // Small non-critical change
									"host-2": 0.0,
								},
							},
						},
					},
				},
			},
			expectedContains: []string{
				"Decision driven by 1/2 pipeline step: resource-weigher",
			},
		},
		{
			name: "multiple critical steps",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
						NormalizedInWeights: map[string]float64{
							"host-1": 1.0,
							"host-2": 3.0, // host-2 would win without pipeline
						},
						StepResults: []v1alpha1.StepResult{
							{
								StepRef: corev1.ObjectReference{Name: "resource-weigher"},
								Activations: map[string]float64{
									"host-1": 1.0, // host-1: 2.0, host-2: 2.5 (ties host2 and host3)
									"host-2": -0.5,
								},
							},
							{
								StepRef: corev1.ObjectReference{Name: "availability-filter"},
								Activations: map[string]float64{
									"host-1": 1.0, // host-1: 3.0, host-2: 2.5 (host-1 becomes winner)
									"host-2": 0.0,
								},
							},
							{
								StepRef: corev1.ObjectReference{Name: "placement-policy"},
								Activations: map[string]float64{
									"host-1": 0.05, // Small non-critical change
									"host-2": 0.05,
								},
							},
						},
					},
				},
			},
			expectedContains: []string{
				"Decision driven by 2/3 pipeline steps: resource-weigher and availability-filter",
			},
		},
		{
			name: "all steps non-critical",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
						NormalizedInWeights: map[string]float64{
							"host-1": 3.0, // Clear winner from input
							"host-2": 1.0,
						},
						StepResults: []v1alpha1.StepResult{
							{
								StepRef: corev1.ObjectReference{Name: "step-1"},
								Activations: map[string]float64{
									"host-1": 0.05, // Small changes don't change winner
									"host-2": 0.05,
								},
							},
							{
								StepRef: corev1.ObjectReference{Name: "step-2"},
								Activations: map[string]float64{
									"host-1": 0.02,
									"host-2": 0.02,
								},
							},
						},
					},
				},
			},
			expectedContains: []string{
				"Decision driven by input only (all 2 steps are non-critical)",
			},
		},
		{
			name: "all steps critical",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
						NormalizedInWeights: map[string]float64{
							"host-1": 1.0,
							"host-2": 3.0, // host-2 would win without pipeline
						},
						StepResults: []v1alpha1.StepResult{
							{
								StepRef: corev1.ObjectReference{Name: "step-1"},
								Activations: map[string]float64{
									"host-1": 1.0, // host-1: 2.0, host-2: 2.5 (ties)
									"host-2": -0.5,
								},
							},
							{
								StepRef: corev1.ObjectReference{Name: "step-2"},
								Activations: map[string]float64{
									"host-1": 1.0, // host-1: 3.0, host-2: 2.5 (host-1 becomes winner)
									"host-2": 0.0,
								},
							},
						},
					},
				},
			},
			expectedContains: []string{
				"Decision requires all 2 pipeline steps",
			},
		},
		{
			name: "three critical steps formatting",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
						NormalizedInWeights: map[string]float64{
							"host-1": 1.0,
							"host-2": 4.0, // host-2 would win without pipeline
						},
						StepResults: []v1alpha1.StepResult{
							{
								StepRef: corev1.ObjectReference{Name: "step-a"},
								Activations: map[string]float64{
									"host-1": 1.0, // host-1: 2.0, host-2: 3.5
									"host-2": -0.5,
								},
							},
							{
								StepRef: corev1.ObjectReference{Name: "step-b"},
								Activations: map[string]float64{
									"host-1": 1.0, // host-1: 3.0, host-2: 3.5
									"host-2": 0.0,
								},
							},
							{
								StepRef: corev1.ObjectReference{Name: "step-c"},
								Activations: map[string]float64{
									"host-1": 1.0, // host-1: 4.0, host-2: 3.5 (host-1 becomes winner)
									"host-2": 0.0,
								},
							},
							{
								StepRef: corev1.ObjectReference{Name: "step-d"},
								Activations: map[string]float64{
									"host-1": 0.05, // Small non-critical change
									"host-2": 0.05,
								},
							},
						},
					},
				},
			},
			expectedContains: []string{
				"Decision driven by 3/4 pipeline steps: step-a, step-b and step-c",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(tt.decision).
				Build()

			explainer := &Explainer{Client: client}

			explanation, err := explainer.Explain(context.Background(), tt.decision)
			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}

			for _, expected := range tt.expectedContains {
				if !contains(explanation, expected) {
					t.Errorf("Expected explanation to contain '%s', but got: %s", expected, explanation)
				}
			}
		})
	}
}

func TestExplainer_CompleteExplanation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// Test a complete explanation with all features
	decision := &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-decision-2",
			Namespace: "default",
		},
		Spec: v1alpha1.DecisionSpec{
			Type:       v1alpha1.DecisionTypeNovaServer,
			ResourceID: "test-resource",
		},
		Status: v1alpha1.DecisionStatus{
			History: &[]corev1.ObjectReference{
				{
					Kind:      "Decision",
					Namespace: "default",
					Name:      "test-decision-1",
					UID:       "test-uid-1",
				},
			},
			Precedence: intPtr(1),
			Result: &v1alpha1.DecisionResult{
				TargetHost: stringPtr("host-2"),
				RawInWeights: map[string]float64{
					"host-1": 1.50, // host-1 would win without pipeline
					"host-2": 1.20,
					"host-3": 0.95,
				},
				AggregatedOutWeights: map[string]float64{
					"host-1": 1.85,
					"host-2": 2.45,
					"host-3": 2.10,
				},
				OrderedHosts: []string{"host-2", "host-3", "host-1"},
				StepResults: []v1alpha1.StepResult{
					{
						StepRef: corev1.ObjectReference{Name: "resource-weigher"},
						Activations: map[string]float64{
							"host-1": 0.15, // host-1: 1.65, host-2: 2.05, host-3: 1.70 (host-2 leads but not enough)
							"host-2": 0.85,
							"host-3": 0.75,
						},
					},
					{
						StepRef: corev1.ObjectReference{Name: "availability-filter"},
						Activations: map[string]float64{
							"host-1": 0.20, // host-1: 1.85, host-2: 2.45, host-3: 2.10 (host-2 wins decisively)
							"host-2": 0.40,
							"host-3": 0.40,
						},
					},
				},
			},
		},
	}

	previousDecision := &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-decision-1",
			Namespace: "default",
			UID:       "test-uid-1",
		},
		Spec: v1alpha1.DecisionSpec{
			Type:       v1alpha1.DecisionTypeNovaServer,
			ResourceID: "test-resource",
		},
		Status: v1alpha1.DecisionStatus{
			Result: &v1alpha1.DecisionResult{
				TargetHost: stringPtr("host-1"),
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(decision, previousDecision).
		Build()

	explainer := &Explainer{Client: client}

	explanation, err := explainer.Explain(context.Background(), decision)
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
		return
	}

	expectedParts := []string{
		"Decision #2 for this nova server",
		"Previous target host was 'host-1', now it's 'host-2'",
		"Selected: host-2 (score: 2.45), gap to 2nd: 0.35, 3 hosts evaluated",
		"Input favored host-1 (1.50), final winner: host-2 (1.20→2.45)",
		"Decision driven by 1/2 pipeline step: resource-weigher",
	}

	for _, expected := range expectedParts {
		if !contains(explanation, expected) {
			t.Errorf("Expected explanation to contain '%s', but got: %s", expected, explanation)
		}
	}
}

func TestExplainer_DeletedHostsAnalysis(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	tests := []struct {
		name             string
		decision         *v1alpha1.Decision
		expectedContains []string
	}{
		{
			name: "single host filtered by single step",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
						NormalizedInWeights: map[string]float64{
							"host-1": 1.0,
							"host-2": 2.0, // host-2 is input winner but gets filtered
						},
						StepResults: []v1alpha1.StepResult{
							{
								StepRef: corev1.ObjectReference{Name: "availability-filter"},
								Activations: map[string]float64{
									"host-1": 0.5, // Only host-1 survives
								},
							},
						},
					},
				},
			},
			expectedContains: []string{
				"Input winner host-2 was filtered by availability-filter",
			},
		},
		{
			name: "multiple hosts filtered",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
						NormalizedInWeights: map[string]float64{
							"host-1": 3.0, // host-1 is input winner and survives
							"host-2": 2.0,
							"host-3": 1.0,
						},
						StepResults: []v1alpha1.StepResult{
							{
								StepRef: corev1.ObjectReference{Name: "availability-filter"},
								Activations: map[string]float64{
									"host-1": 0.5, // Only host-1 survives
								},
							},
						},
					},
				},
			},
			expectedContains: []string{
				"2 hosts filtered",
			},
		},
		{
			name: "multiple hosts filtered including input winner",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
						NormalizedInWeights: map[string]float64{
							"host-1": 1.0,
							"host-2": 3.0, // host-2 is input winner but gets filtered
							"host-3": 2.0,
						},
						StepResults: []v1alpha1.StepResult{
							{
								StepRef: corev1.ObjectReference{Name: "availability-filter"},
								Activations: map[string]float64{
									"host-1": 0.5, // Only host-1 survives
								},
							},
						},
					},
				},
			},
			expectedContains: []string{
				"2 hosts filtered (including input winner host-2)",
			},
		},
		{
			name: "no hosts filtered",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
						NormalizedInWeights: map[string]float64{
							"host-1": 1.0,
							"host-2": 2.0,
						},
						StepResults: []v1alpha1.StepResult{
							{
								StepRef: corev1.ObjectReference{Name: "resource-weigher"},
								Activations: map[string]float64{
									"host-1": 0.5, // Both hosts survive
									"host-2": 0.3,
								},
							},
						},
					},
				},
			},
			expectedContains: []string{}, // No deleted hosts analysis should be present
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(tt.decision).
				Build()

			explainer := &Explainer{Client: client}

			explanation, err := explainer.Explain(context.Background(), tt.decision)
			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}

			for _, expected := range tt.expectedContains {
				if !contains(explanation, expected) {
					t.Errorf("Expected explanation to contain '%s', but got: %s", expected, explanation)
				}
			}

			// For the "no hosts filtered" case, ensure no deleted hosts analysis is present
			if len(tt.expectedContains) == 0 {
				deletedHostsKeywords := []string{"filtered", "Input winner", "hosts filtered"}
				for _, keyword := range deletedHostsKeywords {
					if contains(explanation, keyword) {
						t.Errorf("Expected explanation to NOT contain '%s' for no deleted hosts case, but got: %s", keyword, explanation)
					}
				}
			}
		})
	}
}

func TestExplainer_GlobalChainAnalysis(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// Create timestamps for testing durations
	baseTime := metav1.Now()
	time1 := metav1.Time{Time: baseTime.Add(-120 * time.Minute)} // 2 hours ago
	time2 := metav1.Time{Time: baseTime.Add(-60 * time.Minute)}  // 1 hour ago
	time3 := metav1.Time{Time: baseTime.Time}                    // now

	tests := []struct {
		name               string
		currentDecision    *v1alpha1.Decision
		historyDecisions   []v1alpha1.Decision
		expectedContains   []string
		expectedNotContain []string
	}{
		{
			name: "simple chain with durations",
			currentDecision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "decision-3",
					Namespace:         "default",
					CreationTimestamp: time3,
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					History: &[]corev1.ObjectReference{
						{Kind: "Decision", Namespace: "default", Name: "decision-1", UID: "uid-1"},
						{Kind: "Decision", Namespace: "default", Name: "decision-2", UID: "uid-2"},
					},
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-3"),
					},
				},
			},
			historyDecisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "decision-1",
						Namespace:         "default",
						UID:               "uid-1",
						CreationTimestamp: time1,
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-1"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "decision-2",
						Namespace:         "default",
						UID:               "uid-2",
						CreationTimestamp: time2,
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-2"),
						},
					},
				},
			},
			expectedContains: []string{
				"Chain: host-1 (1h) -> host-2 (1h) -> host-3 (0m).",
			},
		},
		{
			name: "chain with loop detection",
			currentDecision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "decision-3",
					Namespace:         "default",
					CreationTimestamp: time3,
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					History: &[]corev1.ObjectReference{
						{Kind: "Decision", Namespace: "default", Name: "decision-1", UID: "uid-1"},
						{Kind: "Decision", Namespace: "default", Name: "decision-2", UID: "uid-2"},
					},
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"), // Back to host-1 - creates loop
					},
				},
			},
			historyDecisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "decision-1",
						Namespace:         "default",
						UID:               "uid-1",
						CreationTimestamp: time1,
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-1"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "decision-2",
						Namespace:         "default",
						UID:               "uid-2",
						CreationTimestamp: time2,
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-2"),
						},
					},
				},
			},
			expectedContains: []string{
				"Chain (loop detected): host-1 (1h) -> host-2 (1h) -> host-1 (0m).",
			},
		},
		{
			name: "chain with multiple decisions on same host",
			currentDecision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "decision-4",
					Namespace:         "default",
					CreationTimestamp: time3,
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					History: &[]corev1.ObjectReference{
						{Kind: "Decision", Namespace: "default", Name: "decision-1", UID: "uid-1"},
						{Kind: "Decision", Namespace: "default", Name: "decision-2", UID: "uid-2"},
						{Kind: "Decision", Namespace: "default", Name: "decision-3", UID: "uid-3"},
					},
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-2"),
					},
				},
			},
			historyDecisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "decision-1",
						Namespace:         "default",
						UID:               "uid-1",
						CreationTimestamp: time1,
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-1"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "decision-2",
						Namespace:         "default",
						UID:               "uid-2",
						CreationTimestamp: time1, // Same time as decision-1
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-1"), // Same host as decision-1
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "decision-3",
						Namespace:         "default",
						UID:               "uid-3",
						CreationTimestamp: time2,
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-1"), // Still same host
						},
					},
				},
			},
			expectedContains: []string{
				"Chain: host-1 (2h; 3 decisions) -> host-2 (0m).",
			},
		},
		{
			name: "no chain for initial decision",
			currentDecision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "decision-1",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					History: nil, // No history
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
					},
				},
			},
			historyDecisions: []v1alpha1.Decision{},
			expectedNotContain: []string{
				"Chain:",
				"chain:",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []runtime.Object{tt.currentDecision}
			for i := range tt.historyDecisions {
				objects = append(objects, &tt.historyDecisions[i])
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				Build()

			explainer := &Explainer{Client: client}

			explanation, err := explainer.Explain(context.Background(), tt.currentDecision)
			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}

			for _, expected := range tt.expectedContains {
				if !contains(explanation, expected) {
					t.Errorf("Expected explanation to contain '%s', but got: %s", expected, explanation)
				}
			}

			for _, notExpected := range tt.expectedNotContain {
				if contains(explanation, notExpected) {
					t.Errorf("Expected explanation to NOT contain '%s', but got: %s", notExpected, explanation)
				}
			}
		})
	}
}

func intPtr(i int) *int {
	return &i
}

// TestExplainer_RawWeightsPriorityBugFix tests that the explainer correctly prioritizes
// raw weights over normalized weights to preserve small but important differences.
// This test verifies the fix for the bug where normalized weights were incorrectly preferred.
func TestExplainer_RawWeightsPriorityBugFix(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	tests := []struct {
		name             string
		decision         *v1alpha1.Decision
		expectedContains []string
		description      string
	}{
		{
			name: "raw_weights_preserve_small_differences",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-2"),
						RawInWeights: map[string]float64{
							"host-1": 1000.05, // Small but important difference
							"host-2": 1000.10, // Clear winner in raw weights
							"host-3": 1000.00,
						},
						NormalizedInWeights: map[string]float64{
							"host-1": 1.0, // Normalized weights mask the difference
							"host-2": 1.0, // All appear equal after normalization
							"host-3": 1.0,
						},
						AggregatedOutWeights: map[string]float64{
							"host-1": 1001.05,
							"host-2": 1002.10, // host-2 wins after pipeline
							"host-3": 1001.00,
						},
					},
				},
			},
			expectedContains: []string{
				"Input choice confirmed: host-2 (1000.10→1002.10)", // Should use raw weights (1000.10)
			},
			description: "Raw weights preserve small differences that normalized weights would mask",
		},
		{
			name: "raw_weights_detect_correct_input_winner",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-3"),
						RawInWeights: map[string]float64{
							"host-1": 2000.15, // Clear winner in raw weights
							"host-2": 2000.10,
							"host-3": 2000.05, // Lowest in raw weights but wins final
						},
						NormalizedInWeights: map[string]float64{
							"host-1": 1.0, // All equal after normalization
							"host-2": 1.0,
							"host-3": 1.0,
						},
						AggregatedOutWeights: map[string]float64{
							"host-1": 2001.15,
							"host-2": 2001.10,
							"host-3": 2002.05, // host-3 wins after pipeline
						},
					},
				},
			},
			expectedContains: []string{
				"Input favored host-1 (2000.15), final winner: host-3 (2000.05→2002.05)", // Should detect host-1 as input winner using raw weights
			},
			description: "Raw weights correctly identify input winner that normalized weights would miss",
		},
		{
			name: "critical_steps_analysis_uses_raw_weights",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
						RawInWeights: map[string]float64{
							"host-1": 1000.05, // Slight advantage in raw weights
							"host-2": 1000.00,
						},
						NormalizedInWeights: map[string]float64{
							"host-1": 1.0, // Equal after normalization
							"host-2": 1.0,
						},
						StepResults: []v1alpha1.StepResult{
							{
								StepRef: corev1.ObjectReference{Name: "resource-weigher"},
								Activations: map[string]float64{
									"host-1": 0.5, // Small boost makes host-1 clear winner
									"host-2": 0.0,
								},
							},
						},
					},
				},
			},
			expectedContains: []string{
				"Decision driven by input only (all 1 steps are non-critical)", // With small raw weight advantage, step is non-critical
				"Input choice confirmed: host-1 (1000.05→0.00)",                // Shows raw weights are being used
			},
			description: "Critical steps analysis uses raw weights - with small raw advantage, step becomes non-critical",
		},
		{
			name: "deleted_hosts_analysis_uses_raw_weights",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
						RawInWeights: map[string]float64{
							"host-1": 1000.00,
							"host-2": 1000.05, // Slight winner in raw weights
							"host-3": 999.95,
						},
						NormalizedInWeights: map[string]float64{
							"host-1": 1.0, // All equal after normalization
							"host-2": 1.0,
							"host-3": 1.0,
						},
						StepResults: []v1alpha1.StepResult{
							{
								StepRef: corev1.ObjectReference{Name: "availability-filter"},
								Activations: map[string]float64{
									"host-1": 0.0, // Only host-1 survives
								},
							},
						},
					},
				},
			},
			expectedContains: []string{
				"2 hosts filtered (including input winner host-2)",                    // Shows raw weights are used to identify input winner
				"Input favored host-2 (1000.05), final winner: host-1 (1000.00→0.00)", // Shows raw weights in input comparison
			},
			description: "Deleted hosts analysis uses raw weights to correctly identify input winner",
		},
		{
			name: "fallback_to_normalized_when_no_raw_weights",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:       v1alpha1.DecisionTypeNovaServer,
					ResourceID: "test-resource",
				},
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						TargetHost:   stringPtr("host-1"),
						RawInWeights: nil, // No raw weights available
						NormalizedInWeights: map[string]float64{
							"host-1": 1.5, // Should fall back to normalized
							"host-2": 1.0,
							"host-3": 0.8,
						},
						AggregatedOutWeights: map[string]float64{
							"host-1": 2.5,
							"host-2": 2.0,
							"host-3": 1.8,
						},
					},
				},
			},
			expectedContains: []string{
				"Input choice confirmed: host-1 (1.50→2.50)", // Should use normalized weights as fallback
			},
			description: "Should fall back to normalized weights when raw weights are not available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Test description: %s", tt.description)

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(tt.decision).
				Build()

			explainer := &Explainer{Client: client}

			explanation, err := explainer.Explain(context.Background(), tt.decision)
			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}

			for _, expected := range tt.expectedContains {
				if !contains(explanation, expected) {
					t.Errorf("Expected explanation to contain '%s', but got: %s", expected, explanation)
				}
			}

			t.Logf("✅ Test passed: %s", explanation)
		})
	}
}

// TestExplainer_RawVsNormalizedComparison demonstrates the impact of the bug fix
func TestExplainer_RawVsNormalizedComparison(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// This test demonstrates what would happen with the old (buggy) behavior
	// vs the new (correct) behavior
	decision := &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-decision",
			Namespace: "default",
		},
		Spec: v1alpha1.DecisionSpec{
			Type:       v1alpha1.DecisionTypeNovaServer,
			ResourceID: "test-resource",
		},
		Status: v1alpha1.DecisionStatus{
			Result: &v1alpha1.DecisionResult{
				TargetHost: stringPtr("host-2"),
				RawInWeights: map[string]float64{
					"host-1": 1000.05, // Very small difference
					"host-2": 1000.10, // Slightly higher - should be detected as input winner
					"host-3": 1000.00,
				},
				NormalizedInWeights: map[string]float64{
					"host-1": 1.0, // All normalized to same value - would mask the difference
					"host-2": 1.0,
					"host-3": 1.0,
				},
				AggregatedOutWeights: map[string]float64{
					"host-1": 1001.05,
					"host-2": 1002.10, // host-2 wins
					"host-3": 1001.00,
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(decision).
		Build()

	explainer := &Explainer{Client: client}
	explanation, err := explainer.Explain(context.Background(), decision)
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
		return
	}

	// With the fix, should correctly identify host-2 as input winner using raw weights
	if !contains(explanation, "Input choice confirmed: host-2 (1000.10→1002.10)") {
		t.Errorf("Expected explanation to show raw weight value (1000.10), but got: %s", explanation)
	}

	// Should NOT show any indication that input choice was overridden
	if contains(explanation, "Input favored host-1") || contains(explanation, "Input favored host-3") {
		t.Errorf("Expected explanation to NOT show input choice override, but got: %s", explanation)
	}

	t.Logf("✅ Correctly identified host-2 as input winner using raw weights (1000.10)")
	t.Logf("✅ Small but important difference preserved (0.05 difference between host-1 and host-2)")
	t.Logf("Explanation: %s", explanation)
}
