// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"log/slog"
	"math"
	"testing"

	"github.com/cobaltcore-dev/cortex/scheduler/internal/conf"

	libconf "github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/testlib/mqtt"
	"k8s.io/client-go/rest"
)

type mockPipelineStep struct {
	err   error
	name  string
	alias string
}

func (m *mockPipelineStep) Init(alias string, db db.DB, opts libconf.RawOpts) error {
	m.alias = alias
	return nil
}

func (m *mockPipelineStep) GetName() string {
	return m.name
}

func (m *mockPipelineStep) GetAlias() string {
	return m.alias
}

func (m *mockPipelineStep) Run(traceLog *slog.Logger, request mockPipelineRequest) (*StepResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &StepResult{
		Activations: map[string]float64{"host1": 0.0, "host2": 1.0},
	}, nil
}

func TestPipeline_Run(t *testing.T) {
	// Create an instance of the pipeline with a mock step
	pipeline := &pipeline[mockPipelineRequest]{
		executionOrder: [][]Step[mockPipelineRequest]{
			{&mockPipelineStep{
				name:  "mock_pipeline_step",
				alias: "mock_pipeline_step_alias",
			}},
		},
		applicationOrder: []string{
			"mock_pipeline_step (mock_pipeline_step_alias)",
		},
		mqttClient: &mqtt.MockClient{},
	}

	tests := []struct {
		name           string
		request        mockPipelineRequest
		expectedResult []string
	}{
		{
			name: "Single step pipeline",
			request: mockPipelineRequest{
				Subjects: []string{"host1", "host2", "host3"},
				Weights:  map[string]float64{"host1": 0.0, "host2": 0.0, "host3": 0.0},
			},
			expectedResult: []string{"host2", "host1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := pipeline.Run(tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if len(result) != len(tt.expectedResult) {
				t.Fatalf("expected %d results, got %d", len(tt.expectedResult), len(result))
			}
			for i, host := range tt.expectedResult {
				if result[i] != host {
					t.Errorf("expected host %s at position %d, got %s", host, i, result[i])
				}
			}
		})
	}
}

func TestPipeline_NormalizeNovaWeights(t *testing.T) {
	p := &pipeline[mockPipelineRequest]{}

	tests := []struct {
		name     string
		weights  map[string]float64
		expected map[string]float64
	}{
		{
			name: "Normalize weights",
			weights: map[string]float64{
				"host1": 1000.0,
				"host2": -1000.0,
				"host3": 0.0,
			},
			expected: map[string]float64{
				"host1": 1.0,
				"host2": -1.0,
				"host3": 0.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.normalizeInputWeights(tt.weights)
			for host, weight := range tt.expected {
				if result[host] != weight {
					t.Errorf("expected weight %f for host %s, got %f", weight, host, result[host])
				}
			}
		})
	}
}

func TestPipeline_ApplyStepWeights(t *testing.T) {
	p := &pipeline[mockPipelineRequest]{
		applicationOrder: []string{"step1", "step2"},
	}

	tests := []struct {
		name           string
		stepWeights    map[string]map[string]float64
		inWeights      map[string]float64
		expectedResult map[string]float64
	}{
		{
			name: "Apply step weights",
			stepWeights: map[string]map[string]float64{
				"step1": {"host1": 0.5, "host2": 0.2},
				"step2": {"host1": 0.3, "host2": 0.4},
			},
			inWeights: map[string]float64{
				"host1": 1.0,
				"host2": 1.0,
			},
			expectedResult: map[string]float64{
				"host1": 1.0 + math.Tanh(0.5) + math.Tanh(0.3),
				"host2": 1.0 + math.Tanh(0.2) + math.Tanh(0.4),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.applyStepWeights(tt.stepWeights, tt.inWeights)
			for host, weight := range tt.expectedResult {
				if result[host] != weight {
					t.Errorf("expected weight %f for host %s, got %f", weight, host, result[host])
				}
			}
		})
	}
}

func TestPipeline_SortHostsByWeights(t *testing.T) {
	p := &pipeline[mockPipelineRequest]{}

	tests := []struct {
		name     string
		weights  map[string]float64
		expected []string
	}{
		{
			name: "Sort hosts by weights",
			weights: map[string]float64{
				"host1": 0.5,
				"host2": 1.0,
				"host3": 0.2,
			},
			expected: []string{"host2", "host1", "host3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.sortSubjectsByWeights(tt.weights)
			for i, host := range tt.expected {
				if result[i] != host {
					t.Errorf("expected host %s at position %d, got %s", host, i, result[i])
				}
			}
		})
	}
}

