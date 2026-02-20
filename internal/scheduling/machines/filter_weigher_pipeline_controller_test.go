// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package machines

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/external/ironcore"
	ironcorev1alpha1 "github.com/cobaltcore-dev/cortex/api/external/ironcore/v1alpha1"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

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
		expectError               bool
		expectMachinePoolAssigned bool
		expectTargetHost          string
	}{
		{
			name: "successful machine processing",
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
					CreateDecisions:  false,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			expectError:               false,
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
			expectError:               false,
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
			expectError:               true,
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
					DecisionQueue:   make(chan lib.DecisionUpdate),
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

func (m *mockMachinePipeline) Run(request ironcore.MachinePipelineRequest) (lib.FilterWeigherPipelineResult, error) {
	if len(request.Pools) == 0 {
		return lib.FilterWeigherPipelineResult{
			OrderedHosts: []string{},
		}, nil
	}

	// Return the first pool as the target host
	targetHost := request.Pools[0].Name
	return lib.FilterWeigherPipelineResult{
		OrderedHosts: []string{targetHost},
	}, nil
}
