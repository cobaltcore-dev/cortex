// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/descheduler/internal/conf"
	"github.com/cobaltcore-dev/cortex/descheduler/internal/nova/plugins"
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
)

// Mock implementations for testing pipeline functionality

type mockPipelineStep struct {
	name        string
	decisions   []plugins.Decision
	runError    error
	initError   error
	initialized bool
}

func (m *mockPipelineStep) GetName() string {
	return m.name
}

func (m *mockPipelineStep) Run() ([]plugins.Decision, error) {
	if m.runError != nil {
		return nil, m.runError
	}
	return m.decisions, nil
}

func (m *mockPipelineStep) Init(db db.DB, opts libconf.RawOpts) error {
	if m.initError != nil {
		return m.initError
	}
	m.initialized = true
	return nil
}

func TestPipeline_Init(t *testing.T) {
	tests := []struct {
		name           string
		supportedSteps []Step
		config         conf.DeschedulerConfig
		expectedSteps  int
	}{
		{
			name: "successful initialization with single step",
			supportedSteps: []Step{
				&mockPipelineStep{name: "test-step"},
			},
			config: conf.DeschedulerConfig{
				Nova: conf.NovaDeschedulerConfig{
					Plugins: []conf.DeschedulerStepConfig{
						{Name: "test-step", Options: libconf.RawOpts{}},
					},
				},
			},
			expectedSteps: 1,
		},
		{
			name: "initialization with unsupported step",
			supportedSteps: []Step{
				&mockPipelineStep{name: "test-step"},
			},
			config: conf.DeschedulerConfig{
				Nova: conf.NovaDeschedulerConfig{
					Plugins: []conf.DeschedulerStepConfig{
						{Name: "unsupported-step", Options: libconf.RawOpts{}},
					},
				},
			},
			expectedSteps: 0,
		},
		{
			name: "initialization with step init error",
			supportedSteps: []Step{
				&mockPipelineStep{name: "failing-step", initError: errors.New("init failed")},
			},
			config: conf.DeschedulerConfig{
				Nova: conf.NovaDeschedulerConfig{
					Plugins: []conf.DeschedulerStepConfig{
						{Name: "failing-step", Options: libconf.RawOpts{}},
					},
				},
			},
			expectedSteps: 0,
		},
		{
			name: "initialization with multiple steps",
			supportedSteps: []Step{
				&mockPipelineStep{name: "step1"},
				&mockPipelineStep{name: "step2"},
			},
			config: conf.DeschedulerConfig{
				Nova: conf.NovaDeschedulerConfig{
					Plugins: []conf.DeschedulerStepConfig{
						{Name: "step1", Options: libconf.RawOpts{}},
						{Name: "step2", Options: libconf.RawOpts{}},
					},
				},
			},
			expectedSteps: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline := &Pipeline{
				Config: tt.config,
			}

			ctx := context.Background()
			testDB := db.DB{}
			pipeline.Init(tt.supportedSteps, ctx, testDB, tt.config)

			if len(pipeline.steps) != tt.expectedSteps {
				t.Errorf("expected %d steps, got %d", tt.expectedSteps, len(pipeline.steps))
			}

			// Verify that successfully initialized steps are actually initialized
			for _, step := range pipeline.steps {
				if stepMonitor, ok := step.(StepMonitor); ok {
					if mockStep, ok := stepMonitor.step.(*mockPipelineStep); ok {
						if !mockStep.initialized {
							t.Errorf("step %s was not properly initialized", mockStep.name)
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
		steps           []Step
		expectedResults map[string][]plugins.Decision
	}{
		{
			name: "successful run with single step",
			steps: []Step{
				&mockPipelineStep{
					name: "test-step",
					decisions: []plugins.Decision{
						{VMID: "vm1", Reason: "test reason", Host: "host1"},
					},
				},
			},
			expectedResults: map[string][]plugins.Decision{
				"test-step": {
					{VMID: "vm1", Reason: "test reason", Host: "host1"},
				},
			},
		},
		{
			name: "run with step error",
			steps: []Step{
				&mockPipelineStep{
					name:     "failing-step",
					runError: errors.New("step failed"),
				},
			},
			expectedResults: map[string][]plugins.Decision{},
		},
		{
			name: "run with step skipped",
			steps: []Step{
				&mockPipelineStep{
					name:     "skipped-step",
					runError: ErrStepSkipped,
				},
			},
			expectedResults: map[string][]plugins.Decision{},
		},
		{
			name: "run with multiple steps",
			steps: []Step{
				&mockPipelineStep{
					name: "step1",
					decisions: []plugins.Decision{
						{VMID: "vm1", Reason: "reason1", Host: "host1"},
					},
				},
				&mockPipelineStep{
					name: "step2",
					decisions: []plugins.Decision{
						{VMID: "vm2", Reason: "reason2", Host: "host2"},
					},
				},
			},
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
	if len(SupportedSteps) == 0 {
		t.Error("SupportedSteps should not be empty")
	}

	// Verify each supported step has a name
	for i, step := range SupportedSteps {
		if step.GetName() == "" {
			t.Errorf("supported step at index %d has empty name", i)
		}
	}
}

// Benchmark tests
func BenchmarkPipeline_run(b *testing.B) {
	steps := []Step{
		&mockPipelineStep{
			name: "bench-step1",
			decisions: []plugins.Decision{
				{VMID: "vm1", Reason: "bench reason", Host: "host1"},
			},
		},
		&mockPipelineStep{
			name: "bench-step2",
			decisions: []plugins.Decision{
				{VMID: "vm2", Reason: "bench reason", Host: "host2"},
			},
		},
	}

	pipeline := &Pipeline{
		steps: steps,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	for i := 0; i < b.N; i++ {
		pipeline.combine(decisionsByStep)
	}
}
