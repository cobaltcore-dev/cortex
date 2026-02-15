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

func TestVMwareAvoidLongTermContendedHostsStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name      string
		opts      VMwareAvoidLongTermContendedHostsStepOpts
		wantError bool
	}{
		{
			name: "valid opts with different bounds",
			opts: VMwareAvoidLongTermContendedHostsStepOpts{
				AvgCPUContentionLowerBound:           0.0,
				AvgCPUContentionUpperBound:           100.0,
				AvgCPUContentionActivationLowerBound: 0.0,
				AvgCPUContentionActivationUpperBound: -1.0,
				MaxCPUContentionLowerBound:           0.0,
				MaxCPUContentionUpperBound:           100.0,
				MaxCPUContentionActivationLowerBound: 0.0,
				MaxCPUContentionActivationUpperBound: -1.0,
			},
			wantError: false,
		},
		{
			name: "invalid opts - equal avg bounds",
			opts: VMwareAvoidLongTermContendedHostsStepOpts{
				AvgCPUContentionLowerBound:           50.0,
				AvgCPUContentionUpperBound:           50.0, // Same as lower
				AvgCPUContentionActivationLowerBound: 0.0,
				AvgCPUContentionActivationUpperBound: -1.0,
				MaxCPUContentionLowerBound:           0.0,
				MaxCPUContentionUpperBound:           100.0,
				MaxCPUContentionActivationLowerBound: 0.0,
				MaxCPUContentionActivationUpperBound: -1.0,
			},
			wantError: true,
		},
		{
			name: "invalid opts - equal max bounds",
			opts: VMwareAvoidLongTermContendedHostsStepOpts{
				AvgCPUContentionLowerBound:           0.0,
				AvgCPUContentionUpperBound:           100.0,
				AvgCPUContentionActivationLowerBound: 0.0,
				AvgCPUContentionActivationUpperBound: -1.0,
				MaxCPUContentionLowerBound:           50.0,
				MaxCPUContentionUpperBound:           50.0, // Same as lower
				MaxCPUContentionActivationLowerBound: 0.0,
				MaxCPUContentionActivationUpperBound: -1.0,
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if (err != nil) != tt.wantError {
				t.Errorf("Validate() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestVMwareAvoidLongTermContendedHostsStep_Init(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	validParams := runtime.RawExtension{
		Raw: []byte(`{
			"avgCPUContentionLowerBound": 0,
			"avgCPUContentionUpperBound": 100,
			"avgCPUContentionActivationLowerBound": 0,
			"avgCPUContentionActivationUpperBound": -1,
			"maxCPUContentionLowerBound": 0,
			"maxCPUContentionUpperBound": 100,
			"maxCPUContentionActivationLowerBound": 0,
			"maxCPUContentionActivationUpperBound": -1
		}`),
	}

	tests := []struct {
		name          string
		knowledge     *v1alpha1.Knowledge
		weigherSpec   v1alpha1.WeigherSpec
		wantError     bool
		errorContains string
	}{
		{
			name: "successful init with valid knowledge",
			knowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{Name: "vmware-long-term-contended-hosts"},
				Status: v1alpha1.KnowledgeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.KnowledgeConditionReady,
							Status: metav1.ConditionTrue,
						},
					},
					RawLength: 10,
				},
			},
			weigherSpec: v1alpha1.WeigherSpec{
				Name:   "vmware_avoid_long_term_contended_hosts",
				Params: validParams,
			},
			wantError: false,
		},
		{
			name:      "fails when knowledge doesn't exist",
			knowledge: nil,
			weigherSpec: v1alpha1.WeigherSpec{
				Name:   "vmware_avoid_long_term_contended_hosts",
				Params: validParams,
			},
			wantError:     true,
			errorContains: "failed to get knowledge",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.knowledge != nil {
				builder = builder.WithObjects(tt.knowledge)
			}
			client := builder.Build()

			step := &VMwareAvoidLongTermContendedHostsStep{}
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

func TestVMwareAvoidLongTermContendedHostsStep_Run(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	vropsHostsystemContentionLongTerm, err := v1alpha1.BoxFeatureList([]any{
		&compute.VROpsHostsystemContentionLongTerm{ComputeHost: "host1", AvgCPUContention: 0.0, MaxCPUContention: 0.0},
		&compute.VROpsHostsystemContentionLongTerm{ComputeHost: "host2", AvgCPUContention: 100.0, MaxCPUContention: 0.0},
		&compute.VROpsHostsystemContentionLongTerm{ComputeHost: "host3", AvgCPUContention: 0.0, MaxCPUContention: 100.0},
		&compute.VROpsHostsystemContentionLongTerm{ComputeHost: "host4", AvgCPUContention: 100.0, MaxCPUContention: 100.0},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create an instance of the step
	step := &VMwareAvoidLongTermContendedHostsStep{}
	step.Options.AvgCPUContentionLowerBound = 0
	step.Options.AvgCPUContentionUpperBound = 100
	step.Options.AvgCPUContentionActivationLowerBound = 0.0
	step.Options.AvgCPUContentionActivationUpperBound = -1.0
	step.Options.MaxCPUContentionLowerBound = 0
	step.Options.MaxCPUContentionUpperBound = 100
	step.Options.MaxCPUContentionActivationLowerBound = 0.0
	step.Options.MaxCPUContentionActivationUpperBound = -1.0
	step.Client = fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&v1alpha1.Knowledge{
			ObjectMeta: metav1.ObjectMeta{Name: "vmware-long-term-contended-hosts"},
			Status:     v1alpha1.KnowledgeStatus{Raw: vropsHostsystemContentionLongTerm},
		}).
		Build()

	tests := []struct {
		name     string
		request  api.ExternalSchedulerRequest
		expected map[string]float64
	}{
		{
			name: "Avoid contended hosts",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expected: map[string]float64{
				"host1": 0,
				"host2": -1,
				"host3": -1,
				"host4": -2, // Max and avg contention stack up.
			},
		},
		{
			name: "Missing data",
			request: api.ExternalSchedulerRequest{
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
				},
			},
			expected: map[string]float64{
				"host4": -2,
				"host5": 0, // No data but still contained in the result.
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := step.Run(t.Context(), slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			// Check that the weights have decreased
			for host, weight := range result.Activations {
				expected := tt.expected[host]
				if weight != expected {
					t.Errorf("expected weight for host %s to be %f, got %f", host, expected, weight)
				}
			}
		})
	}
}
