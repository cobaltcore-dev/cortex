// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package explanation

import (
	"context"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestController_shouldReconcileDecision(t *testing.T) {
	controller := &Controller{
		SchedulingDomain: v1alpha1.SchedulingDomainNova,
	}

	tests := []struct {
		name     string
		decision *v1alpha1.Decision
		expected bool
	}{
		{
			name: "should reconcile nova decision without explanation",
			decision: &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.DecisionStatus{
					Explanation: "",
					Result: &v1alpha1.DecisionResult{
						TargetHost: stringPtr("host-1"),
					},
				},
			},
			expected: true,
		},
		{
			name: "should not reconcile decision from different operator",
			decision: &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: "different-operator",
				},
				Status: v1alpha1.DecisionStatus{
					Explanation: "",
				},
			},
			expected: false,
		},
		{
			name: "should not reconcile decision with existing explanation",
			decision: &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.DecisionStatus{
					Explanation: "Already has explanation",
				},
			},
			expected: false,
		},
		{
			name: "should not reconcile non-nova decision",
			decision: &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.DecisionStatus{
					Explanation: "",
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := controller.shouldReconcileDecision(tt.decision)
			if result != tt.expected {
				t.Errorf("shouldReconcileDecision() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestController_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	tests := []struct {
		name                  string
		decision              *v1alpha1.Decision
		existingDecisions     []v1alpha1.Decision
		expectError           bool
		expectRequeue         bool
		expectedExplanation   string
		expectedHistoryLength int
	}{
		{
			name: "decision not found",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nonexistent-decision",
					Namespace: "default",
				},
			},
			expectError: false, // controller-runtime ignores not found errors
		},
		{
			name: "reconcile decision without history",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					ResourceID:       "test-resource-1",
				},
				Status: v1alpha1.DecisionStatus{},
			},
			expectedExplanation:   "Initial placement of the nova server",
			expectedHistoryLength: 0,
		},
		{
			name: "reconcile decision with history",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-decision-2",
					Namespace:         "default",
					CreationTimestamp: metav1.Time{Time: time.Now().Add(time.Hour)},
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					ResourceID:       "test-resource-2",
				},
				Status: v1alpha1.DecisionStatus{},
			},
			existingDecisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-decision-1",
						Namespace:         "default",
						CreationTimestamp: metav1.Time{Time: time.Now()},
					},
					Spec: v1alpha1.DecisionSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						ResourceID:       "test-resource-2",
					},
					Status: v1alpha1.DecisionStatus{
						Result: &v1alpha1.DecisionResult{
							TargetHost: stringPtr("host-1"),
						},
					},
				},
			},
			expectedHistoryLength: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []runtime.Object
			if tt.name != "decision not found" {
				objects = append(objects, tt.decision)
			}
			for i := range tt.existingDecisions {
				objects = append(objects, &tt.existingDecisions[i])
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&v1alpha1.Decision{}).
				Build()

			controller := &Controller{
				Client:           client,
				SchedulingDomain: v1alpha1.SchedulingDomainNova,
				SkipIndexFields:  true, // Skip field indexing for testing
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.decision.Name,
					Namespace: tt.decision.Namespace,
				},
			}

			result, err := controller.Reconcile(context.Background(), req)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
				return
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}

			if tt.expectRequeue && result.RequeueAfter == 0 {
				t.Errorf("Expected requeue but got none")
			}
			if !tt.expectRequeue && result.RequeueAfter > 0 {
				t.Errorf("Expected no requeue but got %v", result.RequeueAfter)
			}

			// Only check results if we expect the decision to exist
			if tt.name != "decision not found" {
				// Verify the decision was updated
				var updated v1alpha1.Decision
				err = client.Get(context.Background(), req.NamespacedName, &updated)
				if err != nil {
					t.Errorf("Failed to get updated decision: %v", err)
					return
				}

				if tt.expectedExplanation != "" && !contains(updated.Status.Explanation, tt.expectedExplanation) {
					t.Errorf("Expected explanation to contain '%s', but got: %s", tt.expectedExplanation, updated.Status.Explanation)
				}

				if updated.Status.History != nil && len(*updated.Status.History) != tt.expectedHistoryLength {
					t.Errorf("Expected history length %d, got %d", tt.expectedHistoryLength, len(*updated.Status.History))
				}
			}
		})
	}
}

