// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"log/slog"
	"math"
	"testing"

	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type mockPipelineStep struct {
	err  error
	name string
}

func (m *mockPipelineStep) Init(ctx context.Context, client client.Client, step v1alpha1.Step) error {
	return nil
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
		steps: map[string]Step[mockPipelineRequest]{
			"mock_pipeline_step": &mockPipelineStep{
				name: "mock_pipeline_step",
			},
		},
		order: []string{
			"mock_pipeline_step",
		},
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
			if len(result.OrderedHosts) != len(tt.expectedResult) {
				t.Fatalf("expected %d results, got %d", len(tt.expectedResult), len(result.OrderedHosts))
			}
			for i, host := range tt.expectedResult {
				if result.OrderedHosts[i] != host {
					t.Errorf("expected host %s at position %d, got %s", host, i, result.OrderedHosts[i])
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
		steps: map[string]Step[mockPipelineRequest]{},
		order: []string{"step1", "step2"},
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
		name: "mock_pipeline_step",
	}
	p := &pipeline[mockPipelineRequest]{
		order: []string{
			"mock_pipeline_step",
		},
		steps: map[string]Step[mockPipelineRequest]{
			"mock_pipeline_step": mockStep,
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
	if _, ok := result["mock_pipeline_step"]; !ok {
		t.Fatalf("expected result for step 'mock_pipeline_step'")
	}
	if result["mock_pipeline_step"]["host2"] != 1.0 {
		t.Errorf("expected weight 1.0 for host2, got %f", result["mock_pipeline_step"]["host2"])
	}
}
