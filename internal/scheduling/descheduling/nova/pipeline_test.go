// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/descheduling/nova/plugins"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Mock implementations for testing pipeline functionality

type mockPipelineStep struct {
	decisions   []plugins.Decision
	runError    error
	initError   error
	initialized bool
}

func (m *mockPipelineStep) Run() ([]plugins.Decision, error) {
	if m.runError != nil {
		return nil, m.runError
	}
	return m.decisions, nil
}

func (m *mockPipelineStep) Init(ctx context.Context, client client.Client, step v1alpha1.StepSpec) error {
	if m.initError != nil {
		return m.initError
	}
	m.initialized = true
	return nil
}

func TestPipeline_Init(t *testing.T) {
	tests := []struct {
		name           string
		supportedSteps map[string]Step
		confedSteps    []v1alpha1.StepSpec
		expectedSteps  int
		expectedError  bool
	}{
		{
			name: "successful initialization with single step",
			supportedSteps: map[string]Step{
				"test-step": &mockPipelineStep{},
			},
			confedSteps: []v1alpha1.StepSpec{{
				Name: "test-step",
				Type: v1alpha1.StepTypeDescheduler,
			}},
			expectedSteps: 1,
		},
		{
			name: "initialization with unsupported step",
			supportedSteps: map[string]Step{
				"test-step": &mockPipelineStep{},
			},
			confedSteps: []v1alpha1.StepSpec{{
				Name: "unsupported-step",
				Type: v1alpha1.StepTypeDescheduler,
			}},
			expectedError: true,
		},
		{
			name: "initialization with step init error",
			supportedSteps: map[string]Step{
				"failing-step": &mockPipelineStep{initError: errors.New("init failed")},
			},
			confedSteps: []v1alpha1.StepSpec{{
				Name: "failing-step",
				Type: v1alpha1.StepTypeDescheduler,
			}},
			expectedError: true,
		},
		{
			name: "initialization with multiple steps",
			supportedSteps: map[string]Step{
				"step1": &mockPipelineStep{},
				"step2": &mockPipelineStep{},
			},
			confedSteps: []v1alpha1.StepSpec{
				{
					Name: "step1",
					Type: v1alpha1.StepTypeDescheduler,
				},
				{
					Name: "step2",
					Type: v1alpha1.StepTypeDescheduler,
				},
			},
			expectedSteps: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline := &Pipeline{}

			err := pipeline.Init(t.Context(), tt.confedSteps, tt.supportedSteps)
			if tt.expectedError {
				if err == nil {
					t.Fatalf("expected error during initialization, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("Failed to initialize pipeline: %v", err)
			}

			if len(pipeline.steps) != tt.expectedSteps {
				t.Errorf("expected %d steps, got %d", tt.expectedSteps, len(pipeline.steps))
			}

			// Verify that successfully initialized steps are actually initialized
			for _, step := range pipeline.steps {
				if stepMonitor, ok := step.(StepMonitor); ok {
					if mockStep, ok := stepMonitor.step.(*mockPipelineStep); ok {
						if !mockStep.initialized {
							t.Error("step was not properly initialized")
						}
					}
				}
			}
		})
	}
}

func TestPipeline_run(t *testing.T) {
	tests := []struct {
		name            string
		steps           map[string]Step
		order           []string
		expectedResults map[string][]plugins.Decision
	}{
		{
			name: "successful run with single step",
			steps: map[string]Step{
				"test-step": &mockPipelineStep{
					decisions: []plugins.Decision{
						{VMID: "vm1", Reason: "test reason", Host: "host1"},
					},
				},
			},
			order: []string{"test-step"},
			expectedResults: map[string][]plugins.Decision{
				"test-step": {
					{VMID: "vm1", Reason: "test reason", Host: "host1"},
				},
			},
		},
		{
			name: "run with step error",
			steps: map[string]Step{
				"failing-step": &mockPipelineStep{
					runError: errors.New("step failed"),
				},
			},
			order:           []string{"failing-step"},
			expectedResults: map[string][]plugins.Decision{},
		},
		{
			name: "run with step skipped",
			steps: map[string]Step{
				"skipped-step": &mockPipelineStep{
					runError: ErrStepSkipped,
				},
			},
			order:           []string{"skipped-step"},
			expectedResults: map[string][]plugins.Decision{},
		},
		{
			name: "run with multiple steps",
			steps: map[string]Step{
				"step1": &mockPipelineStep{
					decisions: []plugins.Decision{
						{VMID: "vm1", Reason: "reason1", Host: "host1"},
					},
				},
				"step2": &mockPipelineStep{
					decisions: []plugins.Decision{
						{VMID: "vm2", Reason: "reason2", Host: "host2"},
					},
				},
			},
			order: []string{"step1", "step2"},
			expectedResults: map[string][]plugins.Decision{
				"step1": {
					{VMID: "vm1", Reason: "reason1", Host: "host1"},
				},
				"step2": {
					{VMID: "vm2", Reason: "reason2", Host: "host2"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline := &Pipeline{
				steps: tt.steps,
				order: tt.order,
			}

			results := pipeline.run()

			if !reflect.DeepEqual(results, tt.expectedResults) {
				t.Errorf("expected results %v, got %v", tt.expectedResults, results)
			}
		})
	}
}

func TestPipeline_combine(t *testing.T) {
	tests := []struct {
		name              string
		decisionsByStep   map[string][]plugins.Decision
		expectedDecisions []plugins.Decision
	}{
		{
			name: "single decision per VM",
			decisionsByStep: map[string][]plugins.Decision{
				"step1": {
					{VMID: "vm1", Reason: "reason1", Host: "host1"},
					{VMID: "vm2", Reason: "reason2", Host: "host2"},
				},
			},
			expectedDecisions: []plugins.Decision{
				{VMID: "vm1", Reason: "reason1", Host: "host1"},
				{VMID: "vm2", Reason: "reason2", Host: "host2"},
			},
		},
		{
			name: "multiple decisions for same VM with same host",
			decisionsByStep: map[string][]plugins.Decision{
				"step1": {
					{VMID: "vm1", Reason: "reason1", Host: "host1"},
				},
				"step2": {
					{VMID: "vm1", Reason: "reason2", Host: "host1"},
				},
			},
			expectedDecisions: []plugins.Decision{
				{VMID: "vm1", Reason: "multiple reasons: reason1; reason2", Host: "host1"},
			},
		},
		{
			name: "multiple decisions for same VM with different hosts",
			decisionsByStep: map[string][]plugins.Decision{
				"step1": {
					{VMID: "vm1", Reason: "reason1", Host: "host1"},
				},
				"step2": {
					{VMID: "vm1", Reason: "reason2", Host: "host2"},
				},
			},
			expectedDecisions: []plugins.Decision{}, // Should be skipped due to conflicting hosts
		},
		{
			name: "mixed scenario",
			decisionsByStep: map[string][]plugins.Decision{
				"step1": {
					{VMID: "vm1", Reason: "reason1", Host: "host1"},
					{VMID: "vm2", Reason: "reason2", Host: "host2"},
				},
				"step2": {
					{VMID: "vm1", Reason: "reason3", Host: "host1"},
					{VMID: "vm3", Reason: "reason4", Host: "host3"},
				},
			},
			expectedDecisions: []plugins.Decision{
				{VMID: "vm1", Reason: "multiple reasons: reason1; reason3", Host: "host1"},
				{VMID: "vm2", Reason: "reason2", Host: "host2"},
				{VMID: "vm3", Reason: "reason4", Host: "host3"},
			},
		},
		{
			name:              "empty input",
			decisionsByStep:   map[string][]plugins.Decision{},
			expectedDecisions: []plugins.Decision{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline := &Pipeline{}
			results := pipeline.combine(tt.decisionsByStep)

			// Sort results for consistent comparison
			if len(results) != len(tt.expectedDecisions) {
				t.Errorf("expected %d decisions, got %d", len(tt.expectedDecisions), len(results))
				return
			}

			// Create maps for easier comparison (order doesn't matter)
			expectedMap := make(map[string]plugins.Decision)
			for _, d := range tt.expectedDecisions {
				expectedMap[d.VMID] = d
			}

			resultMap := make(map[string]plugins.Decision)
			for _, d := range results {
				resultMap[d.VMID] = d
			}

			if !reflect.DeepEqual(expectedMap, resultMap) {
				t.Errorf("expected decisions %v, got %v", tt.expectedDecisions, results)
			}
		})
	}
}

func TestSupportedSteps(t *testing.T) {
	// Test that SupportedSteps is properly initialized
	if len(supportedSteps) == 0 {
		t.Error("SupportedSteps should not be empty")
	}
}

// Benchmark tests
func BenchmarkPipeline_run(b *testing.B) {
	steps := map[string]Step{
		"step1": &mockPipelineStep{
			decisions: []plugins.Decision{
				{VMID: "vm1", Reason: "bench reason", Host: "host1"},
			},
		},
		"step2": &mockPipelineStep{
			decisions: []plugins.Decision{
				{VMID: "vm2", Reason: "bench reason", Host: "host2"},
			},
		},
	}

	pipeline := &Pipeline{
		steps: steps,
		order: []string{"step1", "step2"},
	}

	b.ResetTimer()
	for range b.N {
		pipeline.run()
	}
}

func BenchmarkPipeline_combine(b *testing.B) {
	decisionsByStep := map[string][]plugins.Decision{
		"step1": {
			{VMID: "vm1", Reason: "reason1", Host: "host1"},
			{VMID: "vm2", Reason: "reason2", Host: "host2"},
		},
		"step2": {
			{VMID: "vm1", Reason: "reason3", Host: "host1"},
			{VMID: "vm3", Reason: "reason4", Host: "host3"},
		},
	}

	pipeline := &Pipeline{}

	b.ResetTimer()
	for range b.N {
		pipeline.combine(decisionsByStep)
	}
}
