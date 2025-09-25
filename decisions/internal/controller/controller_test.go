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
			Input: map[string]float64{},
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

	t.Logf("Reconcile completed successfully: description=%s", resource.Status.Description)
}
