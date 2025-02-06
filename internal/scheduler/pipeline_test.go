// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	testlibPlugins "github.com/cobaltcore-dev/cortex/testlib/scheduler/plugins"
	"gopkg.in/yaml.v2"
)

type mockPipelineStep struct {
	err error
}

func (m *mockPipelineStep) Init(db db.DB, opts yaml.MapSlice) error {
	return nil
}

func (m *mockPipelineStep) GetName() string {
	return "mock_pipeline_step"
}

func (m *mockPipelineStep) Run(scenario plugins.Scenario) (map[string]float64, error) {
	if m.err != nil {
		return nil, m.err
	}
	return map[string]float64{"host1": 0.0, "host2": 1.0}, nil
}

func TestPipeline_Run(t *testing.T) {
	mockDB := testlibDB.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	// Create an instance of the pipeline with a mock step
	pipeline := &pipeline{
		executionOrder: [][]plugins.Step{
			{&mockPipelineStep{}},
		},
		applicationOrder: []string{
			"mock_pipeline_step",
		},
	}

	tests := []struct {
		name           string
		scenario       plugins.Scenario
		expectedResult []string
	}{
		{
			name: "Single step pipeline",
			scenario: &testlibPlugins.MockScenario{
				Hosts: []testlibPlugins.MockScenarioHost{
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
				tt.scenario,
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
