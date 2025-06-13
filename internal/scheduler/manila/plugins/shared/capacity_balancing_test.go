// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/manila/api"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestResourceBalancingStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(
		testDB.AddTable(shared.StoragePoolUtilization{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the feature_storage_pool_utilization table
	_, err = testDB.Exec(`
        INSERT INTO feature_storage_pool_utilization (storage_pool_name, capacity_utilized_pct)
        VALUES
            ('host1', 100),
            ('host2', 0)
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create an instance of the step
	opts := conf.NewRawOpts(`{
        "utilizedLowerBoundPct": 0.0,
        "utilizedUpperBoundPct": 100.0,
        "utilizedActivationLowerBound": 1.0,
        "utilizedActivationUpperBound": 0.0
    }`)
	step := &CapacityBalancingStep{}
	if err := step.Init(testDB, opts); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name            string
		request         api.ExternalSchedulerRequest
		expectedWeights map[string]float64
	}{
		{
			name: "Single share",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ShareHost: "host1"},
					{ShareHost: "host2"},
				},
			},
			expectedWeights: map[string]float64{
				"host1": 0.0, // utilized
				"host2": 1.0, // empty
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
