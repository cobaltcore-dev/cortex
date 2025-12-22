// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestVMwareHanaBinpackingStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    VMwareHanaBinpackingStepOpts
		wantErr bool
	}{
		{
			name: "valid options",
			opts: VMwareHanaBinpackingStepOpts{
				RAMUtilizedAfterLowerBoundPct: 30.0,
				RAMUtilizedAfterUpperBoundPct: 90.0,
			},
			wantErr: false,
		},
		{
			name: "equal bounds - should error",
			opts: VMwareHanaBinpackingStepOpts{
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

func TestVMwareHanaBinpackingStep_Run(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostUtilizations, err := v1alpha1.BoxFeatureList([]any{
		&compute.HostUtilization{
			ComputeHost:           "host1",
			RAMUtilizedPct:        50.0,
			TotalRAMAllocatableMB: 65536.0,
		},
		&compute.HostUtilization{
			ComputeHost:           "host2",
			RAMUtilizedPct:        70.0,
			TotalRAMAllocatableMB: 131072.0,
		},
		&compute.HostUtilization{
			ComputeHost:           "host3",
			RAMUtilizedPct:        30.0,
			TotalRAMAllocatableMB: 32768.0,
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	hostCapabilities, err := v1alpha1.BoxFeatureList([]any{
		&compute.HostCapabilities{
			ComputeHost: "host1",
			Traits:      "HANA_EXCLUSIVE,CUSTOM_TRAIT_1",
		},
		&compute.HostCapabilities{
			ComputeHost: "host2",
			Traits:      "HANA_EXCLUSIVE,CUSTOM_TRAIT_2",
		},
		&compute.HostCapabilities{
			ComputeHost: "host3",
			Traits:      "CUSTOM_TRAIT_3",
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	step := &VMwareHanaBinpackingStep{}
	step.Options = VMwareHanaBinpackingStepOpts{
		RAMUtilizedAfterLowerBoundPct:        30.0,
		RAMUtilizedAfterUpperBoundPct:        80.0,
		RAMUtilizedAfterActivationLowerBound: 0.0,
		RAMUtilizedAfterActivationUpperBound: 1.0,
	}
	step.Client = fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: "host-utilization"},
			Status:     v1alpha1.KnowledgeStatus{Raw: hostUtilizations},
		}).
		WithObjects(&v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: "host-capabilities"},
			Status:     v1alpha1.KnowledgeStatus{Raw: hostCapabilities},
		}).
		Build()

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
				"host1": true,  // HANA_EXCLUSIVE host should get activation (50% + 25% = 75%, in range 30-80%)
				"host2": false, // HANA_EXCLUSIVE host but above range (70% + 12.5% = 82.5%, above 80% range)
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
