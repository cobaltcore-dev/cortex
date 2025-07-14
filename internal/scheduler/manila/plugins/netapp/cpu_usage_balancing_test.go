// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package netapp

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/netapp"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/manila/api"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestCPUUsageBalancingStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency table
	err := testDB.CreateTable(testDB.AddTable(netapp.StoragePoolCPUUsage{}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the feature_storage_pool_cpu_usage table
	_, err = testDB.Exec(`
        INSERT INTO feature_storage_pool_cpu_usage (storage_pool_name, avg_cpu_usage_pct, max_cpu_usage_pct)
        VALUES
            ('pool1', 0.0, 0.0),
            ('pool2', 100.0, 0.0),
            ('pool3', 0.0, 100.0),
            ('pool4', 100.0, 100.0)
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create an instance of the step
	opts := conf.NewRawOpts(`{
        "avgCPUUsageLowerBound": 0,
        "avgCPUUsageUpperBound": 100,
        "avgCPUUsageActivationLowerBound": 0.0,
        "avgCPUUsageActivationUpperBound": -1.0,
        "maxCPUUsageLowerBound": 0,
        "maxCPUUsageUpperBound": 100,
        "maxCPUUsageActivationLowerBound": 0.0,
        "maxCPUUsageActivationUpperBound": -1.0
    }`)
	step := &CPUUsageBalancingStep{}
	if err := step.Init("", testDB, opts); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name     string
		request  api.ExternalSchedulerRequest
		expected map[string]float64
	}{
		{
			name: "Avoid contended pools",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ShareHost: "pool1"},
					{ShareHost: "pool2"},
					{ShareHost: "pool3"},
					{ShareHost: "pool4"},
				},
			},
			expected: map[string]float64{
				"pool1": 0,
				"pool2": -1,
				"pool3": -1,
				"pool4": -2, // Max and avg usage stack up.
			},
		},
		{
			name: "Missing data",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ShareHost: "pool4"},
					{ShareHost: "pool5"}, // No data for pool5
				},
			},
			expected: map[string]float64{
				"pool4": -2,
				"pool5": 0, // No data but still contained in the result.
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
			for pool, weight := range result.Activations {
				expected := tt.expected[pool]
				if weight != expected {
					t.Errorf("expected weight for pool %s to be %f, got %f", pool, expected, weight)
				}
			}
		})
	}
}
