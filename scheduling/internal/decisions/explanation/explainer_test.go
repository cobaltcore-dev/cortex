// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package explanation

import (
	"context"
	"testing"

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