func TestController_reconcileHistory(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	tests := []struct {
		name              string
		decision          *v1alpha1.Decision
		existingDecisions []v1alpha1.Decision
		expectedHistory   int
		expectError       bool
	}{
		{
			name: "no previous decisions",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					ResourceID: "test-resource-1",
				},
			},
			expectedHistory: 0,
		},
		{
			name: "one previous decision",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-decision-2",
					Namespace:         "default",
					CreationTimestamp: metav1.Time{Time: time.Now().Add(time.Hour)},
				},
				Spec: v1alpha1.DecisionSpec{
					ResourceID: "test-resource-2",
				},
			},
			existingDecisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-decision-1",
						Namespace:         "default",
						CreationTimestamp: metav1.Time{Time: time.Now()},
					},
					Spec: v1alpha1.DecisionSpec{
						ResourceID: "test-resource-2",
					},
				},
			},
			expectedHistory: 1,
		},
		{
			name: "multiple previous decisions in correct order",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-decision-3",
					Namespace:         "default",
					CreationTimestamp: metav1.Time{Time: time.Now().Add(2 * time.Hour)},
				},
				Spec: v1alpha1.DecisionSpec{
					ResourceID: "test-resource-3",
				},
			},
			existingDecisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-decision-1",
						Namespace:         "default",
						CreationTimestamp: metav1.Time{Time: time.Now()},
					},
					Spec: v1alpha1.DecisionSpec{
						ResourceID: "test-resource-3",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-decision-2",
						Namespace:         "default",
						CreationTimestamp: metav1.Time{Time: time.Now().Add(time.Hour)},
					},
					Spec: v1alpha1.DecisionSpec{
						ResourceID: "test-resource-3",
					},
				},
			},
			expectedHistory: 2,
		},
		{
			name: "exclude future decisions",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-decision-2",
					Namespace:         "default",
					CreationTimestamp: metav1.Time{Time: time.Now().Add(time.Hour)},
				},
				Spec: v1alpha1.DecisionSpec{
					ResourceID: "test-resource-4",
				},
			},
			existingDecisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-decision-1",
						Namespace:         "default",
						CreationTimestamp: metav1.Time{Time: time.Now()},
					},
					Spec: v1alpha1.DecisionSpec{
						ResourceID: "test-resource-4",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-decision-3",
						Namespace:         "default",
						CreationTimestamp: metav1.Time{Time: time.Now().Add(2 * time.Hour)},
					},
					Spec: v1alpha1.DecisionSpec{
						ResourceID: "test-resource-4",
					},
				},
			},
			expectedHistory: 1, // Only test-decision-1 should be included
		},
		{
			name: "exclude decisions with different ResourceID",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-decision-target",
					Namespace:         "default",
					CreationTimestamp: metav1.Time{Time: time.Now().Add(time.Hour)},
				},
				Spec: v1alpha1.DecisionSpec{
					ResourceID: "target-resource",
				},
			},
			existingDecisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-decision-same",
						Namespace:         "default",
						CreationTimestamp: metav1.Time{Time: time.Now()},
					},
					Spec: v1alpha1.DecisionSpec{
						ResourceID: "target-resource",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-decision-different",
						Namespace:         "default",
						CreationTimestamp: metav1.Time{Time: time.Now()},
					},
					Spec: v1alpha1.DecisionSpec{
						ResourceID: "different-resource",
					},
				},
			},
			expectedHistory: 1, // Only same ResourceID should be included
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
				WithStatusSubresource(&v1alpha1.Decision{}).
				Build()

			controller := &Controller{
				Client:           client,
				SchedulingDomain: v1alpha1.SchedulingDomainNova,
				SkipIndexFields:  true, // Skip field indexing for testing
			}

			err := controller.reconcileHistory(context.Background(), tt.decision)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
				return
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}

			if tt.decision.Status.History == nil {
				if tt.expectedHistory != 0 {
					t.Errorf("Expected history length %d, got nil", tt.expectedHistory)
				}
			} else if len(*tt.decision.Status.History) != tt.expectedHistory {
				t.Errorf("Expected history length %d, got %d", tt.expectedHistory, len(*tt.decision.Status.History))
			}
		})
	}
}

