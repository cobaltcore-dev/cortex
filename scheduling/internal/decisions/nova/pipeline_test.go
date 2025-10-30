// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"log/slog"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestSupportedSteps(t *testing.T) {
	// Test that we have the expected supported steps
	expectedSteps := []string{
		"vmware_anti_affinity_noisy_projects",
		"vmware_avoid_long_term_contended_hosts",
		"vmware_avoid_short_term_contended_hosts",
		"kvm_avoid_overloaded_hosts_cpu",
		"kvm_avoid_overloaded_hosts_memory",
		"shared_resource_balancing",
		"filter_has_accelerators",
		"filter_correct_az",
		"filter_disabled",
		"filter_packed_virtqueue",
		"filter_external_customer",
		"filter_project_aggregates",
		"filter_compute_capabilities",
		"filter_has_requested_traits",
		"filter_has_enough_capacity",
		"filter_host_instructions",
	}

	for _, stepName := range expectedSteps {
		if _, exists := supportedSteps[stepName]; !exists {
			t.Errorf("Expected supported step %s not found", stepName)
		}
	}

	// Test that we can create instances of all supported steps
	for stepName, stepFactory := range supportedSteps {
		step := stepFactory()
		if step == nil {
			t.Errorf("Step factory for %s returned nil", stepName)
		}
		if step.GetName() != stepName {
			t.Errorf("Expected step name %s but got %s", stepName, step.GetName())
		}
	}
}

func TestNewPipeline(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	tests := []struct {
		name        string
		steps       []v1alpha1.Step
		expectError bool
	}{
		{
			name:        "empty pipeline",
			steps:       []v1alpha1.Step{},
			expectError: false,
		},
		{
			name: "pipeline with supported step",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "filter-disabled",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeFilter,
						Impl: "filter_disabled",
					},
				},
			},
			expectError: false,
		},
		{
			name: "pipeline with unsupported step",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "unsupported-step",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeFilter,
						Impl: "unsupported-impl",
					},
				},
			},
			expectError: true,
		},
		{
			name: "pipeline with scoped step",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "scoped-filter",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeFilter,
						Impl: "filter_disabled",
						Opts: runtime.RawExtension{
							Raw: []byte(`{"scope":{"host_capabilities":{"any_of_trait_infixes":["TEST_TRAIT"]}}}`),
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "pipeline with invalid scope JSON",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "invalid-scoped-filter",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeFilter,
						Impl: "filter_disabled",
						Opts: runtime.RawExtension{
							Raw: []byte(`invalid json`),
						},
					},
				},
			},
			expectError: true,
		},
		{
			name: "pipeline with weigher step",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "weigher-step",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeWeigher,
						Impl: "shared_resource_balancing",
						Weigher: &v1alpha1.WeigherSpec{
							DisabledValidations: v1alpha1.DisabledValidationsSpec{
								SameSubjectNumberInOut: true,
							},
						},
					},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor := lib.PipelineMonitor{}
			pipeline, err := NewPipeline(tt.steps, testDB, monitor)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if !tt.expectError && pipeline == nil {
				t.Error("Expected pipeline but got nil")
			}
		})
	}
}

