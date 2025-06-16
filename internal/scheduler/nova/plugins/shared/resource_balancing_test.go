// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
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
	_, err = testDB.Exec(`
        INSERT INTO feature_host_utilization (compute_host, ram_utilized_pct, vcpus_utilized_pct, disk_utilized_pct, total_memory_allocatable_mb, total_vcpus_allocatable, total_disk_allocatable_gb)
        VALUES
            ('host1', 0, 0, 0, 1000, 100, 100),
            ('host2',100, 100, 100, 1000, 100, 100)
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create an instance of the step
	opts := conf.NewRawOpts(`{
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
    }`)
	step := &ResourceBalancingStep{}
	if err := step.Init(testDB, opts); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name            string
		request         api.ExternalSchedulerRequest
		expectedWeights map[string]float64
	}{
		{
			name: "Single VM",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NInstances: 1,
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
		},
		{
			name: "Multiple VMs",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						NInstances: 2,
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedWeights: map[string]float64{
				"host1": 0.0, // No weight change for multiple VMs
				"host2": 0.0, // No weight change for multiple VMs
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			for host, expectedWeight := range tt.expectedWeights {
				if weight, ok := result.Activations[host]; ok {
					if weight != expectedWeight {
						t.Errorf("expected weight for host %s to be %f, got %f", host, expectedWeight, weight)
					}
				} else {
					t.Errorf("expected weight for host %s to be %f, but host was not found", host, expectedWeight)
				}
			}
		})
	}
}
