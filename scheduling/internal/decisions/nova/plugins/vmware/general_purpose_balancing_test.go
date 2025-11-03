// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestGeneralPurposeBalancingStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    GeneralPurposeBalancingStepOpts
		wantErr bool
	}{
		{
			name: "valid options",
			opts: GeneralPurposeBalancingStepOpts{
				RAMUtilizedAfterLowerBoundPct: 20.0,
				RAMUtilizedAfterUpperBoundPct: 80.0,
			},
			wantErr: false,
		},
		{
			name: "equal bounds - should error",
			opts: GeneralPurposeBalancingStepOpts{
				RAMUtilizedAfterLowerBoundPct: 50.0,
				RAMUtilizedAfterUpperBoundPct: 50.0,
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

func TestGeneralPurposeBalancingStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
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
			RAMUtilizedPct:        60.0,
			TotalRAMAllocatableMB: 32768.0,
		},
		&shared.HostUtilization{
			ComputeHost:           "host2",
			RAMUtilizedPct:        40.0,
			TotalRAMAllocatableMB: 16384.0,
		},
		&shared.HostUtilization{
			ComputeHost:           "host3",
			RAMUtilizedPct:        80.0,
			TotalRAMAllocatableMB: 65536.0,
		},
	}
	if err := testDB.Insert(hostUtilizations...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostCapabilities := []any{
		&shared.HostCapabilities{
			ComputeHost: "host1",
			Traits:      "CUSTOM_TRAIT_1",
		},
		&shared.HostCapabilities{
			ComputeHost: "host2",
			Traits:      "CUSTOM_TRAIT_2",
		},
		&shared.HostCapabilities{
			ComputeHost: "host3",
			Traits:      "HANA_EXCLUSIVE,OTHER_TRAIT",
		},
	}
	if err := testDB.Insert(hostCapabilities...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	step := &GeneralPurposeBalancingStep{}
	step.Options = GeneralPurposeBalancingStepOpts{
		RAMUtilizedAfterLowerBoundPct:        20.0,
		RAMUtilizedAfterUpperBoundPct:        70.0,
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
			name: "General purpose VM on VMware",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.medium",
								MemoryMB: 4096,
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
				"host1": true,  // should get activation (60% - 12.5% = 47.5%, in range 20-70%)
				"host2": false, // should be no-effect (40% - 25% = 15%, below 20% range)
				"host3": false, // HANA_EXCLUSIVE host should be no-effect
			},
		},
		{
			name: "HANA flavor should be skipped",
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
								Name:     "m1.medium",
								MemoryMB: 4096,
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
					// For general purpose balancing, we expect some calculated activation based on RAM utilization
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
			if tt.name == "General purpose VM on VMware" {
				if _, ok := result.Statistics["ram utilized after"]; !ok {
					t.Error("expected 'ram utilized after' statistic")
				}
			}
		})
	}
}