func TestPipelineRun(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	monitor := lib.PipelineMonitor{}
	pipeline, err := NewPipeline([]v1alpha1.Step{}, testDB, monitor)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}

	tests := []struct {
		name               string
		request            api.ExternalSchedulerRequest
		expectedHostsCount int
	}{
		{
			name: "basic nova request",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Name:      "RequestSpec",
					Namespace: "nova_object",
					Version:   "1.19",
					Data: api.NovaSpec{
						ProjectID:    "test-project",
						UserID:       "test-user",
						InstanceUUID: "test-instance-uuid",
						NumInstances: 1,
					},
				},
				Context: api.NovaRequestContext{
					ProjectID:       "test-project",
					UserID:          "test-user",
					RequestID:       "req-123",
					GlobalRequestID: func() *string { s := "global-req-123"; return &s }(),
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "compute-1", HypervisorHostname: "hv-1"},
					{ComputeHost: "compute-2", HypervisorHostname: "hv-2"},
				},
				Weights:  map[string]float64{"compute-1": 1.0, "compute-2": 0.5},
				Pipeline: "test-pipeline",
			},
			expectedHostsCount: 2,
		},
		{
			name: "single host request",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Name:      "RequestSpec",
					Namespace: "nova_object",
					Version:   "1.19",
					Data: api.NovaSpec{
						ProjectID:    "test-project",
						UserID:       "test-user",
						InstanceUUID: "test-instance-single",
						NumInstances: 1,
					},
				},
				Context: api.NovaRequestContext{
					ProjectID: "test-project",
					UserID:    "test-user",
					RequestID: "req-456",
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "compute-1", HypervisorHostname: "hv-1"},
				},
				Weights:  map[string]float64{"compute-1": 2.0},
				Pipeline: "test-pipeline",
			},
			expectedHostsCount: 1,
		},
		{
			name: "no hosts request",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Name:      "RequestSpec",
					Namespace: "nova_object",
					Version:   "1.19",
					Data: api.NovaSpec{
						ProjectID:    "test-project",
						UserID:       "test-user",
						InstanceUUID: "test-instance-empty",
						NumInstances: 1,
					},
				},
				Context: api.NovaRequestContext{
					ProjectID: "test-project",
					UserID:    "test-user",
					RequestID: "req-789",
				},
				Hosts:    []api.ExternalSchedulerHost{},
				Weights:  map[string]float64{},
				Pipeline: "test-pipeline",
			},
			expectedHostsCount: 0,
		},
		{
			name: "vmware request",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Name:      "RequestSpec",
					Namespace: "nova_object",
					Version:   "1.19",
					Data: api.NovaSpec{
						ProjectID:    "test-project",
						UserID:       "test-user",
						InstanceUUID: "test-vmware-instance",
						NumInstances: 1,
					},
				},
				Context: api.NovaRequestContext{
					ProjectID: "test-project",
					UserID:    "test-user",
					RequestID: "req-vmware",
				},
				VMware: true,
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "vmware-compute-1", HypervisorHostname: "domain-c123.uuid"},
				},
				Weights:  map[string]float64{"vmware-compute-1": 1.0},
				Pipeline: "test-pipeline",
			},
			expectedHostsCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := pipeline.Run(tt.request)

			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if len(result.OrderedHosts) != tt.expectedHostsCount {
				t.Errorf("Expected %d hosts but got %d", tt.expectedHostsCount, len(result.OrderedHosts))
			}

			if tt.expectedHostsCount > 0 {
				if result.TargetHost == nil {
					t.Error("Expected target host to be set but was nil")
				} else if *result.TargetHost != result.OrderedHosts[0] {
					t.Errorf("Expected target host %s but got %s", result.OrderedHosts[0], *result.TargetHost)
				}
			} else {
				if result.TargetHost != nil {
					t.Error("Expected target host to be nil but was set")
				}
			}

			if result.RawInWeights == nil {
				t.Error("Expected raw input weights to be set")
			}

			if result.NormalizedInWeights == nil {
				t.Error("Expected normalized input weights to be set")
			}

			if result.AggregatedOutWeights == nil {
				t.Error("Expected aggregated output weights to be set")
			}
		})
	}
}

func TestNovaPipelineType(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	var step NovaStep = &mockNovaStep{}

	// Test initialization
	opts := conf.NewRawOpts(`{}`)
	err := step.Init(testDB, opts)
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}
}

// Mock implementation for testing
type mockNovaStep struct{}

func (s *mockNovaStep) Init(db db.DB, opts conf.RawOpts) error {
	return nil
}

func (s *mockNovaStep) Run(logger *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	return &lib.StepResult{
		Activations: make(map[string]float64),
	}, nil
}

func (s *mockNovaStep) GetName() string {
	return "mock-nova-step"
}
