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

func TestAvoidContendedHostsStep_Run(t *testing.T) {
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	// Create dependency tables
	deps := []interface{}{
		(*vmware.VROpsHostsystemContention)(nil),
	}
	for _, dep := range deps {
		if err := mockDB.
			Get().
			Model(dep).
			CreateTable(&orm.CreateTableOptions{IfNotExists: true}); err != nil {
			panic(err)
		}
	}

	// Insert mock data into the feature_vrops_hostsystem_contention table
	_, err := mockDB.Get().Exec(`
        INSERT INTO feature_vrops_hostsystem_contention (compute_host, avg_cpu_contention, max_cpu_contention)
        VALUES
            ('host1', 15.0, 25.0),
            ('host2', 5.0, 10.0),
            ('host3', 20.0, 30.0)
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create an instance of the step
	opts := map[string]any{
		"avgCPUContentionThreshold": 10.0,
		"maxCPUContentionThreshold": 20.0,
		"activationOnHit":           -1.0,
	}
	step := &AvoidContendedHostsStep{}
	if err := step.Init(&mockDB, opts); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name           string
		state          *plugins.State
		downvotedHosts map[string]struct{}
	}{
		{
			name: "Avoid contended hosts",
			state: &plugins.State{
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
			downvotedHosts: map[string]struct{}{
				"host1": {},
				"host3": {},
			},
		},
		{
			name: "No contended hosts",
			state: &plugins.State{
				Hosts: []plugins.StateHost{
					{ComputeHost: "host4", HypervisorHostname: "hypervisor4"},
					{ComputeHost: "host5", HypervisorHostname: "hypervisor5"},
				},
				Weights: map[string]float64{
					"host4": 1.0,
					"host5": 1.0,
				},
			},
			downvotedHosts: map[string]struct{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			weightsBefore := make(map[string]float64)
			for host, weight := range tt.state.Weights {
				weightsBefore[host] = weight
			}
			if err := step.Run(tt.state); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			// Check that the weights have decreased
			for host, weight := range weightsBefore {
				weightAfter := tt.state.Weights[host]
				if _, ok := tt.downvotedHosts[host]; ok {
					if weightAfter >= weight {
						t.Errorf("expected weight for host %s to be less than %f, got %f", host, weight, weightAfter)
					}
				} else {
					if weightAfter != weight {
						t.Errorf("expected weight for host %s to be %f, got %f", host, weight, weightAfter)
					}
				}
			}
		})
	}
}
