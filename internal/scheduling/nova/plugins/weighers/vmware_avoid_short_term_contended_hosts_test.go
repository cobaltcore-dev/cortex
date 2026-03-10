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
	testlib "github.com/cobaltcore-dev/cortex/pkg/testing"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestVMwareAvoidShortTermContendedHostsStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name      string
		opts      VMwareAvoidShortTermContendedHostsStepOpts
		wantError bool
	}{
		{
			name: "valid opts with different bounds",
			opts: VMwareAvoidShortTermContendedHostsStepOpts{
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
			opts: VMwareAvoidShortTermContendedHostsStepOpts{
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
			opts: VMwareAvoidShortTermContendedHostsStepOpts{
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

func TestVMwareAvoidShortTermContendedHostsStep_Init(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	params := []v1alpha1.Parameter{
		{
			Key:        "avgCPUContentionLowerBound",
			FloatValue: testlib.Ptr(0.0),
		},
		{
			Key:        "avgCPUContentionUpperBound",
			FloatValue: testlib.Ptr(100.0),
		},
		{
			Key:        "avgCPUContentionActivationLowerBound",
			FloatValue: testlib.Ptr(0.0),
		},
		{
			Key:        "avgCPUContentionActivationUpperBound",
			FloatValue: testlib.Ptr(-1.0),
		},
		{
			Key:        "maxCPUContentionLowerBound",
			FloatValue: testlib.Ptr(0.0),
		},
		{
			Key:        "maxCPUContentionUpperBound",
			FloatValue: testlib.Ptr(100.0),
		},
		{
			Key:        "maxCPUContentionActivationLowerBound",
			FloatValue: testlib.Ptr(0.0),
		},
		{
			Key:        "maxCPUContentionActivationUpperBound",
			FloatValue: testlib.Ptr(-1.0),
		},
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
				ObjectMeta: metav1.ObjectMeta{Name: "vmware-short-term-contended-hosts"},
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
				Name:   "vmware_avoid_short_term_contended_hosts",
				Params: params,
			},
			wantError: false,
		},
		{
			name:      "fails when knowledge doesn't exist",
			knowledge: nil,
			weigherSpec: v1alpha1.WeigherSpec{
				Name:   "vmware_avoid_short_term_contended_hosts",
				Params: params,
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

			step := &VMwareAvoidShortTermContendedHostsStep{}
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

func TestVMwareAvoidShortTermContendedHostsStep_Run(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	vropsHostsystemContentionShortTerm, err := v1alpha1.BoxFeatureList([]any{
		&compute.VROpsHostsystemContentionShortTerm{ComputeHost: "host1", AvgCPUContention: 0.0, MaxCPUContention: 0.0},
		&compute.VROpsHostsystemContentionShortTerm{ComputeHost: "host2", AvgCPUContention: 100.0, MaxCPUContention: 0.0},
		&compute.VROpsHostsystemContentionShortTerm{ComputeHost: "host3", AvgCPUContention: 0.0, MaxCPUContention: 100.0},
		&compute.VROpsHostsystemContentionShortTerm{ComputeHost: "host4", AvgCPUContention: 100.0, MaxCPUContention: 100.0},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create an instance of the step
	step := &VMwareAvoidShortTermContendedHostsStep{}
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
			ObjectMeta: metav1.ObjectMeta{Name: "vmware-short-term-contended-hosts"},
			Status:     v1alpha1.KnowledgeStatus{Raw: vropsHostsystemContentionShortTerm},
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
					{ComputeHost: "host5"}, // No data for host5
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
			result, err := step.Run(slog.Default(), tt.request)
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
