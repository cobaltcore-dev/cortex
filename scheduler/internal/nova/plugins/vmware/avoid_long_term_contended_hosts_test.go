// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/vmware"
	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	delegationAPI "github.com/cobaltcore-dev/cortex/scheduler/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/nova/api"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestAvoidLongTermContendedHostsStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(testDB.AddTable(vmware.VROpsHostsystemContentionLongTerm{}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	vropsHostsystemContentionLongTerm := []any{
		&vmware.VROpsHostsystemContentionLongTerm{ComputeHost: "host1", AvgCPUContention: 0.0, MaxCPUContention: 0.0},
		&vmware.VROpsHostsystemContentionLongTerm{ComputeHost: "host2", AvgCPUContention: 100.0, MaxCPUContention: 0.0},
		&vmware.VROpsHostsystemContentionLongTerm{ComputeHost: "host3", AvgCPUContention: 0.0, MaxCPUContention: 100.0},
		&vmware.VROpsHostsystemContentionLongTerm{ComputeHost: "host4", AvgCPUContention: 100.0, MaxCPUContention: 100.0},
	}
	if err := testDB.Insert(vropsHostsystemContentionLongTerm...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create an instance of the step
	opts := conf.NewRawOpts(`{
        "avgCPUContentionLowerBound": 0,
        "avgCPUContentionUpperBound": 100,
        "avgCPUContentionActivationLowerBound": 0.0,
        "avgCPUContentionActivationUpperBound": -1.0,
        "maxCPUContentionLowerBound": 0,
        "maxCPUContentionUpperBound": 100,
        "maxCPUContentionActivationLowerBound": 0.0,
        "maxCPUContentionActivationUpperBound": -1.0
    }`)
	step := &AvoidLongTermContendedHostsStep{}
	if err := step.Init("", testDB, opts); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name     string
		request  api.PipelineRequest
		expected map[string]float64
	}{
		{
			name: "Avoid contended hosts",
			request: api.PipelineRequest{
				VMware: true,
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expected: map[string]float64{
				"host1": 0,
				"host2": -1,
				"host3": -1,
				"host4": -2, // Max and avg contention stack up.
			},
		},
		{
			name: "Missing data",
			request: api.PipelineRequest{
				VMware: true,
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expected: map[string]float64{
				"host4": -2,
				"host5": 0, // No data but still contained in the result.
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			// Check that the weights have decreased
			for host, weight := range result.Activations {
				expected := tt.expected[host]
				if weight != expected {
					t.Errorf("expected weight for host %s to be %f, got %f", host, expected, weight)
				}
			}
		})
	}
}
