// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/descheduling/nova/plugins"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockCycleBreaker struct{}

func (m *mockCycleBreaker) Init(ctx context.Context, client client.Client, conf conf.Config) error {
	return nil
}

func (m *mockCycleBreaker) Filter(ctx context.Context, decisions []plugins.VMDetection) ([]plugins.VMDetection, error) {
	return decisions, nil
}

type mockControllerStep struct{}

func (m *mockControllerStep) Run() ([]plugins.VMDetection, error) {
	return nil, nil
}
func (m *mockControllerStep) Init(ctx context.Context, client client.Client, step v1alpha1.DetectorSpec) error {
	return nil
}

func TestDeschedulingsPipelineController_InitPipeline(t *testing.T) {
	tests := []struct {
		name                   string
		steps                  []v1alpha1.DetectorSpec
		expectNonCriticalError bool
		expectCriticalError    bool
	}{
		{
			name: "successful pipeline initialization",
			steps: []v1alpha1.DetectorSpec{
				{
					Name: "mock-step",
				},
			},
			expectNonCriticalError: false,
			expectCriticalError:    false,
		},
		{
			name: "unsupported step",
			steps: []v1alpha1.DetectorSpec{
				{
					Name: "unsupported",
				},
			},
			expectNonCriticalError: true,
			expectCriticalError:    false,
		},
		{
			name:                   "empty steps",
			steps:                  []v1alpha1.DetectorSpec{},
			expectNonCriticalError: false,
			expectCriticalError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := &DeschedulingsPipelineController{
				Monitor:      lib.NewDetectorPipelineMonitor(),
				CycleBreaker: &mockCycleBreaker{},
			}

			pipeline := lib.DetectorPipeline[plugins.VMDetection]{
				CycleBreaker: controller.CycleBreaker,
				Monitor:      controller.Monitor,
			}
			nonCriticalErr, criticalErr := pipeline.Init(t.Context(), tt.steps, map[string]lib.Detector[plugins.VMDetection]{
				"mock-step": &mockControllerStep{},
			})

			if tt.expectCriticalError {
				if criticalErr == nil {
					t.Errorf("expected critical error, got none")
				}
			} else {
				if criticalErr != nil {
					t.Errorf("unexpected critical error: %v", criticalErr)
				}
			}

			if tt.expectNonCriticalError {
				if nonCriticalErr == nil {
					t.Errorf("expected non-critical error, got none")
				}
			} else {
				if nonCriticalErr != nil {
					t.Errorf("unexpected non-critical error: %v", nonCriticalErr)
				}
			}

			if pipeline.CycleBreaker != controller.CycleBreaker {
				t.Error("expected pipeline to have cycle detector set")
			}

			if pipeline.Monitor != controller.Monitor {
				t.Error("expected pipeline to have monitor set")
			}
		})
	}
}

func TestDeschedulingsPipelineController_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	err := v1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	controller := &DeschedulingsPipelineController{
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