func TestController_reconcileExplanation(t *testing.T) {
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
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			ResourceID:       "test-resource",
		},
		Status: v1alpha1.DecisionStatus{
			History: nil,
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(decision).
		WithStatusSubresource(&v1alpha1.Decision{}).
		Build()

	controller := &Controller{
		Client:           client,
		SchedulingDomain: v1alpha1.SchedulingDomainNova,
	}

	err := controller.reconcileExplanation(context.Background(), decision)
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}

	if decision.Status.Explanation == "" {
		t.Error("Expected explanation to be set but it was empty")
	}

	if !contains(decision.Status.Explanation, "Initial placement of the nova server") {
		t.Errorf("Expected explanation to contain nova server text, got: %s", decision.Status.Explanation)
	}
}

func TestController_StartupCallback(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	// Create a decision that should be reconciled
	decision1 := &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-decision-1",
			Namespace: "default",
		},
		Spec: v1alpha1.DecisionSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			ResourceID:       "test-resource-1",
		},
		Status: v1alpha1.DecisionStatus{
			Explanation: "", // Empty explanation means it should be reconciled
			Result: &v1alpha1.DecisionResult{
				TargetHost: stringPtr("host-1"),
			},
		},
	}

	// Create a decision that should not be reconciled (already has explanation)
	decision2 := &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-decision-2",
			Namespace: "default",
		},
		Spec: v1alpha1.DecisionSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			ResourceID:       "test-resource-2",
		},
		Status: v1alpha1.DecisionStatus{
			Explanation: "Already has explanation",
		},
	}

	// Create a decision from different operator that should not be reconciled
	decision3 := &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-decision-3",
			Namespace: "default",
		},
		Spec: v1alpha1.DecisionSpec{
			SchedulingDomain: "different-operator",
			ResourceID:       "test-resource-3",
		},
		Status: v1alpha1.DecisionStatus{
			Explanation: "",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(decision1, decision2, decision3).
		WithStatusSubresource(&v1alpha1.Decision{}).
		Build()

	controller := &Controller{
		Client:           client,
		SchedulingDomain: v1alpha1.SchedulingDomainNova,
		SkipIndexFields:  true, // Skip field indexing for testing
	}

	err := controller.StartupCallback(context.Background())
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}

	// Verify that decision1 now has an explanation
	var updated1 v1alpha1.Decision
	err = client.Get(context.Background(), types.NamespacedName{Name: "test-decision-1", Namespace: "default"}, &updated1)
	if err != nil {
		t.Errorf("Failed to get updated decision1: %v", err)
	}

	if updated1.Status.Explanation == "" {
		t.Error("Expected decision1 to have explanation after startup callback")
	}

	// Verify that decision2 explanation remains unchanged
	var updated2 v1alpha1.Decision
	err = client.Get(context.Background(), types.NamespacedName{Name: "test-decision-2", Namespace: "default"}, &updated2)
	if err != nil {
		t.Errorf("Failed to get updated decision2: %v", err)
	}

	if updated2.Status.Explanation != "Already has explanation" {
		t.Errorf("Expected decision2 explanation to remain unchanged, got: %s", updated2.Status.Explanation)
	}

	// Verify that decision3 explanation remains empty (different operator)
	var updated3 v1alpha1.Decision
	err = client.Get(context.Background(), types.NamespacedName{Name: "test-decision-3", Namespace: "default"}, &updated3)
	if err != nil {
		t.Errorf("Failed to get updated decision3: %v", err)
	}

	if updated3.Status.Explanation != "" {
		t.Errorf("Expected decision3 explanation to remain empty, got: %s", updated3.Status.Explanation)
	}
}
