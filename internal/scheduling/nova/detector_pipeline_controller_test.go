// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockDetectorCycleBreaker struct{}

func (m *mockDetectorCycleBreaker) Filter(ctx context.Context, decisions []plugins.VMDetection) ([]plugins.VMDetection, error) {
	return decisions, nil
}

type mockControllerStep struct{}

func (m *mockControllerStep) Run() ([]plugins.VMDetection, error) {
	return nil, nil
}
func (m *mockControllerStep) Validate(ctx context.Context, params v1alpha1.Parameters) error {
	return nil
}
func (m *mockControllerStep) Init(ctx context.Context, client client.Client, step v1alpha1.DetectorSpec) error {
	return nil
}

func TestDetectorPipelineController_InitPipeline(t *testing.T) {
	tests := []struct {
		name                   string
		steps                  []v1alpha1.DetectorSpec
		expectNonCriticalError bool
	}{
		{
			name: "successful pipeline initialization",
			steps: []v1alpha1.DetectorSpec{
				{
					Name: "mock-step",
				},
			},
			expectNonCriticalError: false,
		},
		{
			name: "unsupported step",
			steps: []v1alpha1.DetectorSpec{
				{
					Name: "unsupported",
				},
			},
			expectNonCriticalError: true,
		},
		{
			name:                   "empty steps",
			steps:                  []v1alpha1.DetectorSpec{},
			expectNonCriticalError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := &DetectorPipelineController{
				Monitor: lib.NewDetectorPipelineMonitor(),
				Breaker: &mockDetectorCycleBreaker{},
			}

			pipeline := lib.DetectorPipeline[plugins.VMDetection]{
				Breaker: controller.Breaker,
				Monitor: controller.Monitor,
			}
			errs := pipeline.Init(t.Context(), tt.steps, map[string]lib.Detector[plugins.VMDetection]{
				"mock-step": &mockControllerStep{},
			})

			if tt.expectNonCriticalError {
				if len(errs) == 0 {
					t.Errorf("expected non-critical error, got none")
				}
			} else {
				if len(errs) > 0 {
					t.Errorf("unexpected non-critical error: %v", errs)
				}
			}

			if pipeline.Breaker != controller.Breaker {
				t.Error("expected pipeline to have cycle detector set")
			}

			if pipeline.Monitor != controller.Monitor {
				t.Error("expected pipeline to have monitor set")
			}
		})
	}
}

func TestDetectorPipelineController_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	err := v1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	controller := &DetectorPipelineController{
		BasePipelineController: lib.BasePipelineController[*lib.DetectorPipeline[plugins.VMDetection]]{
			Client: client,
		},
	}

	req := ctrl.Request{}
	result, err := controller.Reconcile(t.Context(), req)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if result.RequeueAfter > 0 {
		t.Error("expected no requeue")
	}
}
