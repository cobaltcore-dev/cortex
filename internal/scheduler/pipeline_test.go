// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
	"github.com/cobaltcore-dev/cortex/testlib"
)

type mockPipelineStep struct {
	err error
}

func (m *mockPipelineStep) Conf(db db.DB, opts map[string]interface{}) {}

func (m *mockPipelineStep) GetName() string {
	return "mock_pipeline_step"
}

func (m *mockPipelineStep) Run(state *plugins.State) error {
	if m.err != nil {
		return m.err
	}
	// Example modification: downvote host1
	for i := range state.Hosts {
		if state.Hosts[i].ComputeHost == "host1" {
			state.Weights[state.Hosts[i].ComputeHost] = 0.0
		}
	}
	return nil
}

func TestPipeline_Run(t *testing.T) {
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	// Create an instance of the pipeline with a mock step
	pipeline := &pipeline{
		Steps: []plugins.Step{
			&mockPipelineStep{},
		},
	}

	tests := []struct {
		name          string
		state         *plugins.State
		expectedHosts []string
	}{
		{
			name: "Single step pipeline",
			state: &plugins.State{
				Spec: plugins.StateSpec{
					ProjectID: "project1",
				},
				Hosts: []plugins.StateHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
					{ComputeHost: "host2", HypervisorHostname: "hypervisor2"},
					{ComputeHost: "host3", HypervisorHostname: "hypervisor3"},
				},
				Weights: map[string]float64{
					"host1": 1.0,
					"host2": 2.0,
					"host3": 3.0,
				},
			},
			expectedHosts: []string{"host3", "host2", "host1"},
		},
		{
			name: "Host1 downvoted by mock step",
			state: &plugins.State{
				Spec: plugins.StateSpec{
					ProjectID: "project1",
				},
				Hosts: []plugins.StateHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
					{ComputeHost: "host2", HypervisorHostname: "hypervisor2"},
					{ComputeHost: "host3", HypervisorHostname: "hypervisor3"},
				},
				Weights: map[string]float64{
					"host1": 1.0,
					"host2": 2.0,
					"host3": 3.0,
				},
			},
			expectedHosts: []string{"host3", "host2", "host1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotHosts, err := pipeline.Run(tt.state)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if len(gotHosts) != len(tt.expectedHosts) {
				t.Fatalf(
					"expected %d hosts, got %d",
					len(tt.expectedHosts), len(gotHosts),
				)
			}
			for i := range gotHosts {
				if gotHosts[i] != tt.expectedHosts[i] {
					t.Errorf(
						"expected host at position %d to be %s, got %s",
						i, tt.expectedHosts[i], gotHosts[i],
					)
				}
			}
		})
	}
}
