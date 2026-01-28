// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package machines

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/delegation/ironcore"
	ironcorev1alpha1 "github.com/cobaltcore-dev/cortex/api/delegation/ironcore/v1alpha1"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFilterWeigherPipelineController_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheduling scheme: %v", err)
	}
	if err := ironcorev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add ironcore scheme: %v", err)
	}

	tests := []struct {
		name              string
		decision          *v1alpha1.Decision
		machinePools      []ironcorev1alpha1.MachinePool
		machine           *ironcorev1alpha1.Machine
		expectError       bool
		expectDecision    bool
		expectTargetHost  string
		expectMachinePool string
	}{
		{
			name: "successful machine decision processing",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-decision",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainMachines,
					ResourceID:       "test-machine",
					PipelineRef: corev1.ObjectReference{
						Name: "machines-scheduler",
					},
					MachineRef: &corev1.ObjectReference{
						Name:      "test-machine",
						Namespace: "default",
					},
				},
			},
			machinePools: []ironcorev1alpha1.MachinePool{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pool1"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pool2"},
				},
			},
			machine: &ironcorev1alpha1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
				},
				Spec: ironcorev1alpha1.MachineSpec{
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
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-decision-no-pools",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainMachines,
					ResourceID:       "test-machine",
					PipelineRef: corev1.ObjectReference{
						Name: "machines-scheduler",
					},
					MachineRef: &corev1.ObjectReference{
						Name:      "test-machine",
						Namespace: "default",
					},
				},
			},
			machinePools:   []ironcorev1alpha1.MachinePool{},
			expectError:    true,
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
				WithStatusSubresource(&v1alpha1.Decision{}).
				Build()

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[ironcore.MachinePipelineRequest]]{
					Pipelines: map[string]lib.FilterWeigherPipeline[ironcore.MachinePipelineRequest]{
						"machines-scheduler": createMockPipeline(),
					},
				},
				Conf: conf.Config{
					SchedulingDomain: v1alpha1.SchedulingDomainMachines,
				},
				Monitor: lib.FilterWeigherPipelineMonitor{},
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
				var updatedDecision v1alpha1.Decision
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

				// Verify machine was updated with machine pool ref
				if tt.machine != nil {
					var updatedMachine ironcorev1alpha1.Machine
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

func TestFilterWeigherPipelineController_InitPipeline(t *testing.T) {
	controller := &FilterWeigherPipelineController{
		Monitor: lib.FilterWeigherPipelineMonitor{},
	}

	tests := []struct {
		name                   string
		filters                []v1alpha1.FilterSpec
		weighers               []v1alpha1.WeigherSpec
		expectNonCriticalError bool
		expectCriticalError    bool
	}{
		{
			name:                   "empty steps",
			filters:                []v1alpha1.FilterSpec{},
			weighers:               []v1alpha1.WeigherSpec{},
			expectNonCriticalError: false,
			expectCriticalError:    false,
		},
		{
			name: "noop step",
			filters: []v1alpha1.FilterSpec{
				{Name: "noop"},
			},
			expectNonCriticalError: false,
			expectCriticalError:    false,
		},
		{
			name: "unsupported step",
			filters: []v1alpha1.FilterSpec{
				{Name: "unsupported"},
			},
			expectNonCriticalError: false,
			expectCriticalError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initResult := controller.InitPipeline(t.Context(), v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainMachines,
					Filters:          tt.filters,
					Weighers:         tt.weighers,
				},
			})

			if tt.expectCriticalError && len(initResult.FilterErrors) == 0 {
				t.Error("Expected critical error but got none")
			}
			if !tt.expectCriticalError && len(initResult.FilterErrors) > 0 {
				t.Errorf("Expected no critical error but got: %v", initResult.FilterErrors)
			}
			if tt.expectNonCriticalError && len(initResult.WeigherErrors) == 0 {
				t.Error("Expected non-critical error but got none")
			}
			if !tt.expectNonCriticalError && len(initResult.WeigherErrors) > 0 {
				t.Errorf("Expected no non-critical error but got: %v", initResult.WeigherErrors)
			}
		})
	}
}

