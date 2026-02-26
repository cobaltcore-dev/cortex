// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"log/slog"
	"math"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Mock pipeline type for testing
type mockPipeline struct {
	name string
}

func TestPipeline_Run(t *testing.T) {
	// Create an instance of the pipeline with a mock step
	pipeline := &filterWeigherPipeline[mockFilterWeigherPipelineRequest]{
		filters: map[string]Filter[mockFilterWeigherPipelineRequest]{
			"mock_filter": &mockFilter[mockFilterWeigherPipelineRequest]{
				RunFunc: func(traceLog *slog.Logger, request mockFilterWeigherPipelineRequest) (*FilterWeigherPipelineStepResult, error) {
					// Filter out host3
					return &FilterWeigherPipelineStepResult{
						Activations: map[string]float64{
							"host1": 0.0,
							"host2": 0.0,
						},
					}, nil
				},
			},
		},
		filtersOrder: []string{"mock_filter"},
		weighers: map[string]Weigher[mockFilterWeigherPipelineRequest]{
			"mock_weigher": &mockWeigher[mockFilterWeigherPipelineRequest]{
				RunFunc: func(traceLog *slog.Logger, request mockFilterWeigherPipelineRequest) (*FilterWeigherPipelineStepResult, error) {
					// Assign weights to hosts
					activations := map[string]float64{
						"host1": 0.5,
						"host2": 1.0,
						"host3": -0.5,
					}
					return &FilterWeigherPipelineStepResult{
						Activations: activations,
					}, nil
				},
			},
		},
		weighersOrder: []string{"mock_weigher"},
	}

	tests := []struct {
		name           string
		request        mockFilterWeigherPipelineRequest
		expectedResult []string
	}{
		{
			name: "Single step pipeline",
			request: mockFilterWeigherPipelineRequest{
				Hosts:   []string{"host1", "host2", "host3"},
				Weights: map[string]float64{"host1": 0.0, "host2": 0.0, "host3": 0.0},
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
	p := &filterWeigherPipeline[mockFilterWeigherPipelineRequest]{}

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
	p := &filterWeigherPipeline[mockFilterWeigherPipelineRequest]{
		weighers:      map[string]Weigher[mockFilterWeigherPipelineRequest]{},
		weighersOrder: []string{"step1", "step2"},
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
			result := p.applyWeights(slog.Default(), tt.stepWeights, tt.inWeights)
			for host, weight := range tt.expectedResult {
				if result[host] != weight {
					t.Errorf("expected weight %f for host %s, got %f", weight, host, result[host])
				}
			}
		})
	}
}

func TestPipeline_SortHostsByWeights(t *testing.T) {
	p := &filterWeigherPipeline[mockFilterWeigherPipelineRequest]{}

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
			result := p.sortHostsByWeights(tt.weights)
			for i, host := range tt.expected {
				if result[i] != host {
					t.Errorf("expected host %s at position %d, got %s", host, i, result[i])
				}
			}
		})
	}
}

func TestPipeline_RunFilters(t *testing.T) {
	mockStep := &mockFilter[mockFilterWeigherPipelineRequest]{
		RunFunc: func(traceLog *slog.Logger, request mockFilterWeigherPipelineRequest) (*FilterWeigherPipelineStepResult, error) {
			// Filter out host3
			return &FilterWeigherPipelineStepResult{
				Activations: map[string]float64{
					"host1": 0.0,
					"host2": 0.0,
				},
			}, nil
		},
	}
	p := &filterWeigherPipeline[mockFilterWeigherPipelineRequest]{
		filtersOrder: []string{
			"mock_filter",
		},
		filters: map[string]Filter[mockFilterWeigherPipelineRequest]{
			"mock_filter": mockStep,
		},
	}

	request := mockFilterWeigherPipelineRequest{
		Hosts:   []string{"host1", "host2"},
		Weights: map[string]float64{"host1": 0.0, "host2": 0.0, "host3": 0.0},
	}

	req := p.runFilters(slog.Default(), request)
	if len(req.Hosts) != 2 {
		t.Fatalf("expected 2 step results, got %d", len(req.Hosts))
	}
}

