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
	testlibAPI "github.com/cobaltcore-dev/cortex/testlib/scheduler/api"
)

func TestFlavorBinpackingStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(
		testDB.AddTable(shared.HostSpace{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the feature_flavor_host_space table
	_, err = testDB.Exec(`
        INSERT INTO feature_host_space (compute_host, ram_left_mb, vcpus_left, disk_left_gb, ram_left_pct, vcpus_left_pct, disk_left_pct)
        VALUES
            ('host1', 1024, 1, 100, 50, 25, 50),
            ('host2', 2048, 2, 200, 100, 12.5, 100)
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create an instance of the step
	opts := conf.NewRawOpts(`{
        "cpuEnabled": true,
        "cpuFreeLowerBound": 2,
        "cpuFreeUpperBound": 0,
        "cpuFreeActivationLowerBound": 0.0,
        "cpuFreeActivationUpperBound": 1.0,
        "ramEnabled": true,
        "ramFreeLowerBound": 2048,
        "ramFreeUpperBound": 0,
        "ramFreeActivationLowerBound": 0.0,
        "ramFreeActivationUpperBound": 1.0,
        "diskEnabled": true,
        "diskFreeLowerBound": 200,
        "diskFreeUpperBound": 0,
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
								// Fits ideally on host2
								MemoryMB:   2048,
								VCPUs:      2,
								RootDiskGB: 200,
							},
						},
						NInstances: 1,
					},
				},
				Hosts: []string{"host1", "host2", "host3"},
			},
			expectedWeights: map[string]float64{
				"host1": 0.0, // Too big for that host
				"host2": 3.0, // 2048 - 2048 = 0%, 2 - 2 = 0%, 200 - 200 = 0% --> perfect fit
			},
		},
		{
			name: "Single VM with flavor2",
			request: testlibAPI.MockRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								FlavorID:   "flavor2",
								MemoryMB:   1024,
								VCPUs:      2,
								RootDiskGB: 100,
							},
						},
						NInstances: 1,
					},
				},
				Hosts: []string{"host1", "host2", "host3"},
			},
			expectedWeights: map[string]float64{
				"host1": 0.0, // Too big for that host
				"host2": 2.0, // 2048 - 1024 = 50%, 2 - 2 = 0%, 200 - 100 = 50%
			},
		},
		{
			name: "Multiple VMs",
			request: testlibAPI.MockRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								FlavorID:   "flavor1",
								MemoryMB:   1024,
								VCPUs:      2,
								RootDiskGB: 100,
							},
						},
						NInstances: 2,
					},
				},
				Hosts: []string{"host1", "host2", "host3"},
			},
			// Not supported right now.
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