func TestFilterWeigherPipelineController_ProcessNewMachine(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheduling scheme: %v", err)
	}
	if err := ironcorev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add ironcore scheme: %v", err)
	}

	tests := []struct {
		name                      string
		machine                   *ironcorev1alpha1.Machine
		machinePools              []ironcorev1alpha1.MachinePool
		pipelineConfig            *v1alpha1.Pipeline
		createDecisions           bool
		expectError               bool
		expectDecisionCreated     bool
		expectMachinePoolAssigned bool
		expectTargetHost          string
	}{
		{
			name: "successful machine processing with decision creation",
			machine: &ironcorev1alpha1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
				},
				Spec: ironcorev1alpha1.MachineSpec{
					Scheduler: "",
				},
			},
			machinePools: []ironcorev1alpha1.MachinePool{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pool1"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pool2"},
				},
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "machines-scheduler",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainMachines,
					CreateDecisions:  true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			createDecisions:           true,
			expectError:               false,
			expectDecisionCreated:     true,
			expectMachinePoolAssigned: true,
			expectTargetHost:          "pool1",
		},
		{
			name: "successful machine processing without decision creation",
			machine: &ironcorev1alpha1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine-no-decision",
					Namespace: "default",
				},
				Spec: ironcorev1alpha1.MachineSpec{
					Scheduler: "",
				},
			},
			machinePools: []ironcorev1alpha1.MachinePool{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pool1"},
				},
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "machines-scheduler",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainMachines,
					CreateDecisions:  false,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			createDecisions:           false,
			expectError:               false,
			expectDecisionCreated:     false,
			expectMachinePoolAssigned: true,
			expectTargetHost:          "pool1",
		},
		{
			name: "pipeline not configured",
			machine: &ironcorev1alpha1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine-no-pipeline",
					Namespace: "default",
				},
				Spec: ironcorev1alpha1.MachineSpec{
					Scheduler: "",
				},
			},
			machinePools:              []ironcorev1alpha1.MachinePool{},
			pipelineConfig:            nil,
			expectError:               true,
			expectDecisionCreated:     false,
			expectMachinePoolAssigned: false,
		},
		{
			name: "no machine pools available",
			machine: &ironcorev1alpha1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine-no-pools",
					Namespace: "default",
				},
				Spec: ironcorev1alpha1.MachineSpec{
					Scheduler: "",
				},
			},
			machinePools: []ironcorev1alpha1.MachinePool{},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "machines-scheduler",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainMachines,
					CreateDecisions:  true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			createDecisions:           true,
			expectError:               true,
			expectDecisionCreated:     true, // Decision is created but processing fails
			expectMachinePoolAssigned: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []runtime.Object{tt.machine}
			for i := range tt.machinePools {
				objects = append(objects, &tt.machinePools[i])
			}
			if tt.pipelineConfig != nil {
				objects = append(objects, tt.pipelineConfig)
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&v1alpha1.Decision{}).
				Build()

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[ironcore.MachinePipelineRequest]]{
					Pipelines:       map[string]lib.FilterWeigherPipeline[ironcore.MachinePipelineRequest]{},
					PipelineConfigs: map[string]v1alpha1.Pipeline{},
				},
				Conf: conf.Config{
					SchedulingDomain: v1alpha1.SchedulingDomainMachines,
				},
				Monitor: lib.FilterWeigherPipelineMonitor{},
			}
			controller.Client = client

			if tt.pipelineConfig != nil {
				controller.PipelineConfigs[tt.pipelineConfig.Name] = *tt.pipelineConfig
				controller.Pipelines[tt.pipelineConfig.Name] = createMockPipeline()
			}

			err := controller.ProcessNewMachine(context.Background(), tt.machine)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
				return
			}

			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
				return
			}

			// Check if decision was created (if expected)
			if tt.expectDecisionCreated {
				var decisions v1alpha1.DecisionList
				err := client.List(context.Background(), &decisions)
				if err != nil {
					t.Errorf("Failed to list decisions: %v", err)
					return
				}

				found := false
				for _, decision := range decisions.Items {
					if decision.Spec.MachineRef != nil &&
						decision.Spec.MachineRef.Name == tt.machine.Name &&
						decision.Spec.MachineRef.Namespace == tt.machine.Namespace {
						found = true

						// Verify decision properties
						if decision.Spec.SchedulingDomain != v1alpha1.SchedulingDomainMachines {
							t.Errorf("expected scheduling domain %q, got %q", v1alpha1.SchedulingDomainMachines, decision.Spec.SchedulingDomain)
						}
						if decision.Spec.ResourceID != tt.machine.Name {
							t.Errorf("expected resource ID %q, got %q", tt.machine.Name, decision.Spec.ResourceID)
						}
						if decision.Spec.PipelineRef.Name != "machines-scheduler" {
							t.Errorf("expected pipeline ref %q, got %q", "machines-scheduler", decision.Spec.PipelineRef.Name)
						}

						// Check if result was set (only for successful cases)
						if !tt.expectError && tt.expectTargetHost != "" {
							if decision.Status.Result == nil {
								t.Error("expected decision result to be set")
								return
							}
							if decision.Status.Result.TargetHost == nil {
								t.Error("expected target host to be set")
								return
							}
							if *decision.Status.Result.TargetHost != tt.expectTargetHost {
								t.Errorf("expected target host %q, got %q", tt.expectTargetHost, *decision.Status.Result.TargetHost)
							}
						}
						break
					}
				}

				if !found {
					t.Error("expected decision to be created but was not found")
				}
			} else {
				// Check that no decisions were created
				var decisions v1alpha1.DecisionList
				err := client.List(context.Background(), &decisions)
				if err != nil {
					t.Errorf("Failed to list decisions: %v", err)
					return
				}

				for _, decision := range decisions.Items {
					if decision.Spec.MachineRef != nil &&
						decision.Spec.MachineRef.Name == tt.machine.Name &&
						decision.Spec.MachineRef.Namespace == tt.machine.Namespace {
						t.Error("expected no decision to be created but found one")
						break
					}
				}
			}

			// Check if machine pool was assigned (if expected)
			if tt.expectMachinePoolAssigned {
				var updatedMachine ironcorev1alpha1.Machine
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

				if updatedMachine.Spec.MachinePoolRef.Name != tt.expectTargetHost {
					t.Errorf("expected machine pool %q, got %q", tt.expectTargetHost, updatedMachine.Spec.MachinePoolRef.Name)
				}
			}
		})
	}
}

// Helper function to create a mock pipeline that works with the ironcore types
func createMockPipeline() lib.FilterWeigherPipeline[ironcore.MachinePipelineRequest] {
	return &mockMachinePipeline{}
}

type mockMachinePipeline struct{}

func (m *mockMachinePipeline) Run(request ironcore.MachinePipelineRequest) (v1alpha1.DecisionResult, error) {
	if len(request.Pools) == 0 {
		return v1alpha1.DecisionResult{}, nil
	}

	// Return the first pool as the target host
	targetHost := request.Pools[0].Name
	return v1alpha1.DecisionResult{
		TargetHost: &targetHost,
	}, nil
}
