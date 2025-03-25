// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
)

type mockPipelineStep struct {
	err             error
	weightsToReturn map[string]float64
}

func (m *mockPipelineStep) Init(db db.DB, opts conf.RawOpts) error {
	return nil
}

func (m *mockPipelineStep) GetName() string {
	return "mock_pipeline_step"
}

func (m *mockPipelineStep) Run(request api.Request) (map[string]float64, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.weightsToReturn, nil
}

func TestPipeline_Run(t *testing.T) {
	// Create an instance of the pipeline with a mock step
	pipeline := &Pipeline{
		executionOrder: [][]plugins.Step{
			{&mockPipelineStep{
				weightsToReturn: map[string]float64{"host1": 0.0, "host2": 1.0},
			}},
		},
		applicationOrder: []string{
			"mock_pipeline_step",
		},
	}

	tests := []struct {
		name           string
		request        api.Request
		expectedResult []string
	}{
		{
			name: "Single step pipeline",
			request: api.Request{
				Hosts: []api.Host{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
					{ComputeHost: "host2", HypervisorHostname: "hypervisor2"},
					{ComputeHost: "host3", HypervisorHostname: "hypervisor3"},
				},
			},
			expectedResult: []string{"host2", "host1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := pipeline.Run(
				tt.request,
				map[string]float64{"host1": 0.0, "host2": 0.0, "host3": 0.0},
			)
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

func TestPipeline_FallbackToOriginalHosts(t *testing.T) {
	// Create an instance of the pipeline with a mock step
	pipeline := &Pipeline{
		executionOrder: [][]plugins.Step{
			{&mockPipelineStep{weightsToReturn: map[string]float64{}}},
		},
		applicationOrder: []string{
			"mock_pipeline_step",
		},
	}

	tests := []struct {
		name           string
		request        api.Request
		expectedResult []string
	}{
		{
			name: "Single step pipeline",
			request: api.Request{
				Hosts: []api.Host{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
					{ComputeHost: "host2", HypervisorHostname: "hypervisor2"},
					{ComputeHost: "host3", HypervisorHostname: "hypervisor3"},
				},
			},
			// Should return the original hosts in the same order.
			expectedResult: []string{"host1", "host2", "host3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := pipeline.Run(
				tt.request,
				map[string]float64{"host1": 1.0, "host2": 0.5, "host3": 0.0},
			)
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
