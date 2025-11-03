// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"

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
		request         api.ExternalSchedulerRequest
		expectedWeights map[string]float64
		opts            ResourceBalancingStepOpts
	}{
		{
			name: "Single VM",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedWeights: map[string]float64{
				"host1": 3.0,
				"host2": 0.0,
			},
			opts: ResourceBalancingStepOpts{
				CPUEnabled:                       true,
				CPUUtilizedLowerBoundPct:         0.0,
				CPUUtilizedUpperBoundPct:         100.0,
				CPUUtilizedActivationLowerBound:  1.0,
				CPUUtilizedActivationUpperBound:  0.0,
				RAMEnabled:                       true,
				RAMUtilizedLowerBoundPct:         0.0,
				RAMUtilizedUpperBoundPct:         100.0,
				RAMUtilizedActivationLowerBound:  1.0,
				RAMUtilizedActivationUpperBound:  0.0,
				DiskEnabled:                      true,
				DiskUtilizedLowerBoundPct:        0.0,
				DiskUtilizedUpperBoundPct:        100.0,
				DiskUtilizedActivationLowerBound: 1.0,
				DiskUtilizedActivationUpperBound: 0.0,
			},
		},
		{
			name: "CPU/RAM/Disk After Enabled",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NumInstances: 1,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
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
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedWeights: map[string]float64{
				"host1": 3.0,
				"host2": 0.3, // 3 * 0.1 = 0.3
			},
			opts: ResourceBalancingStepOpts{
				CPUAfterEnabled:                       true,
				CPUUtilizedAfterLowerBoundPct:         0.0,
				CPUUtilizedAfterUpperBoundPct:         100.0,
				CPUUtilizedAfterActivationLowerBound:  1.0,
				CPUUtilizedAfterActivationUpperBound:  0.0,
				RAMAfterEnabled:                       true,
				RAMUtilizedAfterLowerBoundPct:         0.0,
				RAMUtilizedAfterUpperBoundPct:         100.0,
				RAMUtilizedAfterActivationLowerBound:  1.0,
				RAMUtilizedAfterActivationUpperBound:  0.0,
				DiskAfterEnabled:                      true,
				DiskUtilizedAfterLowerBoundPct:        0.0,
				DiskUtilizedAfterUpperBoundPct:        100.0,
				DiskUtilizedAfterActivationLowerBound: 1.0,
				DiskUtilizedAfterActivationUpperBound: 0.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &ResourceBalancingStep{}
			step.Options = tt.opts
			step.DB = testDB
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
