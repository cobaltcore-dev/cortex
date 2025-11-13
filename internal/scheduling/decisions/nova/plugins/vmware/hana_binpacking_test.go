// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
)

func TestHanaBinpackingStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    HanaBinpackingStepOpts
		wantErr bool
	}{
		{
			name: "valid options",
			opts: HanaBinpackingStepOpts{
				RAMUtilizedAfterLowerBoundPct: 30.0,
				RAMUtilizedAfterUpperBoundPct: 90.0,
			},
			wantErr: false,
		},
		{
			name: "equal bounds - should error",
			opts: HanaBinpackingStepOpts{
				RAMUtilizedAfterLowerBoundPct: 60.0,
				RAMUtilizedAfterUpperBoundPct: 60.0,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHanaBinpackingStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Create dependency tables
	err := testDB.CreateTable(testDB.AddTable(shared.HostUtilization{}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	err = testDB.CreateTable(testDB.AddTable(shared.HostCapabilities{}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostUtilizations := []any{
		&shared.HostUtilization{
			ComputeHost:           "host1",
			RAMUtilizedPct:        50.0,
			TotalRAMAllocatableMB: 65536.0,
		},
		&shared.HostUtilization{
			ComputeHost:           "host2",
			RAMUtilizedPct:        70.0,
			TotalRAMAllocatableMB: 131072.0,
		},
		&shared.HostUtilization{
			ComputeHost:           "host3",
			RAMUtilizedPct:        30.0,
			TotalRAMAllocatableMB: 32768.0,
		},
	}
	if err := testDB.Insert(hostUtilizations...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostCapabilities := []any{
		&shared.HostCapabilities{
			ComputeHost: "host1",
			Traits:      "HANA_EXCLUSIVE,CUSTOM_TRAIT_1",
		},
		&shared.HostCapabilities{
			ComputeHost: "host2",
			Traits:      "HANA_EXCLUSIVE,CUSTOM_TRAIT_2",
		},
		&shared.HostCapabilities{
			ComputeHost: "host3",
			Traits:      "CUSTOM_TRAIT_3",
		},
	}
	if err := testDB.Insert(hostCapabilities...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	step := &HanaBinpackingStep{}
	step.Options = HanaBinpackingStepOpts{
		RAMUtilizedAfterLowerBoundPct:        30.0,
		RAMUtilizedAfterUpperBoundPct:        80.0,
		RAMUtilizedAfterActivationLowerBound: 0.0,
		RAMUtilizedAfterActivationUpperBound: 1.0,
	}
	step.DB = &testDB

	tests := []struct {
		name          string
		request       api.ExternalSchedulerRequest
		expectedHosts map[string]bool // true if host should have modified activation
	}{
		{
			name: "HANA VM on VMware",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "hana.xlarge",
								MemoryMB: 16384,
							},
						},
					},
				},
				VMware: true,
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			expectedHosts: map[string]bool{
				"host1": false, // HANA_EXCLUSIVE host but below range (50% - 25% = 25%, below 30% range)
				"host2": true,  // HANA_EXCLUSIVE host should get activation (70% - 12.5% = 57.5%, in range 30-80%)
				"host3": false, // non-HANA_EXCLUSIVE host should be no-effect
			},
		},
		{
			name: "Non-HANA flavor should be skipped",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.large",
								MemoryMB: 8192,
							},
						},
					},
				},
				VMware: true,
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: map[string]bool{
				"host1": false, // should be no-effect
				"host2": false, // should be no-effect
			},
		},
		{
			name: "Non-VMware VM should be skipped",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "hana.large",
								MemoryMB: 8192,
							},
						},
					},
				},
				VMware: false,
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			expectedHosts: map[string]bool{
				"host1": false, // should be no-effect
				"host2": false, // should be no-effect
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if result == nil {
				t.Fatal("expected result, got nil")
			}

			for host, shouldHaveActivation := range tt.expectedHosts {
				activation, ok := result.Activations[host]
				if !ok {
					t.Errorf("expected activation for host %s", host)
					continue
				}

				if shouldHaveActivation {
					// For HANA binpacking, we expect some calculated activation based on RAM utilization
					if activation == 0 {
						t.Errorf("expected non-zero activation for host %s, got %f", host, activation)
					}
				} else {
					// Should be no-effect (0)
					if activation != 0 {
						t.Errorf("expected no-effect (0) activation for host %s, got %f", host, activation)
					}
				}
			}

			// Check statistics
			if tt.name == "HANA VM on VMware" {
				if _, ok := result.Statistics["ram utilized after"]; !ok {
					t.Error("expected 'ram utilized after' statistic")
				}
			}
		})
	}
}
