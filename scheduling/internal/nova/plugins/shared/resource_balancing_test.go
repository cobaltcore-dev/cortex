// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	delegationAPI "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/nova/api"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestResourceBalancingStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(
		testDB.AddTable(shared.HostUtilization{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the feature_host_utilization table
	hostUtilizations := []any{
		&shared.HostUtilization{ComputeHost: "host1", RAMUtilizedPct: 0, VCPUsUtilizedPct: 0, DiskUtilizedPct: 0, TotalRAMAllocatableMB: 1000, TotalVCPUsAllocatable: 100, TotalDiskAllocatableGB: 100},
		&shared.HostUtilization{ComputeHost: "host2", RAMUtilizedPct: 100, VCPUsUtilizedPct: 100, DiskUtilizedPct: 100, TotalRAMAllocatableMB: 1000, TotalVCPUsAllocatable: 100, TotalDiskAllocatableGB: 100},
	}
	if err := testDB.Insert(hostUtilizations...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name            string
		request         api.PipelineRequest
		expectedWeights map[string]float64
		opts            string
	}{
		{
			name: "Single VM",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedWeights: map[string]float64{
				"host1": 3.0,
				"host2": 0.0,
			},
			opts: `{
		"cpuEnabled": true,
		"cpuUtilizedLowerBoundPct": 0.0,
		"cpuUtilizedUpperBoundPct": 100.0,
		"cpuUtilizedActivationLowerBound": 1.0,
		"cpuUtilizedActivationUpperBound": 0.0,
		"ramEnabled": true,
		"ramUtilizedLowerBoundPct": 0.0,
		"ramUtilizedUpperBoundPct": 100.0,
		"ramUtilizedActivationLowerBound": 1.0,
		"ramUtilizedActivationUpperBound": 0.0,
		"diskEnabled": true,
		"diskUtilizedLowerBoundPct": 0.0,
		"diskUtilizedUpperBoundPct": 100.0,
		"diskUtilizedActivationLowerBound": 1.0,
		"diskUtilizedActivationUpperBound": 0.0
	}`,
		},
		{
			name: "CPU/RAM/Disk After Enabled",
			request: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						NumInstances: 1,
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								VCPUs:    10,  // 1 tenth
								MemoryMB: 100, // 1 tenth
								RootGB:   10,  // 1 tenth
							},
							Name:      "Flavor",
							Namespace: "nova",
							Version:   "1.2",
						},
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedWeights: map[string]float64{
				"host1": 3.0,
				"host2": 0.3, // 3 * 0.1 = 0.3
			},
			opts: `{
		"cpuAfterEnabled": true,
		"cpuUtilizedAfterLowerBoundPct": 0.0,
		"cpuUtilizedAfterUpperBoundPct": 100.0,
		"cpuUtilizedAfterActivationLowerBound": 1.0,
		"cpuUtilizedAfterActivationUpperBound": 0.0,
		"ramAfterEnabled": true,
		"ramUtilizedAfterLowerBoundPct": 0.0,
		"ramUtilizedAfterUpperBoundPct": 100.0,
		"ramUtilizedAfterActivationLowerBound": 1.0,
		"ramUtilizedAfterActivationUpperBound": 0.0,
		"diskAfterEnabled": true,
		"diskUtilizedAfterLowerBoundPct": 0.0,
		"diskUtilizedAfterUpperBoundPct": 100.0,
		"diskUtilizedAfterActivationLowerBound": 1.0,
		"diskUtilizedAfterActivationUpperBound": 0.0
	}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &ResourceBalancingStep{}
			if err := step.Init("", testDB, conf.NewRawOpts(tt.opts)); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			for host, expectedWeight := range tt.expectedWeights {
				if weight, ok := result.Activations[host]; ok {
					// round the weight to avoid floating point precision issues
					if weight-expectedWeight > 0.0001 || weight-expectedWeight < -0.0001 {
						t.Errorf("expected weight for host %s to be %f, got %f", host, expectedWeight, weight)
					}
				} else {
					t.Errorf("expected weight for host %s to be %f, but host was not found", host, expectedWeight)
				}
			}
		})
	}
}
