// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestVMwareGeneralPurposeBalancingStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    VMwareGeneralPurposeBalancingStepOpts
		wantErr bool
	}{
		{
			name: "valid options",
			opts: VMwareGeneralPurposeBalancingStepOpts{
				RAMUtilizedLowerBoundPct: 20.0,
				RAMUtilizedUpperBoundPct: 80.0,
			},
			wantErr: false,
		},
		{
			name: "equal bounds - should error",
			opts: VMwareGeneralPurposeBalancingStepOpts{
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

func TestVMwareGeneralPurposeBalancingStep_Init(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	validParams := runtime.RawExtension{
		Raw: []byte(`{
			"ramUtilizedLowerBoundPct": 20.0,
			"ramUtilizedUpperBoundPct": 80.0,
			"ramUtilizedActivationLowerBound": 0.0,
			"ramUtilizedActivationUpperBound": 1.0
		}`),
	}

	tests := []struct {
		name          string
		knowledges    []*v1alpha1.Knowledge
		weigherSpec   v1alpha1.WeigherSpec
		wantError     bool
		errorContains string
	}{
		{
			name: "successful init with valid knowledges",
			knowledges: []*v1alpha1.Knowledge{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host-utilization"},
					Status: v1alpha1.KnowledgeStatus{
						Conditions: []metav1.Condition{
							{Type: v1alpha1.KnowledgeConditionReady, Status: metav1.ConditionTrue},
						},
						RawLength: 10,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host-capabilities"},
					Status: v1alpha1.KnowledgeStatus{
						Conditions: []metav1.Condition{
							{Type: v1alpha1.KnowledgeConditionReady, Status: metav1.ConditionTrue},
						},
						RawLength: 10,
					},
				},
			},
			weigherSpec: v1alpha1.WeigherSpec{
				Name:   "vmware_general_purpose_balancing",
				Params: validParams,
			},
			wantError: false,
		},
		{
			name:       "fails when host-utilization knowledge doesn't exist",
			knowledges: nil,
			weigherSpec: v1alpha1.WeigherSpec{
				Name:   "vmware_general_purpose_balancing",
				Params: validParams,
			},
			wantError:     true,
			errorContains: "failed to get knowledge",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			for _, k := range tt.knowledges {
				builder = builder.WithObjects(k)
			}
			client := builder.Build()

			step := &VMwareGeneralPurposeBalancingStep{}
			err := step.Init(context.Background(), client, tt.weigherSpec)

			if tt.wantError {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("expected error containing %q, got %q", tt.errorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestVMwareGeneralPurposeBalancingStep_Run(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert test data
	hostUtilizations, err := v1alpha1.BoxFeatureList([]any{
		&compute.HostUtilization{
			ComputeHost:    "host1",
			RAMUtilizedPct: 30.0,
		},
		&compute.HostUtilization{
			ComputeHost:    "host2",
			RAMUtilizedPct: 60.0,
		},
		&compute.HostUtilization{
			ComputeHost:    "host3",
			RAMUtilizedPct: 90.0,
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	hostCapabilities, err := v1alpha1.BoxFeatureList([]any{
		&compute.HostCapabilities{
			ComputeHost: "host1",
			Traits:      "CUSTOM_TRAIT_1",
		},
		&compute.HostCapabilities{
			ComputeHost: "host2",
			Traits:      "CUSTOM_TRAIT_2",
		},
		&compute.HostCapabilities{
			ComputeHost: "host3",
			Traits:      "HANA_EXCLUSIVE,OTHER_TRAIT",
		},
		&compute.HostCapabilities{
			ComputeHost: "host4",
			Traits:      "GENERAL_PURPOSE",
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	step := &VMwareGeneralPurposeBalancingStep{}
	step.Options = VMwareGeneralPurposeBalancingStepOpts{
		RAMUtilizedLowerBoundPct:        20.0,
		RAMUtilizedUpperBoundPct:        80.0,
		RAMUtilizedActivationLowerBound: 0.0,
		RAMUtilizedActivationUpperBound: 1.0,
	}
	step.Client = fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&v1alpha1.Knowledge{
			ObjectMeta: metav1.ObjectMeta{Name: "host-utilization"},
			Status:     v1alpha1.KnowledgeStatus{Raw: hostUtilizations},
		}).
		WithObjects(&v1alpha1.Knowledge{
			ObjectMeta: metav1.ObjectMeta{Name: "host-capabilities"},
			Status:     v1alpha1.KnowledgeStatus{Raw: hostCapabilities},
		}).
		Build()

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
			result, err := step.Run(t.Context(), slog.Default(), tt.request)
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
