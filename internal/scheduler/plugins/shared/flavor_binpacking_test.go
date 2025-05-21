// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	testlibAPI "github.com/cobaltcore-dev/cortex/testlib/scheduler/api"
)

func TestFlavorBinpackingStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(testDB.AddTable(shared.FlavorHostSpace{}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the feature_flavor_host_space table
	_, err = testDB.Exec(`
        INSERT INTO feature_flavor_host_space (flavor_id, compute_host, ram_left_mb, vcpus_left, disk_left_gb)
        VALUES
            ('flavor1', 'host1', 1024, 4, 100),
            ('flavor1', 'host2', 2048, 2, 200),
            ('flavor2', 'host1', 512, 1, 50)
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create an instance of the step
	opts := conf.NewRawOpts(`{
        "cpuEnabled": true,
        "cpuFreeLowerBound": 0,
        "cpuFreeUpperBound": 4,
        "cpuFreeActivationLowerBound": 0.0,
        "cpuFreeActivationUpperBound": 1.0,
        "ramEnabled": true,
        "ramFreeLowerBound": 0,
        "ramFreeUpperBound": 2048,
        "ramFreeActivationLowerBound": 0.0,
        "ramFreeActivationUpperBound": 1.0,
        "diskEnabled": true,
        "diskFreeLowerBound": 0,
        "diskFreeUpperBound": 200,
        "diskFreeActivationLowerBound": 0.0,
        "diskFreeActivationUpperBound": 1.0
    }`)
	step := &FlavorBinpackingStep{}
	if err := step.Init(testDB, opts); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name            string
		request         testlibAPI.MockRequest
		expectedWeights map[string]float64
	}{
		{
			name: "Single VM with flavor1",
			request: testlibAPI.MockRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								FlavorID: "flavor1",
							},
						},
						NInstances: 1,
					},
				},
				Hosts: []string{"host1", "host2"},
			},
			expectedWeights: map[string]float64{
				"host1": 1.0 + 0.5 + 0.5, // CPU: 4/4, RAM: 1024/2048, Disk: 100/200
				"host2": 0.5 + 1.0 + 1.0, // CPU: 2/4, RAM: 2048/2048, Disk: 200/200
			},
		},
		{
			name: "Single VM with flavor2",
			request: testlibAPI.MockRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								FlavorID: "flavor2",
							},
						},
						NInstances: 1,
					},
				},
				Hosts: []string{"host1", "host2"},
			},
			expectedWeights: map[string]float64{
				"host1": 0.25 + 0.25 + 0.25, // CPU: 1/4, RAM: 512/2048, Disk: 50/200
				"host2": 0.0 + 0.0 + 0.0,    // No matching flavor
			},
		},
		{
			name: "Multiple VMs",
			request: testlibAPI.MockRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								FlavorID: "flavor1",
							},
						},
						NInstances: 2,
					},
				},
				Hosts: []string{"host1", "host2"},
			},
			expectedWeights: map[string]float64{
				"host1": 0.0, // No weight change for multiple VMs
				"host2": 0.0, // No weight change for multiple VMs
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := step.Run(slog.Default(), &tt.request)
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
