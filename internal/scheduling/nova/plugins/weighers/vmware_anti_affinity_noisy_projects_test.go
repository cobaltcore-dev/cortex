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

func TestVMwareAntiAffinityNoisyProjectsStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name      string
		opts      VMwareAntiAffinityNoisyProjectsStepOpts
		wantError bool
	}{
		{
			name: "valid opts with different bounds",
			opts: VMwareAntiAffinityNoisyProjectsStepOpts{
				AvgCPUUsageLowerBound:           20.0,
				AvgCPUUsageUpperBound:           100.0,
				AvgCPUUsageActivationLowerBound: 0.0,
				AvgCPUUsageActivationUpperBound: -0.5,
			},
			wantError: false,
		},
		{
			name: "invalid opts - equal bounds causes zero division",
			opts: VMwareAntiAffinityNoisyProjectsStepOpts{
				AvgCPUUsageLowerBound:           50.0,
				AvgCPUUsageUpperBound:           50.0, // Same as lower bound
				AvgCPUUsageActivationLowerBound: 0.0,
				AvgCPUUsageActivationUpperBound: -0.5,
			},
			wantError: true,
		},
		{
			name: "valid opts with zero bounds",
			opts: VMwareAntiAffinityNoisyProjectsStepOpts{
				AvgCPUUsageLowerBound:           0.0,
				AvgCPUUsageUpperBound:           100.0,
				AvgCPUUsageActivationLowerBound: 0.0,
				AvgCPUUsageActivationUpperBound: 1.0,
			},
			wantError: false,
		},
		{
			name: "valid opts with negative values",
			opts: VMwareAntiAffinityNoisyProjectsStepOpts{
				AvgCPUUsageLowerBound:           -10.0,
				AvgCPUUsageUpperBound:           10.0,
				AvgCPUUsageActivationLowerBound: -1.0,
				AvgCPUUsageActivationUpperBound: 1.0,
			},
			wantError: false,
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

func TestVMwareAntiAffinityNoisyProjectsStep_Init(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Valid params JSON for the weigher
	validParams := runtime.RawExtension{
		Raw: []byte(`{"avgCPUUsageLowerBound": 20.0, "avgCPUUsageUpperBound": 100.0, "avgCPUUsageActivationLowerBound": 0.0, "avgCPUUsageActivationUpperBound": -0.5}`),
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
				ObjectMeta: metav1.ObjectMeta{Name: "vmware-project-noisiness"},
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
				Name:   "vmware_anti_affinity_noisy_projects",
				Params: validParams,
			},
			wantError: false,
		},
		{
			name:      "fails when knowledge doesn't exist",
			knowledge: nil,
			weigherSpec: v1alpha1.WeigherSpec{
				Name:   "vmware_anti_affinity_noisy_projects",
				Params: validParams,
			},
			wantError:     true,
			errorContains: "failed to get knowledge",
		},
		{
			name: "fails when knowledge not ready",
			knowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{Name: "vmware-project-noisiness"},
				Status: v1alpha1.KnowledgeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.KnowledgeConditionReady,
							Status: metav1.ConditionFalse,
						},
					},
					RawLength: 0,
				},
			},
			weigherSpec: v1alpha1.WeigherSpec{
				Name:   "vmware_anti_affinity_noisy_projects",
				Params: validParams,
			},
			wantError:     true,
			errorContains: "not ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.knowledge != nil {
				builder = builder.WithObjects(tt.knowledge)
			}
			client := builder.Build()

			step := &VMwareAntiAffinityNoisyProjectsStep{}
			err := step.Init(context.Background(), client, tt.weigherSpec)

			if tt.wantError {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errorContains != "" && !containsString(err.Error(), tt.errorContains) {
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

func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestVMwareAntiAffinityNoisyProjectsStep_Run(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	vropsProjectNoisiness, err := v1alpha1.BoxFeatureList([]any{
		&compute.VROpsProjectNoisiness{Project: "project1", ComputeHost: "host1", AvgCPUOfProject: 25.0},
		&compute.VROpsProjectNoisiness{Project: "project1", ComputeHost: "host2", AvgCPUOfProject: 30.0},
		&compute.VROpsProjectNoisiness{Project: "project2", ComputeHost: "host3", AvgCPUOfProject: 15.0},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	step := &VMwareAntiAffinityNoisyProjectsStep{}
	step.Options.AvgCPUUsageLowerBound = 20.0
	step.Options.AvgCPUUsageUpperBound = 100.0
	step.Options.AvgCPUUsageActivationLowerBound = 0.0
	step.Options.AvgCPUUsageActivationUpperBound = -0.5
	step.Client = fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&v1alpha1.Knowledge{
			ObjectMeta: metav1.ObjectMeta{Name: "vmware-project-noisiness"},
			Status:     v1alpha1.KnowledgeStatus{Raw: vropsProjectNoisiness},
		}).
		Build()

	tests := []struct {
		name           string
		request        api.ExternalSchedulerRequest
		downvotedHosts map[string]struct{}
	}{
		{
			name: "Noisy project",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project1",
					},
				},
				VMware: true,
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			downvotedHosts: map[string]struct{}{
				"host1": {},
				"host2": {},
			},
		},
		{
			name: "Non-noisy project",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project2",
					},
				},
				VMware: true,
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			downvotedHosts: map[string]struct{}{},
		},
		{
			name: "No noisy project data",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						ProjectID: "project3",
					},
				},
				VMware: true,
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			downvotedHosts: map[string]struct{}{},
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
				if _, ok := tt.downvotedHosts[host]; ok {
					if weight >= 0 {
						t.Errorf("expected weight for host %s to be less than 0, got %f", host, weight)
					}
				} else {
					if weight != 0 {
						t.Errorf("expected weight for host %s to be 0, got %f", host, weight)
					}
				}
			}
		})
	}
}