func TestInitNewFilterWeigherPipeline_Success(t *testing.T) {
	scheme := runtime.NewScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	supportedFilters := map[string]func() Filter[mockFilterWeigherPipelineRequest]{
		"test-filter": func() Filter[mockFilterWeigherPipelineRequest] {
			return &mockFilter[mockFilterWeigherPipelineRequest]{
				InitFunc: func(ctx context.Context, c client.Client, step v1alpha1.FilterSpec) error {
					return nil
				},
			}
		},
	}

	supportedWeighers := map[string]func() Weigher[mockFilterWeigherPipelineRequest]{
		"test-weigher": func() Weigher[mockFilterWeigherPipelineRequest] {
			return &mockWeigher[mockFilterWeigherPipelineRequest]{
				InitFunc: func(ctx context.Context, c client.Client, step v1alpha1.WeigherSpec) error {
					return nil
				},
			}
		},
	}

	confedFilters := []v1alpha1.FilterSpec{
		{
			Name:   "test-filter",
			Params: nil,
		},
	}

	confedWeighers := []v1alpha1.WeigherSpec{
		{
			Name:   "test-weigher",
			Params: nil,
		},
	}

	monitor := FilterWeigherPipelineMonitor{
		PipelineName: "test-pipeline",
	}

	result := InitNewFilterWeigherPipeline(
		t.Context(),
		cl,
		"test-pipeline",
		supportedFilters,
		confedFilters,
		supportedWeighers,
		confedWeighers,
		monitor,
	)

	if len(result.FilterErrors) != 0 {
		t.Fatalf("expected no filter error, got %v", result.FilterErrors)
	}
	if len(result.WeigherErrors) != 0 {
		t.Fatalf("expected no weigher error, got %v", result.WeigherErrors)
	}
	if result.Pipeline == nil {
		t.Fatal("expected pipeline, got nil")
	}
}

func TestInitNewFilterWeigherPipeline_UnsupportedFilter(t *testing.T) {
	scheme := runtime.NewScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	supportedFilters := map[string]func() Filter[mockFilterWeigherPipelineRequest]{}
	supportedWeighers := map[string]func() Weigher[mockFilterWeigherPipelineRequest]{}

	confedFilters := []v1alpha1.FilterSpec{
		{
			Name:   "unsupported-filter",
			Params: nil,
		},
	}

	monitor := FilterWeigherPipelineMonitor{
		PipelineName: "test-pipeline",
	}

	result := InitNewFilterWeigherPipeline(
		t.Context(),
		cl,
		"test-pipeline",
		supportedFilters,
		confedFilters,
		supportedWeighers,
		nil,
		monitor,
	)

	if len(result.UnknownFilters) != 1 || result.UnknownFilters[0] != "unsupported-filter" {
		t.Fatalf("expected unknown filter 'unsupported-filter', got %v", result.UnknownFilters)
	}
}

func TestInitNewFilterWeigherPipeline_UnsupportedWeigher(t *testing.T) {
	scheme := runtime.NewScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	supportedFilters := map[string]func() Filter[mockFilterWeigherPipelineRequest]{}
	supportedWeighers := map[string]func() Weigher[mockFilterWeigherPipelineRequest]{}

	confedWeighers := []v1alpha1.WeigherSpec{
		{
			Name:   "unsupported-weigher",
			Params: nil,
		},
	}

	monitor := FilterWeigherPipelineMonitor{
		PipelineName: "test-pipeline",
	}

	result := InitNewFilterWeigherPipeline(
		t.Context(),
		cl,
		"test-pipeline",
		supportedFilters,
		nil,
		supportedWeighers,
		confedWeighers,
		monitor,
	)

	if len(result.UnknownWeighers) != 1 || result.UnknownWeighers[0] != "unsupported-weigher" {
		t.Fatalf("expected unknown weigher 'unsupported-weigher', got %v", result.UnknownWeighers)
	}
}

func TestFilterWeigherPipelineMonitor_SubPipeline(t *testing.T) {
	monitor := NewPipelineMonitor()

	subPipeline := monitor.SubPipeline("test-sub-pipeline")

	if subPipeline.PipelineName != "test-sub-pipeline" {
		t.Errorf("expected pipeline name 'test-sub-pipeline', got '%s'", subPipeline.PipelineName)
	}
	// Verify that the original monitor is not modified
	if monitor.PipelineName == "test-sub-pipeline" {
		t.Error("original monitor should not be modified")
	}
}
