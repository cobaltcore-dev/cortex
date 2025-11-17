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

func TestGeneralPurposeBalancingStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    GeneralPurposeBalancingStepOpts
		wantErr bool
	}{
		{
			name: "valid options",
			opts: GeneralPurposeBalancingStepOpts{
				RAMUtilizedLowerBoundPct: 20.0,
				RAMUtilizedUpperBoundPct: 80.0,
			},
			wantErr: false,
		},
		{
			name: "equal bounds - should error",
			opts: GeneralPurposeBalancingStepOpts{
				RAMUtilizedLowerBoundPct: 50.0,
				RAMUtilizedUpperBoundPct: 50.0,
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

	// Insert test data
	hostUtilizations := []any{
		&shared.HostUtilization{
			ComputeHost:    "host1",
			RAMUtilizedPct: 30.0,
		},
		&shared.HostUtilization{
			ComputeHost:    "host2",
			RAMUtilizedPct: 60.0,
		},
		&shared.HostUtilization{
			ComputeHost:    "host3",
			RAMUtilizedPct: 90.0,
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
		&shared.HostCapabilities{
			ComputeHost: "host4",
			Traits:      "GENERAL_PURPOSE",
		},
	}
	if err := testDB.Insert(hostCapabilities...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	step := &GeneralPurposeBalancingStep{}
	step.Options = GeneralPurposeBalancingStepOpts{
		RAMUtilizedLowerBoundPct:        20.0,
		RAMUtilizedUpperBoundPct:        80.0,
		RAMUtilizedActivationLowerBound: 0.0,
		RAMUtilizedActivationUpperBound: 1.0,
	}
	step.DB = &testDB

	tests := []struct {
		name                string
		request             api.ExternalSchedulerRequest
		expectedActivations map[string]float64 // expected activation values
		expectStatistics    bool               // whether statistics should be present
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
			expectedActivations: map[string]float64{
				"host1": 0.16666666666666666, // (30-20)/(80-20) = 10/60 = 0.167
				"host2": 0.6666666666666666,  // (60-20)/(80-20) = 40/60 = 0.667
				"host3": 0.0,                 // HANA_EXCLUSIVE host should be no-effect
			},
			expectStatistics: true,
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
			expectedActivations: map[string]float64{
				"host1": 0.0, // should be no-effect
				"host2": 0.0, // should be no-effect
			},
			expectStatistics: false,
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
			expectedActivations: map[string]float64{
				"host1": 0.0, // should be no-effect
				"host2": 0.0, // should be no-effect
			},
			expectStatistics: false,
		},
		{
			name: "Host without capabilities gets no-effect",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								Name:     "m1.small",
								MemoryMB: 2048,
							},
						},
					},
				},
				VMware: true,
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host_unknown"}, // no capabilities for this host
				},
			},
			expectedActivations: map[string]float64{
				"host1":        0.16666666666666666, // normal scaling
				"host_unknown": 0.0,                 // no-effect due to missing capabilities
			},
			expectStatistics: true,
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

			// Check activations
			for host, expectedActivation := range tt.expectedActivations {
				activation, ok := result.Activations[host]
				if !ok {
					t.Errorf("expected activation for host %s", host)
					continue
				}

				// Use a small epsilon for floating point comparison
				epsilon := 1e-10
				if abs(activation-expectedActivation) > epsilon {
					t.Errorf("expected activation %.10f for host %s, got %.10f", expectedActivation, host, activation)
				}
			}

			// Check statistics
			if tt.expectStatistics {
				if _, ok := result.Statistics["ram utilized"]; !ok {
					t.Error("expected 'ram utilized' statistic")
				}
			} else {
				if _, ok := result.Statistics["ram utilized"]; ok {
					t.Error("did not expect 'ram utilized' statistic")
				}
			}
		})
	}
}

// Helper function for floating point comparison
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
