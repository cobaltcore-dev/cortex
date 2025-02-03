// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/features/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
	"github.com/cobaltcore-dev/cortex/testlib"
	"github.com/go-pg/pg/v10/orm"
)

func TestAntiAffinityNoisyProjectsStep_Run(t *testing.T) {
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	// Create dependency tables
	deps := []interface{}{
		(*vmware.VROpsProjectNoisiness)(nil),
	}
	for _, dep := range deps {
		if err := mockDB.
			Get().
			Model(dep).
			CreateTable(&orm.CreateTableOptions{IfNotExists: true}); err != nil {
			panic(err)
		}
	}

	// Insert mock data into the feature_vrops_project_noisiness table
	_, err := mockDB.Get().Exec(`
        INSERT INTO feature_vrops_project_noisiness (project, compute_host, avg_cpu_of_project)
        VALUES
            ('project1', 'host1', 25.0),
            ('project1', 'host2', 30.0),
            ('project2', 'host3', 15.0)
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	opts := map[string]any{
		"avgCPUThreshold": 20.0,
	}
	step := &VROpsAntiAffinityNoisyProjectsStep{}
	if err := step.Init(&mockDB, opts); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		state         *plugins.State
		expectedHosts map[string]float64
	}{
		{
			name: "Noisy project",
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
					"host2": 1.0,
					"host3": 1.0,
				},
			},
			expectedHosts: map[string]float64{
				"host1": 0.0,
				"host2": 0.0,
				"host3": 1.0,
			},
		},
		{
			name: "Non-noisy project",
			state: &plugins.State{
				Spec: plugins.StateSpec{
					ProjectID: "project2",
				},
				Hosts: []plugins.StateHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
					{ComputeHost: "host2", HypervisorHostname: "hypervisor2"},
					{ComputeHost: "host3", HypervisorHostname: "hypervisor3"},
				},
				Weights: map[string]float64{
					"host1": 1.0,
					"host2": 1.0,
					"host3": 1.0,
				},
			},
			expectedHosts: map[string]float64{
				"host1": 1.0,
				"host2": 1.0,
				"host3": 1.0,
			},
		},
		{
			name: "No noisy project data",
			state: &plugins.State{
				Spec: plugins.StateSpec{
					ProjectID: "project3",
				},
				Hosts: []plugins.StateHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
					{ComputeHost: "host2", HypervisorHostname: "hypervisor2"},
					{ComputeHost: "host3", HypervisorHostname: "hypervisor3"},
				},
				Weights: map[string]float64{
					"host1": 1.0,
					"host2": 1.0,
					"host3": 1.0,
				},
			},
			expectedHosts: map[string]float64{
				"host1": 1.0,
				"host2": 1.0,
				"host3": 1.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := step.Run(tt.state)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			for host, expectedWeight := range tt.expectedHosts {
				if tt.state.Weights[host] != expectedWeight {
					t.Errorf(
						"expected weight for host %s to be %f, got %f",
						host, expectedWeight, tt.state.Weights[host],
					)
				}
			}
		})
	}
}