func TestPipeline_RunSteps(t *testing.T) {
	mockStep := &mockPipelineStep{
		name:  "mock_pipeline_step",
		alias: "mock_pipeline_step_alias",
	}
	p := &pipeline[mockPipelineRequest]{
		executionOrder: [][]Step[mockPipelineRequest]{
			{mockStep},
		},
	}

	request := mockPipelineRequest{
		Subjects: []string{"host1", "host2"},
		Weights:  map[string]float64{"host1": 0.0, "host2": 0.0},
	}

	result := p.runSteps(slog.Default(), request)
	if len(result) != 1 {
		t.Fatalf("expected 1 step result, got %d", len(result))
	}
	if _, ok := result["mock_pipeline_step (mock_pipeline_step_alias)"]; !ok {
		t.Fatalf("expected result for step 'mock_pipeline_step (mock_pipeline_step_alias)'")
	}
	if result["mock_pipeline_step (mock_pipeline_step_alias)"]["host2"] != 1.0 {
		t.Errorf("expected weight 1.0 for host2, got %f", result["mock_pipeline_step (mock_pipeline_step_alias)"]["host2"])
	}
}

func TestNewPipeline(t *testing.T) {
	database := db.DB{}          // Mock or initialize as needed
	monitor := PipelineMonitor{} // Replace with an actual mock implementation if available
	mqttClient := &mqtt.MockClient{}

	// Set up kubekonfig for GetConfigOrDie
	restConfig := &rest.Config{}
	_ = restConfig

	supportedSteps := map[string]func() Step[mockPipelineRequest]{
		"mock_pipeline_step": func() Step[mockPipelineRequest] {
			return &mockPipelineStep{
				name: "mock_pipeline_step",
			}
		},
	}

	pipeline := NewPipeline(
		supportedSteps,
		[]conf.SchedulerStepConfig{{Name: "mock_pipeline_step", Options: libconf.RawOpts{}}},
		[]StepWrapper[mockPipelineRequest]{},
		database, monitor, mqttClient, "test/topic",
	).(*pipeline[mockPipelineRequest])

	if len(pipeline.executionOrder) != 1 {
		t.Fatalf("expected 1 execution order group, got %d", len(pipeline.executionOrder))
	}
	if len(pipeline.executionOrder[0]) != 1 {
		t.Fatalf("expected 1 step in the execution order, got %d", len(pipeline.executionOrder[0]))
	}
	if pipeline.executionOrder[0][0].GetName() != "mock_pipeline_step" {
		t.Errorf("expected step name 'mock_pipeline_step', got '%s'", pipeline.executionOrder[0][0].GetName())
	}
}

func TestNewPipeline_SameStepMultipleAliases(t *testing.T) {
	database := db.DB{}          // Mock or initialize as needed
	monitor := PipelineMonitor{} // Replace with an actual mock implementation if available
	mqttClient := &mqtt.MockClient{}
	supportedSteps := map[string]func() Step[mockPipelineRequest]{
		"mock_pipeline_step": func() Step[mockPipelineRequest] {
			return &mockPipelineStep{
				name:  "mock_pipeline_step",
				alias: "", // Set by Init
			}
		},
	}

	pipeline := NewPipeline(
		supportedSteps,
		[]conf.SchedulerStepConfig{
			{Name: "mock_pipeline_step", Alias: "mock_step_1", Options: libconf.RawOpts{}},
			{Name: "mock_pipeline_step", Alias: "mock_step_2", Options: libconf.RawOpts{}},
		},
		[]StepWrapper[mockPipelineRequest]{},
		database, monitor, mqttClient, "test/topic",
	).(*pipeline[mockPipelineRequest])

	if len(pipeline.executionOrder[0]) != 2 {
		t.Fatalf("expected 2 steps in the execution order, got %d: %v", len(pipeline.executionOrder[0]), pipeline.executionOrder[0])
	}
	if pipeline.executionOrder[0][0].GetName() != "mock_pipeline_step" {
		t.Errorf("expected step name 'mock_pipeline_step', got '%s'", pipeline.executionOrder[0][0].GetName())
	}
	if pipeline.executionOrder[0][1].GetName() != "mock_pipeline_step" {
		t.Errorf("expected step name 'mock_pipeline_step', got '%s'", pipeline.executionOrder[0][1].GetName())
	}
	if pipeline.executionOrder[0][0].GetAlias() != "mock_step_1" {
		t.Errorf("expected step alias 'mock_step_1', got '%s'", pipeline.executionOrder[0][0].GetAlias())
	}
	if pipeline.executionOrder[0][1].GetAlias() != "mock_step_2" {
		t.Errorf("expected step alias 'mock_step_2', got '%s'", pipeline.executionOrder[0][1].GetAlias())
	}
	if pipeline.applicationOrder[0] != "mock_pipeline_step (mock_step_1)" {
		t.Errorf("expected application order 'mock_pipeline_step (mock_step_1)', got '%s'", pipeline.applicationOrder[0])
	}
	if pipeline.applicationOrder[1] != "mock_pipeline_step (mock_step_2)" {
		t.Errorf("expected application order 'mock_pipeline_step (mock_step_2)', got '%s'", pipeline.applicationOrder[1])
	}
}
