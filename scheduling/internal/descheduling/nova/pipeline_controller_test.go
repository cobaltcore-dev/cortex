// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"testing"

	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/descheduling/nova/plugins"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockCycleDetector struct{}

func (m *mockCycleDetector) Filter(ctx context.Context, decisions []plugins.Decision) ([]plugins.Decision, error) {
	return decisions, nil
}

type mockControllerStep struct{}

func (m *mockControllerStep) GetName() string { return "mock-step" }
func (m *mockControllerStep) Run() ([]plugins.Decision, error) {
	return nil, nil
}
func (m *mockControllerStep) Init(db db.DB, opts libconf.RawOpts) error { return nil }

func TestDeschedulingsPipelineController_InitPipeline(t *testing.T) {
	tests := []struct {
		name          string
		steps         []v1alpha1.Step
		expectError   bool
		expectedError string
	}{
		{
			name: "successful pipeline initialization",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-step",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeDescheduler,
						Impl: "mock-step",
					},
				},
			},
			expectError: false,
		},
		{
			name: "unsupported step",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "unsupported-step",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeDescheduler,
						Impl: "unsupported",
					},
				},
			},
			expectError:   true,
			expectedError: "descheduler: unsupported step: unsupported",
		},
		{
			name:        "empty steps",
			steps:       []v1alpha1.Step{},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := &DeschedulingsPipelineController{
				DB:            db.DB{},
				Monitor:       NewPipelineMonitor(),
				CycleDetector: &mockCycleDetector{},
			}

			// Override supportedSteps for testing
			testSupportedSteps := []Step{&mockControllerStep{}}

			pipeline := Pipeline{
				CycleDetector: controller.CycleDetector,
				Monitor:       controller.Monitor,
			}
			err := pipeline.Init(tt.steps, testSupportedSteps, controller.DB)

			if tt.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
				if tt.expectedError != "" && err.Error() != tt.expectedError {
					t.Errorf("expected error %q, got %q", tt.expectedError, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if pipeline.CycleDetector != controller.CycleDetector {
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
		BasePipelineController: lib.BasePipelineController[Pipeline]{
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

func TestDeschedulingsPipelineController_SetupWithManager(t *testing.T) {
	scheme := runtime.NewScheme()
	err := v1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	controller := &DeschedulingsPipelineController{
		BasePipelineController: lib.BasePipelineController[Pipeline]{
			Client: client,
		},
		Conf: conf.Config{
			Operator: "test-operator",
		},
	}

	// Test that SetupWithManager method exists by calling it with nil manager
	// This will panic with nil pointer dereference, so we recover and verify
	defer func() {
		if r := recover(); r == nil {
			t.Error("SetupWithManager should panic when called with nil manager")
		}
	}()

	err = controller.SetupWithManager(nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
