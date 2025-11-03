// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package machines

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/scheduling/api/delegation/ironcore"
	"github.com/cobaltcore-dev/cortex/scheduling/api/delegation/ironcore/v1alpha1"
	schedulingv1alpha1 "github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDecisionPipelineController_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := schedulingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheduling scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add ironcore scheme: %v", err)
	}

	tests := []struct {
		name              string
		decision          *schedulingv1alpha1.Decision
		machinePools      []v1alpha1.MachinePool
		machine           *v1alpha1.Machine
		expectError       bool
		expectDecision    bool
		expectTargetHost  string
		expectMachinePool string
	}{
		{
			name: "successful machine decision processing",
			decision: &schedulingv1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-decision",
				},
				Spec: schedulingv1alpha1.DecisionSpec{
					Operator:   "test-operator",
					Type:       schedulingv1alpha1.DecisionTypeIroncoreMachine,
					ResourceID: "test-machine",
					PipelineRef: corev1.ObjectReference{
						Name: "machines-scheduler",
					},
					MachineRef: &corev1.ObjectReference{
						Name:      "test-machine",
						Namespace: "default",
					},
				},
			},
			machinePools: []v1alpha1.MachinePool{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pool1"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pool2"},
				},
			},
			machine: &v1alpha1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
				},
				Spec: v1alpha1.MachineSpec{
					Scheduler: "",
				},
			},
			expectError:       false,
			expectDecision:    true,
			expectTargetHost:  "pool1", // NoopFilter returns first pool
			expectMachinePool: "pool1",
		},
		{
			name: "no machine pools available",
			decision: &schedulingv1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-decision-no-pools",
				},
				Spec: schedulingv1alpha1.DecisionSpec{
					Operator:   "test-operator",
					Type:       schedulingv1alpha1.DecisionTypeIroncoreMachine,
					ResourceID: "test-machine",
					PipelineRef: corev1.ObjectReference{
						Name: "machines-scheduler",
					},
					MachineRef: &corev1.ObjectReference{
						Name:      "test-machine",
						Namespace: "default",
					},
				},
			},
			machinePools:   []v1alpha1.MachinePool{},
			expectError:    false,
			expectDecision: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []runtime.Object{tt.decision}
			for i := range tt.machinePools {
				objects = append(objects, &tt.machinePools[i])
			}
			if tt.machine != nil {
				objects = append(objects, tt.machine)
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&schedulingv1alpha1.Decision{}).
				Build()

			controller := &DecisionPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.Pipeline[ironcore.MachinePipelineRequest]]{
					Pipelines: map[string]lib.Pipeline[ironcore.MachinePipelineRequest]{
						"machines-scheduler": createMockPipeline(),
					},
				},
				Conf: conf.Config{
					Operator: "test-operator",
				},
				Monitor: lib.PipelineMonitor{},
			}
			controller.Client = client

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name: tt.decision.Name,
				},
			}

			result, err := controller.Reconcile(context.Background(), req)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
				return
			}

			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
				return
			}

			if result.RequeueAfter > 0 {
				t.Errorf("unexpected requeue: %v", result.RequeueAfter)
			}

			// Verify decision status if expected
			if tt.expectDecision {
				var updatedDecision schedulingv1alpha1.Decision
				err := client.Get(context.Background(), req.NamespacedName, &updatedDecision)
				if err != nil {
					t.Errorf("Failed to get updated decision: %v", err)
					return
				}

				if updatedDecision.Status.Result == nil {
					t.Error("expected decision result to be set")
					return
				}

				if updatedDecision.Status.Result.TargetHost == nil {
					t.Error("expected target host to be set")
					return
				}

				if *updatedDecision.Status.Result.TargetHost != tt.expectTargetHost {
					t.Errorf("expected target host %q, got %q", tt.expectTargetHost, *updatedDecision.Status.Result.TargetHost)
				}

				if updatedDecision.Status.Took.Duration <= 0 {
					t.Error("expected took duration to be positive")
				}

				// Verify machine was updated with machine pool ref
				if tt.machine != nil {
					var updatedMachine v1alpha1.Machine
					err := client.Get(context.Background(), types.NamespacedName{
						Name:      tt.machine.Name,
						Namespace: tt.machine.Namespace,
					}, &updatedMachine)
					if err != nil {
						t.Errorf("Failed to get updated machine: %v", err)
						return
					}

					if updatedMachine.Spec.MachinePoolRef == nil {
						t.Error("expected machine pool ref to be set")
						return
					}

					if updatedMachine.Spec.MachinePoolRef.Name != tt.expectMachinePool {
						t.Errorf("expected machine pool %q, got %q", tt.expectMachinePool, updatedMachine.Spec.MachinePoolRef.Name)
					}
				}
			}
		})
	}
}

func TestDecisionPipelineController_InitPipeline(t *testing.T) {
	controller := &DecisionPipelineController{
		Monitor: lib.PipelineMonitor{},
	}

	tests := []struct {
		name        string
		steps       []schedulingv1alpha1.Step
		expectError bool
	}{
		{
			name:        "empty steps",
			steps:       []schedulingv1alpha1.Step{},
			expectError: false,
		},
		{
			name: "noop step",
			steps: []schedulingv1alpha1.Step{
				{
					Spec: schedulingv1alpha1.StepSpec{
						Impl: "noop",
						Type: schedulingv1alpha1.StepTypeFilter,
					},
				},
			},
			expectError: false,
		},
		{
			name: "unsupported step",
			steps: []schedulingv1alpha1.Step{
				{
					Spec: schedulingv1alpha1.StepSpec{
						Impl: "unsupported",
						Type: schedulingv1alpha1.StepTypeFilter,
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline, err := controller.InitPipeline(t.Context(), tt.steps)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
				return
			}

			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
				return
			}

			if !tt.expectError && pipeline == nil {
				t.Error("expected pipeline to be non-nil")
			}
		})
	}
}

// Helper function to create a mock pipeline that works with the ironcore types
func createMockPipeline() lib.Pipeline[ironcore.MachinePipelineRequest] {
	return &mockMachinePipeline{}
}

type mockMachinePipeline struct{}

func (m *mockMachinePipeline) Run(request ironcore.MachinePipelineRequest) (schedulingv1alpha1.DecisionResult, error) {
	if len(request.Pools) == 0 {
		return schedulingv1alpha1.DecisionResult{}, nil
	}

	// Return the first pool as the target host
	targetHost := request.Pools[0].Name
	return schedulingv1alpha1.DecisionResult{
		TargetHost: &targetHost,
	}, nil
}

func (m *mockMachinePipeline) Deinit(ctx context.Context) error {
	return nil
}
